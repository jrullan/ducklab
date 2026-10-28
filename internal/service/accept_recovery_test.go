package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

// Adversarial review of #181 (R1): an approval that joins another caller's
// accept decided nothing. When that accept is REFUSED it has no result, and
// the joined mark was dropped with it: the approval read "failed: refused…"
// as if the person's approval (and its unused lane and verification options)
// had been the decision.
func TestAJoinedApprovalOfARefusedAcceptIsSuperseded(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-joined-refused")
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
	req, err := s.ApprovalRequestFile(context.Background(), run.ID, ApprovalRequestInput{Action: "accept", Reason: "ok"}, "mcp:test")
	if err != nil {
		t.Fatal(err)
	}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := s.ApprovalRequestApprove(gone, run.ID, req.ID, "human")
	if err != nil || out.Request.Outcome != "accept in progress" {
		t.Fatalf("approve = %+v, %v", out, err)
	}
	gate.failGate(t)
	final := awaitWithin(t, settled, "the approval settling")
	if final.Status != ApprovalSuperseded || !strings.Contains(final.Outcome, "decided nothing") || !strings.Contains(final.Outcome, "refused") {
		t.Fatalf("joined approval of a refused accept = %s / %q; want superseded — it decided nothing — naming the refusal", final.Status, final.Outcome)
	}
}

// The synchronous joined path carries the mark too: a joined call that
// waits for a refused accept is told it joined, not handed the refusal as
// its own decision with nothing to say it decided nothing.
func TestAJoinedCallOfARefusedAcceptIsMarkedJoined(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-joined-sync")
	leave, leaveNow := context.WithCancel(context.Background())
	first := acceptAsync(s, leave, run.ID, "")
	gate.awaitStart(t)
	leaveNow() // the caller leaves once its accept holds the decision
	if got := awaitWithin(t, first, "the first accept's answer"); got.err != nil || !got.res.InProgress {
		t.Fatalf("first accept = %+v, %v", got.res, got.err)
	}
	waiter := acceptAsync(s, context.Background(), run.ID, "")
	gate.failGate(t)
	got := awaitWithin(t, waiter, "the joined call's outcome")
	if got.err == nil || got.res == nil || !got.res.joined || !strings.Contains(got.res.Info, "joined an accept already in progress") {
		t.Fatalf("joined call of a refused accept = %+v, %v; want the refusal, marked joined", got.res, got.err)
	}
}

// Adversarial review of #181 (R2): an accept in flight when the engine dies
// (kill -9, a crash, or a shutdown whose bounded wait gave up) wrote no
// accept_settled. The next engine's run_get then reported an OLDER
// attempt's settlement as last_accept — the field MCP agents are told to
// trust. Recovery closes the dead attempt, with its own id.
func TestLastAcceptAfterAnEngineDiedMidAcceptIsThatAttemptInterrupted(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-died-mid-accept")
	first := acceptAsync(s, context.Background(), run.ID, "attempt one")
	gate.awaitStart(t)
	gate.failGate(t)
	if got := awaitWithin(t, first, "attempt one"); got.err == nil {
		t.Fatal("fixture: attempt one should be refused")
	}
	leave, leaveNow := context.WithCancel(context.Background())
	second := acceptAsync(s, leave, run.ID, "attempt two")
	gate.awaitStart(t)
	leaveNow() // the caller leaves once its accept holds the decision
	got := awaitWithin(t, second, "attempt two")
	if got.err != nil || !got.res.InProgress || got.res.Attempt == "" {
		t.Fatalf("attempt two = %+v, %v; want in progress with its attempt id", got.res, got.err)
	}

	// The engine dies here. The next one has the log and no memory: it is
	// a fresh runState recovered from disk.
	s.runsMu.Lock()
	dead := s.runs[run.ID]
	delete(s.runs, run.ID)
	s.runsMu.Unlock()
	t.Cleanup(func() {
		if job := dead.cancelAccept(errCauseShutdown); job != nil {
			<-job.done
		}
	})
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Run.AcceptInProgress || d.Run.AcceptAttempt != "" {
		t.Fatalf("recovered run: in_progress=%v accept_attempt=%q; want the dead attempt closed", d.Run.AcceptInProgress, d.Run.AcceptAttempt)
	}
	if d.Run.LastAccept["attempt"] != got.res.Attempt || d.Run.LastAccept["outcome"] != "interrupted" {
		t.Fatalf("last_accept after the engine died mid-accept = %v; want attempt %s interrupted, never attempt one's refusal", d.Run.LastAccept, got.res.Attempt)
	}
	// The same observables the live interruption writes.
	var settles, outcomes int
	for _, e := range d.Events {
		if e.Type == "accept_settled" && e.Data["attempt"] == got.res.Attempt {
			settles++
		}
		if e.Type == "decision_outcome" && e.Data["action"] == "accept" && e.Data["outcome"] == "interrupted" {
			outcomes++
		}
	}
	if settles != 1 || outcomes != 1 {
		t.Fatalf("recovery wrote %d accept_settled and %d interrupted decision_outcome for the dead attempt; want one each", settles, outcomes)
	}
}

// An accept that landed before the engine died recovers as landed, not
// interrupted: the record has the commit.
func TestAnAcceptThatLandedBeforeTheEngineDiedRecoversAccepted(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-died-after-landing")
	accept := acceptAsync(s, context.Background(), run.ID, "")
	gate.awaitStart(t)
	gate.open(t)
	got := awaitWithin(t, accept, "the accept")
	if got.err != nil {
		t.Fatal(got.err)
	}
	// Model a death between the commit and the settlement's write: the
	// attempt is still on the record.
	s.runsMu.Lock()
	rs := s.runs[run.ID]
	delete(s.runs, run.ID)
	s.runsMu.Unlock()
	w, err := s.ensureWriter(rs)
	if err != nil {
		t.Fatal(err)
	}
	rs.wmu.Lock()
	rs.run.AcceptAttempt = "acc-died"
	_ = w.WriteState()
	rs.wmu.Unlock()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, _ := s.RunGet(context.Background(), run.ID)
	if d.Run.LastAccept["attempt"] != "acc-died" || d.Run.LastAccept["outcome"] != "accepted" || d.Run.LastAccept["commit_sha"] != got.res.CommitSHA {
		t.Fatalf("last_accept after dying post-commit = %v; want accepted %s", d.Run.LastAccept, got.res.CommitSHA)
	}
	// This accept recorded no decision (no words), so recovery closes none:
	// the live path writes decision_outcome only over a recorded decision.
	for _, e := range d.Events {
		if e.Type == "decision_outcome" {
			t.Fatalf("recovery wrote decision_outcome %v for an accept that recorded no decision", e.Data)
		}
	}
}

// Every accept path writes exactly one accept_settled per attempt, with its
// attempt id — including the unattended ones, which used to write one only
// when interrupted.
func TestEveryAcceptAttemptSettlesExactlyOnce(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-settles-once")
	leave, leaveNow := context.WithCancel(context.Background())
	first := acceptAsync(s, leave, run.ID, "")
	gate.awaitStart(t)
	leaveNow() // the caller leaves once its accept holds the decision
	_ = awaitWithin(t, first, "the first accept's answer")
	joined := acceptAsync(s, context.Background(), run.ID, "")
	gate.open(t)
	if got := awaitWithin(t, joined, "the joined accept"); got.err != nil {
		t.Fatal(got.err)
	}
	started, settled := map[interface{}]int{}, map[interface{}]int{}
	for _, e := range runEvents(t, s, run.ID) {
		switch e.Type {
		case "accept_started":
			started[e.Data["attempt"]]++
			// B-543: a client that did not start the accept reads who
			// decides from here (acceptAsync accepts as an operator).
			if e.Data["actor"] != "mcp:test" {
				t.Errorf("accept_started names actor %v, want the operator that accepted", e.Data["actor"])
			}
		case "accept_settled":
			settled[e.Data["attempt"]]++
		}
	}
	if len(started) != 1 || len(settled) != 1 {
		t.Fatalf("attempts started %v, settled %v; want one attempt, settled once", started, settled)
	}
	for id, n := range started {
		if id == nil || id == "" || n != 1 || settled[id] != 1 {
			t.Fatalf("attempt %v: started %d, settled %d", id, n, settled[id])
		}
	}
}

// The unattended accept (yolo's auto-accept, auto-triage) is an accept like
// any other on the record: one attempt, one accept_settled, last_accept set.
func TestAnUnattendedAcceptSettlesOnTheRecord(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-unattended")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	entry, err := s.entryFor(rs)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.unattendedAccept(context.Background(), rs, entry, "") }()
	gate.awaitStart(t)
	gate.open(t)
	if err := awaitWithin(t, done, "the unattended accept"); err != nil {
		t.Fatal(err)
	}
	d, _ := s.RunGet(context.Background(), run.ID)
	if d.Run.LastAccept["outcome"] != "accepted" || d.Run.LastAccept["attempt"] == nil || d.Run.AcceptAttempt != "" {
		t.Fatalf("after an unattended accept: last_accept=%v accept_attempt=%q", d.Run.LastAccept, d.Run.AcceptAttempt)
	}
	n := 0
	for _, e := range d.Events {
		if e.Type == "accept_settled" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("accept_settled written %d times, want once", n)
	}
}

// A request still approved when the engine died is settled on recovery with
// the same approval_resolved event the live path writes.
func TestRecoveredApprovalWritesApprovalResolved(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-approval-recovered")
	req, _ := approvedInProgress(t, s, run.ID, gate)
	s.runsMu.Lock()
	dead := s.runs[run.ID]
	delete(s.runs, run.ID)
	s.runsMu.Unlock()
	// The dead engine's continuation never runs: it would settle the
	// request in-process, which a killed engine cannot.
	deadSettled := make(chan runlog.ApprovalRequest, 1)
	afterApprovalSettled = func(r runlog.ApprovalRequest) { deadSettled <- r }
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := resolvedEvents(t, s, run.ID, req.ID); n != 1 {
		t.Fatalf("approval_resolved for the recovered request written %d times, want once", n)
	}
	if got := approvalOnRecord(t, s, run.ID, req.ID); got.Status != ApprovalInterrupted {
		t.Fatalf("recovered request = %s", got.Status)
	}
	// Recovery has settled it; the dead engine's job is cancelled without
	// letting its continuation write over the recovered record.
	gate.failGate(t)
	_ = awaitWithin(t, deadSettled, "the dead engine's continuation ending")
	_ = dead
}

// A request settles once: resolving one that is already settled (recovery
// got there first) changes nothing and writes nothing.
func TestASettledApprovalIsNeverResolvedAgain(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-approval-once")
	req, settled := approvedInProgress(t, s, run.ID, gate)
	gate.open(t)
	landed := awaitWithin(t, settled, "the approval settling")
	if landed.Status != ApprovalLanded {
		t.Fatalf("fixture: approval = %s", landed.Status)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	again := s.resolveApproval(rs, req, nil, ErrDecisionInterrupted)
	if again.Status != ApprovalLanded {
		t.Fatalf("re-resolving a landed request returned %s", again.Status)
	}
	if got := approvalOnRecord(t, s, run.ID, req.ID); got.Status != ApprovalLanded {
		t.Fatalf("recorded request after a second resolve = %s, want landed", got.Status)
	}
	if n := resolvedEvents(t, s, run.ID, req.ID); n != 1 {
		t.Fatalf("approval_resolved written %d times, want once", n)
	}
}

// The engine can die between writing an attempt's accept_settled and
// clearing the attempt from the record. Recovery then finds it closed and
// writes no second settlement; and it writes decision_outcome only for an
// attempt that recorded a decision, as the live path does.
func TestRecoveryNeverSettlesAnAttemptTwice(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-settled-twice")
	accept := acceptAsync(s, context.Background(), run.ID, "")
	gate.awaitStart(t)
	gate.failGate(t)
	got := awaitWithin(t, accept, "the accept")
	if got.err == nil {
		t.Fatal("fixture: the accept should be refused")
	}
	settledAttempt := ""
	for _, e := range runEvents(t, s, run.ID) {
		if e.Type == "accept_settled" {
			settledAttempt, _ = e.Data["attempt"].(string)
		}
	}
	s.runsMu.Lock()
	rs := s.runs[run.ID]
	delete(s.runs, run.ID)
	s.runsMu.Unlock()
	w, err := s.ensureWriter(rs)
	if err != nil {
		t.Fatal(err)
	}
	rs.wmu.Lock()
	rs.run.AcceptAttempt = settledAttempt
	_ = w.WriteState()
	rs.wmu.Unlock()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, _ := s.RunGet(context.Background(), run.ID)
	n, outcomes := 0, 0
	for _, e := range d.Events {
		if e.Type == "accept_settled" && e.Data["attempt"] == settledAttempt {
			n++
		}
		if e.Type == "decision_outcome" && e.Data["action"] == "accept" {
			outcomes++
		}
	}
	if n != 1 || d.Run.AcceptAttempt != "" || d.Run.LastAccept["outcome"] != "refused" {
		t.Fatalf("after recovering a settled attempt: %d settlements, accept_attempt=%q, last_accept=%v; want one, cleared, refused", n, d.Run.AcceptAttempt, d.Run.LastAccept)
	}
	if outcomes != 0 {
		t.Fatalf("recovery wrote %d decision_outcome for an accept that recorded no decision", outcomes)
	}
}
