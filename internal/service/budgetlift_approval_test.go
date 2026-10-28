package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/runlog"
)

// budgetPausedRun registers a build run paused on its token cap.
func budgetPausedRun(t *testing.T, s *Service) string {
	t.Helper()
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-cap", ProjectID: p.ID, TaskID: "T-001", Stage: "build",
		Status: "paused", PendingKind: "budget", PendingSince: "2026-10-28T22:00:00Z",
		StartedAt: "2026-10-28T21:00:00Z",
	}
	run.Budget.Limit.Tokens = 20
	run.Budget.Limit.WallclockS = 3600
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
	rs.setTracker(budget.NewTracker(&budget.Budget{MaxTokens: 20, MaxWallclockS: 3600}))
	s.runsMu.Lock()
	s.runs[run.ID] = rs
	s.runsMu.Unlock()
	return run.ID
}

// B-541. budget_lift over MCP removed a cap on the operator's own say-so. A
// cap is the person's limit on what an unattended agent may spend — the one
// rail bounding a coordinator that is wrong with conviction — so the agent
// that hit it is refused, and the refusal names the door: an approval
// request the person confirms.
func TestAnAgentCannotLiftItsOwnBudget(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := budgetPausedRun(t, s)

	_, err := s.RunBudgetLiftAs(context.Background(), id, "tokens", "mcp:opencode")
	if err == nil {
		t.Fatal("an operator lifted a budget cap itself")
	}
	for _, want := range []string{"person's decision", "mcp:opencode", "approval_request", "budget_kind tokens"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	if got := s.runs[id].snapshotRun().Budget.Limit.Tokens; got != 20 {
		t.Fatalf("the cap fell anyway: tokens limit %d", got)
	}
	events, _ := runlog.ReadEvents(s.runs[id].runDir)
	for _, e := range events {
		if e.Type == "budget_lifted" {
			t.Fatalf("a refused lift was recorded: %+v", e.Data)
		}
	}
}

// The door: the operator files what the person agreed to, the person
// approves, and the lift runs as the person's with both actors on record.
func TestABudgetLiftRequestLiftsAsThePerson(t *testing.T) {
	ctx := context.Background()
	s := serviceWithDucklings(t, "pato-uno")
	id := budgetPausedRun(t, s)

	if _, err := s.ApprovalRequestFile(ctx, id, ApprovalRequestInput{Action: "budget_lift", Reason: "ok in chat", BudgetKind: "widgets"}, "mcp:opencode"); err == nil {
		t.Fatal("a request for an unknown cap was filed")
	}
	req, err := s.ApprovalRequestFile(ctx, id, ApprovalRequestInput{Action: "budget_lift", Reason: "the person said lift tokens", BudgetKind: "tokens"}, "mcp:opencode")
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != ApprovalPending || req.BudgetKind != "tokens" {
		t.Fatalf("filed request = %+v", req)
	}
	if _, err := s.ApprovalRequestApprove(ctx, id, req.ID, "mcp:opencode"); err == nil {
		t.Fatal("the operator approved its own request")
	}
	out, err := s.ApprovalRequestApprove(ctx, id, req.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	if out.Request.Status != ApprovalLanded {
		t.Fatalf("approved lift = %+v", out.Request)
	}
	run := s.runs[id].snapshotRun()
	if run.Budget.Limit.Tokens != 0 {
		t.Fatalf("the token cap still stands: %d", run.Budget.Limit.Tokens)
	}
	if next := runNext(run); len(next) == 0 || next[0] != "resume" {
		t.Errorf("next after the approved lift = %v, want resume", next)
	}
	events, err := runlog.ReadEvents(s.runs[id].runDir)
	if err != nil {
		t.Fatal(err)
	}
	var by interface{}
	for _, e := range events {
		if e.Type == "budget_lifted" {
			by = e.Data["by"]
		}
	}
	if by != "human" {
		t.Errorf("the approved lift was recorded by %v, want the person", by)
	}
}

// A cap binds a running run as much as a paused one, so a lift request is
// bound to the run, not to a pending decision — and it is superseded only
// when the run can no longer spend.
func TestABudgetLiftRequestStandsUntilTheRunEnds(t *testing.T) {
	ctx := context.Background()
	s := serviceWithDucklings(t, "pato-uno")
	id := budgetPausedRun(t, s)
	rs := s.runs[id]
	rs.wmu.Lock()
	rs.run.Status, rs.run.PendingKind, rs.run.PendingSince = "running", "", ""
	rs.wmu.Unlock()

	req, err := s.ApprovalRequestFile(ctx, id, ApprovalRequestInput{Action: "budget_lift", Reason: "long task, ok to spend", BudgetKind: "wallclock"}, "mcp:opencode")
	if err != nil {
		t.Fatalf("a running run's lift request was refused: %v", err)
	}
	// The run moves on to a question: still live, still lifting.
	rs.wmu.Lock()
	rs.run.Status, rs.run.PendingKind, rs.run.PendingSince = "paused", "question", "2026-10-28T22:30:00Z"
	rs.wmu.Unlock()
	second, err := s.ApprovalRequestFile(ctx, id, ApprovalRequestInput{Action: "budget_lift", Reason: "and tokens", BudgetKind: "tokens"}, "mcp:opencode")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := s.ApprovalRequestApprove(ctx, id, req.ID, "human"); err != nil || out.Request.Status != ApprovalLanded {
		t.Fatalf("a request filed while running did not land after a pause: %v %+v", err, out)
	}

	rs.wmu.Lock()
	rs.run.Status = "failed"
	rs.wmu.Unlock()
	_, err = s.ApprovalRequestApprove(ctx, id, second.ID, "human")
	if err == nil {
		t.Fatal("a lift was approved on an ended run")
	}
	for _, r := range rs.snapshotRun().ApprovalRequests {
		if r.ID == second.ID && (r.Status != ApprovalSuperseded || !strings.Contains(r.Outcome, "spends nothing more")) {
			t.Errorf("superseded lift reads %+v", r)
		}
	}
}

// Review of B-541: a request for a cap that is already gone decided nothing
// and still read "landed", writing a second budget_lifted with was 0; and
// nothing stopped the same request being filed twice.
func TestALiftRequestNeedsACapToLift(t *testing.T) {
	ctx := context.Background()
	s := serviceWithDucklings(t, "pato-uno")
	id := budgetPausedRun(t, s)

	if _, err := s.ApprovalRequestFile(ctx, id, ApprovalRequestInput{Action: "budget_lift", Reason: "x", BudgetKind: "usd"}, "mcp:opencode"); err == nil {
		t.Fatal("a request to lift a dollar cap the run never had was filed")
	}
	first, err := s.ApprovalRequestFile(ctx, id, ApprovalRequestInput{Action: "budget_lift", Reason: "x", BudgetKind: "tokens"}, "mcp:opencode")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApprovalRequestFile(ctx, id, ApprovalRequestInput{Action: "budget_lift", Reason: "again", BudgetKind: "tokens"}, "mcp:opencode"); err == nil {
		t.Fatal("a second pending request for the same cap was filed")
	}
	// The person lifts it directly; the request then decides nothing.
	if _, err := s.RunBudgetLift(ctx, id, "tokens"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApprovalRequestApprove(ctx, id, first.ID, "human"); err == nil {
		t.Fatal("approving a request for a cap already lifted reported success")
	}
	for _, r := range s.runs[id].snapshotRun().ApprovalRequests {
		if r.ID == first.ID && (r.Status != ApprovalSuperseded || !strings.Contains(r.Outcome, "already lifted")) {
			t.Errorf("stale request reads %+v", r)
		}
	}
}

// Review of B-541: a lift writes the run's state before its request is
// resolved. An engine that stopped between the two left the request
// "approved", and recovery called it interrupted — "nothing landed" — with
// the cap already gone.
func TestAnInterruptedLiftThatLandedSettlesLanded(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := budgetPausedRun(t, s)
	rs := s.runs[id]
	rs.wmu.Lock()
	rs.run.Budget.Limit.Tokens = 0
	rs.run.ApprovalRequests = []runlog.ApprovalRequest{{ID: "ar-1", Action: "budget_lift", BudgetKind: "tokens", Status: ApprovalApproved, DecidedBy: "human"}}
	rs.wmu.Unlock()
	if !s.settleInterruptedApprovals(rs) {
		t.Fatal("recovery changed nothing")
	}
	got := rs.snapshotRun().ApprovalRequests[0]
	if got.Status != ApprovalLanded {
		t.Fatalf("a lift that wrote its state settled as %+v", got)
	}
}
