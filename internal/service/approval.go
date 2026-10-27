package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

// humanOnlyHint ends every human-only refusal. B-530's operator, refused,
// went looking for a way around the rule because the refusal named no way
// through it; this one does.
const humanOnlyHint = "an operator with the person's agreement files an approval request (MCP approval_request) and the person confirms it in the desktop"

// Approval statuses. A request moves pending → approved → landed | failed |
// interrupted, or pending → declined | superseded, and never back: only a
// pending request can be approved, so nothing decides twice. A retry after
// failed or interrupted is a NEW request, filed against whatever the run is
// waiting on then.
//
//   - approved: the person said yes and the decision was handed to its door;
//     it has not settled yet. With an accept that settles later (an accept
//     job that outlives the request — B-529), this is the state it waits in.
//   - landed: the decision was taken (Outcome names the commit).
//   - failed: the door refused it after the person approved (Outcome says
//     why); the run is still waiting, and nothing was decided.
//   - interrupted: the engine stopped while the decision was in flight; the
//     next engine found it approved and unsettled (settleInterruptedApprovals)
//     and says so rather than leaving "approved" with nothing happening. If
//     the accept had landed before the stop, the request reads landed.
const (
	ApprovalPending     = "pending"
	ApprovalApproved    = "approved"
	ApprovalDeclined    = "declined"
	ApprovalLanded      = "landed"
	ApprovalFailed      = "failed"
	ApprovalSuperseded  = "superseded"
	ApprovalInterrupted = "interrupted"
)

// approvalLocks holds one mutex per run. It guards only the TRANSITIONS of
// that run's requests — filing, the pending→approved claim, declining, the
// final outcome — never the decision itself.
//
// B-530 review (Codex): one global mutex was held through the approved
// accept, so an accept replaying its gate for minutes blocked filing and
// declining requests on every other run. The decision now runs with no
// approval lock held; the claim already made the request un-approvable a
// second time.
var approvalLocks sync.Map // run id -> *sync.Mutex

func approvalLock(runID string) *sync.Mutex {
	mu, _ := approvalLocks.LoadOrStore(runID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// decideApproval runs an approved request's decision. A variable so a test
// can hold one decision open and prove it blocks nothing else.
var decideApproval = func(s *Service, ctx context.Context, runID string, req runlog.ApprovalRequest, actor string) (*AcceptResult, error) {
	switch req.Action {
	case "accept":
		msg := fmt.Sprintf("approved %s, requested by %s: %s", req.ID, req.RequestedBy, req.Reason)
		if req.Message != "" {
			msg = req.Message + "\n\n" + msg
		}
		return s.RunAcceptAsWithOptions(ctx, runID, msg, actor, AcceptOptions{
			LaneWidening:  req.LaneWidening,
			HumanVerified: req.HumanVerified,
		})
	case "answer":
		if len(req.WidenLane) > 0 {
			return nil, s.RunAnswerWithLaneAs(ctx, runID, req.QuestionID, req.Answer, req.WidenLane, actor)
		}
		return nil, s.RunAnswerAs(ctx, runID, req.QuestionID, req.Answer, actor)
	}
	return nil, fmt.Errorf("unknown approval action %q", req.Action)
}

// ApprovalRequestInput is what an agent files: the decision it wants the
// person to make, and why.
type ApprovalRequestInput struct {
	// Action is "accept" or "answer" — the doors whose human-only parts an
	// agent cannot take itself.
	Action string `json:"action"`
	// Reason is why — what the person said, where. Required.
	Reason string `json:"reason"`
	// Accept: the message, the lane paths (default: every path the pending
	// decision offers), the manual-verification slices to confirm.
	Message       string   `json:"message,omitempty"`
	LaneWidening  []string `json:"lane_widening,omitempty"`
	HumanVerified []int    `json:"human_verified,omitempty"`
	// Answer: the question (default: the pending one), the answer, and the
	// lane paths it widens.
	QuestionID string   `json:"question_id,omitempty"`
	Answer     string   `json:"answer,omitempty"`
	WidenLane  []string `json:"widen_lane,omitempty"`
	// Actor names the requesting agent ("mcp:<client>"). The engine derives
	// the recorded requester from the credential; this only says which agent.
	Actor string `json:"actor,omitempty"`
}

// ApprovalDecision is what approving a request did.
type ApprovalDecision struct {
	Request runlog.ApprovalRequest `json:"request"`
	// Accept is the accept's result when the request was an accept that
	// landed.
	Accept *AcceptResult `json:"accept,omitempty"`
}

func (s *Service) approvalRun(id string) (*runState, error) {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found", id)
	}
	return rs, nil
}

// ApprovalRequestFile records an agent's request for the person's decision
// on a run's pending decision and rings the doorbell (approval_requested), so
// the desktop lists it and a webhook consumer learns of it.
func (s *Service) ApprovalRequestFile(ctx context.Context, runID string, in ApprovalRequestInput, actor string) (*runlog.ApprovalRequest, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" || actor == "human" {
		// The person decides directly; a request to oneself is a detour that
		// would record the person as having asked an agent's question.
		return nil, fmt.Errorf("approval requests are filed by an agent for the person; the person decides the run directly")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, fmt.Errorf("an approval request needs a reason: what the person agreed to, and where — it is shown to them beside the buttons")
	}
	rs, err := s.approvalRun(runID)
	if err != nil {
		return nil, err
	}
	mu := approvalLock(runID)
	mu.Lock()
	defer mu.Unlock()
	current := rs.snapshotRun()
	if current.Status != "paused" || current.PendingKind == "" {
		return nil, fmt.Errorf("run %q has no pending decision to request (status %s)", runID, current.Status)
	}
	req := runlog.ApprovalRequest{
		Action:       strings.TrimSpace(in.Action),
		Reason:       strings.TrimSpace(in.Reason),
		PendingKind:  current.PendingKind,
		PendingSince: current.PendingSince,
		RequestedBy:  actor,
		RequestedAt:  time.Now().UTC().Format(time.RFC3339),
		Status:       ApprovalPending,
	}
	next := runNext(current)
	switch req.Action {
	case "accept":
		if !slices.Contains(next, "accept") {
			return nil, fmt.Errorf("run %q does not offer accept (next: %v)", runID, next)
		}
		offered := stringSliceValue(current.PendingData["lane_widening"])
		lane := in.LaneWidening
		if len(lane) == 0 {
			lane = offered
		}
		allowed := map[string]bool{}
		for _, p := range offered {
			allowed[cleanLanePath(p)] = true
		}
		for _, p := range lane {
			if !allowed[cleanLanePath(p)] {
				return nil, fmt.Errorf("lane path %q was not offered by the pending decision (offered: %v)", p, offered)
			}
		}
		req.Message = strings.TrimSpace(in.Message)
		req.LaneWidening = slices.Clone(lane)
		req.HumanVerified = slices.Clone(in.HumanVerified)
	case "answer":
		if !slices.Contains(next, "answer") {
			return nil, fmt.Errorf("run %q is not waiting on a question (next: %v)", runID, next)
		}
		if strings.TrimSpace(in.Answer) == "" {
			return nil, fmt.Errorf("an answer request needs the answer the person gave")
		}
		qid := strings.TrimSpace(in.QuestionID)
		if qid == "" {
			qid, _ = current.PendingData["question_id"].(string)
		}
		req.QuestionID = qid
		req.Answer = in.Answer
		req.WidenLane = slices.Clone(in.WidenLane)
	default:
		return nil, fmt.Errorf("unknown approval action %q: request accept or answer", in.Action)
	}
	w, err := s.ensureWriter(rs)
	if err != nil {
		return nil, err
	}
	rs.wmu.Lock()
	req.ID = fmt.Sprintf("ar-%d", len(rs.run.ApprovalRequests)+1)
	rs.run.ApprovalRequests = append(rs.run.ApprovalRequests, req)
	err = w.WriteState()
	rs.wmu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("record approval request: %w", err)
	}
	_ = w.AppendEvent("approval_requested", map[string]interface{}{
		"request": req, "requested_by": req.RequestedBy,
	})
	return &req, nil
}

// setApproval replaces one request on the record and persists it.
func (s *Service) setApproval(rs *runState, req runlog.ApprovalRequest) error {
	w, err := s.ensureWriter(rs)
	if err != nil {
		return err
	}
	rs.wmu.Lock()
	defer rs.wmu.Unlock()
	for i := range rs.run.ApprovalRequests {
		if rs.run.ApprovalRequests[i].ID == req.ID {
			rs.run.ApprovalRequests[i] = req
		}
	}
	return w.WriteState()
}

func (s *Service) pendingApproval(rs *runState, reqID string) (runlog.ApprovalRequest, error) {
	for _, r := range rs.snapshotRun().ApprovalRequests {
		if r.ID == reqID {
			if r.Status != ApprovalPending {
				return r, fmt.Errorf("approval request %s is already %s", reqID, r.Status)
			}
			return r, nil
		}
	}
	return runlog.ApprovalRequest{}, fmt.Errorf("run %q has no approval request %q", rs.run.ID, reqID)
}

// ApprovalRequestApprove is the person confirming an agent's request. The
// decision runs as "human" — the person made it — and the record carries
// both actors: approval_granted names who asked and who approved before the
// decision runs, approval_resolved says what it did.
//
// Two phases (B-530 review). The claim — still pending, still bound to the
// run's pending decision, now approved — is atomic under the run's approval
// lock, so two clicks or two windows cannot both claim it. The decision then
// runs with no approval lock held: an accept that replays its gate for
// minutes blocks nothing but its own run's decision door.
func (s *Service) ApprovalRequestApprove(ctx context.Context, runID, reqID, actor string) (*ApprovalDecision, error) {
	if actor != "human" {
		return nil, fmt.Errorf("approving a request is the person's decision; %s cannot approve it", resumeActor(actor))
	}
	rs, err := s.approvalRun(runID)
	if err != nil {
		return nil, err
	}
	req, err := s.claimApproval(rs, reqID, actor)
	if err != nil {
		return nil, err
	}

	out := &ApprovalDecision{}
	out.Accept, err = decideApproval(s, ctx, runID, req, actor)
	// B-529: the accept outlived the approval's request — it is replaying
	// the gate on the run's own context. The request stays approved, says
	// so, and settles exactly once when that accept does.
	if err == nil && out.Accept != nil && out.Accept.InProgress && out.Accept.job != nil {
		out.Request = s.approvalInProgress(rs, req, out.Accept.job, out.Accept.joined)
		return out, nil
	}
	req = s.resolveApproval(rs, req, out.Accept, err)
	out.Request = req
	if req.Status != ApprovalLanded {
		return out, fmt.Errorf("approved, but the %s was not taken: %s", req.Action, req.Outcome)
	}
	return out, nil
}

// claimApproval is the atomic pending→approved transition.
func (s *Service) claimApproval(rs *runState, reqID, actor string) (runlog.ApprovalRequest, error) {
	mu := approvalLock(rs.run.ID)
	mu.Lock()
	defer mu.Unlock()
	req, err := s.pendingApproval(rs, reqID)
	if err != nil {
		return req, err
	}
	current := rs.snapshotRun()
	now := time.Now().UTC().Format(time.RFC3339)
	if current.Accepted || current.Status != "paused" || current.PendingKind != req.PendingKind || current.PendingSince != req.PendingSince {
		req.Status, req.DecidedBy, req.DecidedAt = ApprovalSuperseded, actor, now
		req.Outcome = "the decision it named is no longer pending; nothing was decided"
		_ = s.setApproval(rs, req)
		if w, werr := s.ensureWriter(rs); werr == nil {
			_ = w.AppendEvent("approval_resolved", map[string]interface{}{"request": req})
		}
		return req, fmt.Errorf("approval request %s was filed against a decision that is no longer pending; nothing was decided", reqID)
	}
	pending := req
	req.Status, req.DecidedBy, req.DecidedAt = ApprovalApproved, actor, now
	if err := s.setApproval(rs, req); err != nil {
		// Not on disk, so not claimed: put the record back the way it was
		// rather than leave an "approved" no decision will ever follow.
		_ = s.setApproval(rs, pending)
		return pending, fmt.Errorf("record approval of %s: %w; nothing was decided", reqID, err)
	}
	if w, werr := s.ensureWriter(rs); werr == nil {
		_ = w.AppendEvent("approval_granted", map[string]interface{}{
			"request": req, "requested_by": req.RequestedBy, "approved_by": actor,
		})
	}
	return req, nil
}

// afterApprovalSettled observes an approval settled after its accept job —
// a no-op in the engine; tests wait on it instead of on a clock.
var afterApprovalSettled = func(runlog.ApprovalRequest) {}

// approvalInProgress keeps an approved accept's request "approved" with the
// outcome "accept in progress", and settles it when job does: landed with
// the commit, failed with the refusal's reason, interrupted when abort or
// shutdown stopped it. Only this path resolves it, so a retry that joins
// the same accept cannot resolve the request a second time. If the engine
// stops before the settlement is written, recovery's
// settleInterruptedApprovals reads the request approved-and-unsettled and
// says interrupted (or landed, when the run's record has the commit).
func (s *Service) approvalInProgress(rs *runState, req runlog.ApprovalRequest, job *acceptJob, joined bool) runlog.ApprovalRequest {
	req.Outcome = "accept in progress"
	mu := approvalLock(rs.run.ID)
	mu.Lock()
	_ = s.setApproval(rs, req)
	mu.Unlock()
	go func() {
		<-job.done
		res := job.result
		if joined {
			res = joinedNote(res, "") // marked joined even when res is nil
		}
		afterApprovalSettled(s.resolveApproval(rs, req, res, job.err))
	}()
	return req
}

// resolveApproval records how an approved decision ended. The run's own
// record is the judge where the door's answer is ambiguous: an accept that
// errored after its commit (a publication failure, say) landed; one that
// returned without deciding (someone else accepted first) decided nothing.
func (s *Service) resolveApproval(rs *runState, req runlog.ApprovalRequest, accept *AcceptResult, decideErr error) runlog.ApprovalRequest {
	after := rs.snapshotRun()
	switch {
	case req.Action == "accept" && accept != nil && strings.Contains(accept.Info, "decided nothing"):
		req.Status, req.Outcome = ApprovalSuperseded, "the run was already accepted ("+accept.CommitSHA+"); this approval decided nothing"
	case req.Action == "accept" && accept != nil && accept.joined:
		// Another caller's accept was already running; this approval joined
		// it and decided nothing itself (B-529).
		req.Status, req.Outcome = ApprovalSuperseded, "an accept of this run was already in progress (it settled as "+approvalSettledAs(accept, decideErr)+"); this approval decided nothing"
	case req.Action == "accept" && errors.Is(decideErr, ErrDecisionInterrupted):
		req.Status, req.Outcome = ApprovalInterrupted, "interrupted: "+decideErr.Error()
	case decideErr == nil:
		req.Status, req.Outcome = ApprovalLanded, req.Action+" taken"
		if accept != nil && accept.CommitSHA != "" {
			req.Outcome = "accepted as " + accept.CommitSHA
		}
	case req.Action == "accept" && after.Accepted && after.CommitSHA != "":
		req.Status = ApprovalLanded
		req.Outcome = "accepted as " + after.CommitSHA + ", with an error after the commit: " + decideErr.Error()
	default:
		req.Status, req.Outcome = ApprovalFailed, "refused: "+decideErr.Error()
	}
	mu := approvalLock(rs.run.ID)
	mu.Lock()
	// A request settles once: if something already settled it (recovery,
	// after the engine that ran its decision died), that stands (review of
	// #181 — a reached status never regresses or changes).
	for _, cur := range rs.snapshotRun().ApprovalRequests {
		if cur.ID == req.ID && cur.Status != ApprovalApproved && cur.Status != ApprovalPending {
			mu.Unlock()
			return cur
		}
	}
	_ = s.setApproval(rs, req)
	mu.Unlock()
	if w, werr := s.ensureWriter(rs); werr == nil {
		_ = w.AppendEvent("approval_resolved", map[string]interface{}{
			"request": req, "requested_by": req.RequestedBy, "approved_by": req.DecidedBy,
		})
	}
	return req
}

// approvalSettledAs names a joined accept's outcome in a superseded request.
func approvalSettledAs(accept *AcceptResult, err error) string {
	switch {
	case errors.Is(err, ErrDecisionInterrupted):
		return "interrupted"
	case err != nil:
		return "refused: " + err.Error()
	case accept != nil && accept.CommitSHA != "":
		return "accepted as " + accept.CommitSHA
	}
	return "accepted"
}

// settleInterruptedApprovals runs as an engine recovers a run: a request
// still "approved" was in flight when the last engine stopped, and no
// decision will follow it now. It reads landed when the run's accept made it
// to a commit, interrupted otherwise — never "approved" with nothing
// happening. Reports whether it changed anything.
func (s *Service) settleInterruptedApprovals(rs *runState) bool {
	run := rs.snapshotRun()
	changed := false
	for _, req := range run.ApprovalRequests {
		if req.Status != ApprovalApproved {
			continue
		}
		if req.Action == "accept" && run.Accepted && run.CommitSHA != "" {
			req.Status, req.Outcome = ApprovalLanded, "accepted as "+run.CommitSHA+" (settled after an engine restart)"
		} else {
			req.Status, req.Outcome = ApprovalInterrupted, "the engine stopped while this decision was in flight; nothing landed — the agent files a new request if it is still wanted"
		}
		if s.setApproval(rs, req) == nil {
			changed = true
			// The same observable the live path writes (review of #181).
			if w, werr := s.ensureWriter(rs); werr == nil {
				_ = w.AppendEvent("approval_resolved", map[string]interface{}{
					"request": req, "requested_by": req.RequestedBy, "approved_by": req.DecidedBy,
				})
			}
		}
	}
	return changed
}

// ApprovalRequestDecline is the person saying no. Nothing is decided; the
// run keeps waiting, and the agent reads the decline (and its reason) on the
// run's record.
func (s *Service) ApprovalRequestDecline(ctx context.Context, runID, reqID, reason, actor string) (*runlog.ApprovalRequest, error) {
	if actor != "human" {
		return nil, fmt.Errorf("declining a request is the person's decision; %s cannot decline it", resumeActor(actor))
	}
	rs, err := s.approvalRun(runID)
	if err != nil {
		return nil, err
	}
	mu := approvalLock(runID)
	mu.Lock()
	defer mu.Unlock()
	req, err := s.pendingApproval(rs, reqID)
	if err != nil {
		return nil, err
	}
	req.Status, req.DecidedBy = ApprovalDeclined, actor
	req.DecidedAt = time.Now().UTC().Format(time.RFC3339)
	req.DeclineReason = strings.TrimSpace(reason)
	if err := s.setApproval(rs, req); err != nil {
		return nil, err
	}
	if w, werr := s.ensureWriter(rs); werr == nil {
		_ = w.AppendEvent("approval_declined", map[string]interface{}{
			"request": req, "requested_by": req.RequestedBy, "declined_by": actor,
		})
	}
	return &req, nil
}
