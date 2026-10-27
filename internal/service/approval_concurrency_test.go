package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

// holdDecisions replaces the approval decision with one that blocks until
// the returned release is called, and reports each decision that starts.
func holdDecisions(t *testing.T) (started <-chan string, release func()) {
	t.Helper()
	gate := make(chan struct{})
	starts := make(chan string, 8)
	saved := decideApproval
	decideApproval = func(s *Service, ctx context.Context, runID string, req runlog.ApprovalRequest, actor string) (*AcceptResult, error) {
		starts <- runID + "/" + req.ID
		<-gate
		return nil, nil
	}
	released := false
	release = func() {
		if !released {
			released = true
			close(gate)
		}
	}
	t.Cleanup(func() { release(); decideApproval = saved })
	return starts, release
}

func within(t *testing.T, what string, d time.Duration, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s blocked for %s behind another run's approved decision", what, d)
	}
}

// B-530 review (Codex): the approval lock was one global mutex held through
// the approved accept, so an accept replaying its gate for minutes blocked
// filing and declining on every other run. A held decision on run A must
// leave run B's requests free.
func TestAHeldApprovedDecisionBlocksNoOtherRun(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	runA, _, _, _ := laneRefusedRun(t, s, "r-held-a")
	runB, _, _, _ := laneRefusedRun(t, s, "r-free-b")
	reqA, err := s.ApprovalRequestFile(context.Background(), runA.ID, ApprovalRequestInput{Action: "accept", Reason: "ok in chat"}, "mcp:opencode")
	if err != nil {
		t.Fatal(err)
	}
	reqB, err := s.ApprovalRequestFile(context.Background(), runB.ID, ApprovalRequestInput{Action: "accept", Reason: "ok in chat"}, "mcp:opencode")
	if err != nil {
		t.Fatal(err)
	}
	started, release := holdDecisions(t)
	approved := make(chan error, 1)
	go func() {
		_, err := s.ApprovalRequestApprove(context.Background(), runA.ID, reqA.ID, "human")
		approved <- err
	}()
	if got := <-started; got != runA.ID+"/"+reqA.ID {
		t.Fatalf("decision started for %s", got)
	}
	// While A's decision is in flight, its record says so.
	if got := approvalOf(t, s, runA.ID, reqA.ID); got.Status != ApprovalApproved {
		t.Fatalf("in-flight request reads %s, want approved", got.Status)
	}

	within(t, "declining on run B", 3*time.Second, func() {
		if _, err := s.ApprovalRequestDecline(context.Background(), runB.ID, reqB.ID, "", "human"); err != nil {
			t.Errorf("decline on B: %v", err)
		}
	})
	within(t, "filing on run B", 3*time.Second, func() {
		if _, err := s.ApprovalRequestFile(context.Background(), runB.ID, ApprovalRequestInput{Action: "accept", Reason: "again"}, "mcp:opencode"); err != nil {
			t.Errorf("file on B: %v", err)
		}
	})
	// The claim already happened: a second click on A neither waits for the
	// first decision nor runs a second one.
	within(t, "a second approve of the same request", 3*time.Second, func() {
		if _, err := s.ApprovalRequestApprove(context.Background(), runA.ID, reqA.ID, "human"); err == nil || !strings.Contains(err.Error(), "already approved") {
			t.Errorf("second approve = %v, want already approved", err)
		}
	})
	release()
	if err := <-approved; err != nil {
		t.Fatalf("held approve: %v", err)
	}
	select {
	case extra := <-started:
		t.Fatalf("a second decision ran: %s", extra)
	default:
	}
	if got := approvalOf(t, s, runA.ID, reqA.ID); got.Status != ApprovalLanded {
		t.Fatalf("after the decision: %s, want landed", got.Status)
	}
}

// A decision the door refuses after the person approved is recorded failed,
// with why — never left "approved" — and cannot be approved again.
func TestARefusedApprovedDecisionIsRecordedFailed(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	run, _, _, _ := laneRefusedRun(t, s, "r-approved-refused")
	req, err := s.ApprovalRequestFile(context.Background(), run.ID, ApprovalRequestInput{Action: "accept", Reason: "ok"}, "mcp:opencode")
	if err != nil {
		t.Fatal(err)
	}
	saved := decideApproval
	t.Cleanup(func() { decideApproval = saved })
	decideApproval = func(*Service, context.Context, string, runlog.ApprovalRequest, string) (*AcceptResult, error) {
		return nil, errString("final reviewer requested changes")
	}
	if _, err := s.ApprovalRequestApprove(context.Background(), run.ID, req.ID, "human"); err == nil {
		t.Fatal("a refused decision reported success")
	}
	got := approvalOf(t, s, run.ID, req.ID)
	if got.Status != ApprovalFailed || !strings.Contains(got.Outcome, "final reviewer requested changes") {
		t.Fatalf("refused decision recorded %s / %q, want failed with the reason", got.Status, got.Outcome)
	}
	if _, err := s.ApprovalRequestApprove(context.Background(), run.ID, req.ID, "human"); err == nil {
		t.Fatal("a failed request was approved again")
	}
}

// An engine that stops mid-decision leaves "approved" on disk. The next
// engine must not show it as still happening.
func TestAnApprovalInFlightAtAStopIsSettledOnRecovery(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	run, _, _, _ := laneRefusedRun(t, s, "r-approval-interrupted")
	req, err := s.ApprovalRequestFile(context.Background(), run.ID, ApprovalRequestInput{Action: "accept", Reason: "ok"}, "mcp:opencode")
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	if _, err := s.claimApproval(rs, req.ID, "human"); err != nil {
		t.Fatal(err)
	}
	// The engine stops here. A fresh recovery reads the record from disk.
	s.runsMu.Lock()
	delete(s.runs, run.ID)
	s.runsMu.Unlock()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := approvalOf(t, s, run.ID, req.ID)
	if got.Status != ApprovalInterrupted || got.Outcome == "" {
		t.Fatalf("after recovery: %s / %q, want interrupted with an outcome", got.Status, got.Outcome)
	}
	if _, err := s.ApprovalRequestApprove(context.Background(), run.ID, req.ID, "human"); err == nil {
		t.Fatal("an interrupted request was approved again; the agent files a new one")
	}
}
