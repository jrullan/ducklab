package service

import (
	"context"
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

// Approval statuses.
const (
	ApprovalPending    = "pending"
	ApprovalApproved   = "approved"
	ApprovalDeclined   = "declined"
	ApprovalLanded     = "landed"
	ApprovalFailed     = "failed"
	ApprovalSuperseded = "superseded"
)

// approvalMu serialises approve and decline. A request is decided once: two
// clicks (or two windows) must not both run its decision.
var approvalMu sync.Mutex

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
	approvalMu.Lock()
	defer approvalMu.Unlock()
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
func (s *Service) ApprovalRequestApprove(ctx context.Context, runID, reqID, actor string) (*ApprovalDecision, error) {
	if actor != "human" {
		return nil, fmt.Errorf("approving a request is the person's decision; %s cannot approve it", resumeActor(actor))
	}
	rs, err := s.approvalRun(runID)
	if err != nil {
		return nil, err
	}
	approvalMu.Lock()
	defer approvalMu.Unlock()
	req, err := s.pendingApproval(rs, reqID)
	if err != nil {
		return nil, err
	}
	current := rs.snapshotRun()
	now := time.Now().UTC().Format(time.RFC3339)
	if current.Status != "paused" || current.PendingKind != req.PendingKind || current.PendingSince != req.PendingSince {
		req.Status, req.DecidedBy, req.DecidedAt = ApprovalSuperseded, actor, now
		req.Outcome = "the decision it named is no longer pending; nothing was decided"
		_ = s.setApproval(rs, req)
		if w, werr := s.ensureWriter(rs); werr == nil {
			_ = w.AppendEvent("approval_resolved", map[string]interface{}{"request": req})
		}
		return nil, fmt.Errorf("approval request %s was filed against a decision that is no longer pending; nothing was decided", reqID)
	}
	req.Status, req.DecidedBy, req.DecidedAt = ApprovalApproved, actor, now
	if err := s.setApproval(rs, req); err != nil {
		return nil, err
	}
	w, err := s.ensureWriter(rs)
	if err != nil {
		return nil, err
	}
	_ = w.AppendEvent("approval_granted", map[string]interface{}{
		"request": req, "requested_by": req.RequestedBy, "approved_by": actor,
	})

	out := &ApprovalDecision{}
	var decideErr error
	switch req.Action {
	case "accept":
		msg := fmt.Sprintf("approved %s, requested by %s: %s", req.ID, req.RequestedBy, req.Reason)
		if req.Message != "" {
			msg = req.Message + "\n\n" + msg
		}
		out.Accept, decideErr = s.RunAcceptAsWithOptions(ctx, runID, msg, actor, AcceptOptions{
			LaneWidening:  req.LaneWidening,
			HumanVerified: req.HumanVerified,
		})
	case "answer":
		if len(req.WidenLane) > 0 {
			decideErr = s.RunAnswerWithLaneAs(ctx, runID, req.QuestionID, req.Answer, req.WidenLane, actor)
		} else {
			decideErr = s.RunAnswerAs(ctx, runID, req.QuestionID, req.Answer, actor)
		}
	default:
		decideErr = fmt.Errorf("unknown approval action %q", req.Action)
	}
	if decideErr != nil {
		req.Status, req.Outcome = ApprovalFailed, "refused: "+decideErr.Error()
	} else {
		req.Status = ApprovalLanded
		req.Outcome = req.Action + " taken"
		if out.Accept != nil && out.Accept.CommitSHA != "" {
			req.Outcome = "accepted as " + out.Accept.CommitSHA
		}
	}
	_ = s.setApproval(rs, req)
	if w, werr := s.ensureWriter(rs); werr == nil {
		_ = w.AppendEvent("approval_resolved", map[string]interface{}{
			"request": req, "requested_by": req.RequestedBy, "approved_by": actor,
		})
	}
	out.Request = req
	if decideErr != nil {
		return out, fmt.Errorf("approved, but the %s was refused: %w", req.Action, decideErr)
	}
	return out, nil
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
	approvalMu.Lock()
	defer approvalMu.Unlock()
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
