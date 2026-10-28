package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

// failGate releases the held replay red: the rebased commit fails its gate.
func (g *heldGate) failGate(t *testing.T) {
	t.Helper()
	fifoStep(t, "the held gate being failed", func() error {
		return os.WriteFile(g.release, []byte("fail\n"), 0o600)
	})
}

// approvedInProgress files an agent's accept request on a held-gate run and
// has the person approve it from a request that is already gone — the
// approval's accept outlives it, so the request must stay approved with the
// outcome "accept in progress" (B-529 × B-530).
func approvedInProgress(t *testing.T, s *Service, runID string, gate *heldGate) (runlog.ApprovalRequest, <-chan runlog.ApprovalRequest) {
	t.Helper()
	settled := make(chan runlog.ApprovalRequest, 4)
	afterApprovalSettled = func(r runlog.ApprovalRequest) { settled <- r }
	t.Cleanup(func() { afterApprovalSettled = func(runlog.ApprovalRequest) {} })

	req, err := s.ApprovalRequestFile(context.Background(), runID, ApprovalRequestInput{
		Action: "accept", Reason: "the person said ship it, in chat",
	}, "mcp:test")
	if err != nil {
		t.Fatal(err)
	}
	leave, leaveNow := context.WithCancel(context.Background())
	type approved struct {
		out *ApprovalDecision
		err error
	}
	answered := make(chan approved, 1)
	go func() {
		out, err := s.ApprovalRequestApprove(leave, runID, req.ID, "human")
		answered <- approved{out, err}
	}()
	gate.awaitStart(t)
	leaveNow() // the person's request ends once the accept holds the decision
	got := awaitWithin(t, answered, "the approval's answer")
	if got.err != nil || got.out == nil || got.out.Accept == nil || !got.out.Accept.InProgress {
		t.Fatalf("approve while the accept replays = %+v, %v; want an in-progress accept", got.out, got.err)
	}
	if got.out.Request.Status != ApprovalApproved || got.out.Request.Outcome != "accept in progress" {
		t.Fatalf("request while in progress = %s / %q; want approved / accept in progress", got.out.Request.Status, got.out.Request.Outcome)
	}
	if onRecord := approvalOnRecord(t, s, runID, req.ID); onRecord.Status != ApprovalApproved || onRecord.Outcome != "accept in progress" {
		t.Fatalf("recorded request while in progress = %s / %q", onRecord.Status, onRecord.Outcome)
	}
	return *req, settled
}

func approvalOnRecord(t *testing.T, s *Service, runID, reqID string) runlog.ApprovalRequest {
	t.Helper()
	detail, err := s.RunGet(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range detail.Run.ApprovalRequests {
		if r.ID == reqID {
			return r
		}
	}
	t.Fatalf("run %s has no approval request %s", runID, reqID)
	return runlog.ApprovalRequest{}
}

func resolvedEvents(t *testing.T, s *Service, runID, reqID string) int {
	t.Helper()
	n := 0
	for _, e := range runEvents(t, s, runID) {
		if e.Type != "approval_resolved" {
			continue
		}
		if req, ok := e.Data["request"].(map[string]interface{}); ok && req["id"] == reqID {
			n++
		}
	}
	return n
}

func TestAnApprovedAcceptInProgressSettlesLanded(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-approval-landed")
	req, settled := approvedInProgress(t, s, run.ID, gate)
	gate.open(t)
	final := awaitWithin(t, settled, "the approval settling")
	detail, _ := s.RunGet(context.Background(), run.ID)
	if final.Status != ApprovalLanded || !strings.Contains(final.Outcome, detail.Run.CommitSHA) || detail.Run.CommitSHA == "" {
		t.Fatalf("approval after the accept landed = %s / %q (commit %s); want landed with the commit", final.Status, final.Outcome, detail.Run.CommitSHA)
	}
	if onRecord := approvalOnRecord(t, s, run.ID, req.ID); onRecord.Status != ApprovalLanded {
		t.Fatalf("recorded request = %s", onRecord.Status)
	}
}

func TestAnApprovedAcceptInProgressSettlesFailedWithTheRefusal(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-approval-failed")
	req, settled := approvedInProgress(t, s, run.ID, gate)
	gate.failGate(t)
	final := awaitWithin(t, settled, "the approval settling")
	refused := ""
	for _, e := range runEvents(t, s, run.ID) {
		if e.Type == "accept_refused" {
			refused, _ = e.Data["reason"].(string)
		}
	}
	if refused == "" || final.Status != ApprovalFailed || !strings.Contains(final.Outcome, refused) {
		t.Fatalf("approval after the accept was refused = %s / %q; want failed with the accept_refused reason %q", final.Status, final.Outcome, refused)
	}
	if onRecord := approvalOnRecord(t, s, run.ID, req.ID); onRecord.Status != ApprovalFailed {
		t.Fatalf("recorded request = %s", onRecord.Status)
	}
}

// Shutdown interrupts the approval's accept; the request says interrupted,
// and the next engine's recovery finds nothing left to settle.
func TestAnApprovedAcceptInProgressSettlesInterruptedAndRecoveryAgrees(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-approval-interrupted")
	req, settled := approvedInProgress(t, s, run.ID, gate)
	if err := s.PauseAllRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	final := awaitWithin(t, settled, "the approval settling")
	if final.Status != ApprovalInterrupted || !strings.Contains(final.Outcome, "shutting down") {
		t.Fatalf("approval after shutdown = %s / %q; want interrupted naming the shutdown", final.Status, final.Outcome)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	if s.settleInterruptedApprovals(rs) {
		t.Fatal("recovery re-settled a request the interrupted accept already settled")
	}
	if onRecord := approvalOnRecord(t, s, run.ID, req.ID); onRecord.Status != ApprovalInterrupted {
		t.Fatalf("recorded request = %s", onRecord.Status)
	}
	if n := resolvedEvents(t, s, run.ID, req.ID); n != 1 {
		t.Fatalf("approval_resolved for %s written %d times, want once", req.ID, n)
	}
}

// An approval whose accept joins one another caller already started decided
// nothing itself, and is resolved exactly once — by its own settlement, not
// also by the joined accept's (review of #181 × B-530).
func TestAnApprovalJoiningAnAcceptInFlightResolvesOnce(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-approval-joined")
	settled := make(chan runlog.ApprovalRequest, 4)
	afterApprovalSettled = func(r runlog.ApprovalRequest) { settled <- r }
	t.Cleanup(func() { afterApprovalSettled = func(runlog.ApprovalRequest) {} })

	leave, leaveNow := context.WithCancel(context.Background())
	first := acceptAsync(s, leave, run.ID, "the operator's own accept")
	gate.awaitStart(t)
	leaveNow() // the caller leaves once its accept holds the decision
	if got := awaitWithin(t, first, "the first accept's answer"); got.err != nil || !got.res.InProgress {
		t.Fatalf("first accept = %+v, %v", got.res, got.err)
	}
	req, err := s.ApprovalRequestFile(context.Background(), run.ID, ApprovalRequestInput{Action: "accept", Reason: "the person agreed"}, "mcp:test")
	if err != nil {
		t.Fatal(err)
	}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := s.ApprovalRequestApprove(gone, run.ID, req.ID, "human")
	if err != nil || out.Request.Status != ApprovalApproved || out.Request.Outcome != "accept in progress" {
		t.Fatalf("approve joining the accept in flight = %+v, %v", out, err)
	}
	gate.open(t)
	final := awaitWithin(t, settled, "the approval settling")
	if final.Status != ApprovalSuperseded || !strings.Contains(final.Outcome, "decided nothing") {
		t.Fatalf("joined approval = %s / %q; want superseded — it decided nothing", final.Status, final.Outcome)
	}
	// The accept itself landed, through the operator's call.
	if detail, _ := s.RunGet(context.Background(), run.ID); !detail.Run.Accepted {
		t.Fatal("the joined accept did not land")
	}
	select {
	case again := <-settled:
		t.Fatalf("the approval was resolved a second time: %+v", again)
	default:
	}
	if n := resolvedEvents(t, s, run.ID, req.ID); n != 1 {
		t.Fatalf("approval_resolved for %s written %d times, want once", req.ID, n)
	}
}
