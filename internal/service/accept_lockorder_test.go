package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/vcs"
)

// Second adversarial review of #181 (P1/P2): a door waiting for the
// decision lock held acceptMu, which run_get, every accept, abort, shutdown
// and an accept's own settlement need. With an unattended accept settling
// behind it, that was a permanent deadlock; with another door's settleWorker
// ahead of it, run_get froze for up to 30 s. The order is decisionMu →
// acceptMu, and nothing blocks on decisionMu holding acceptMu.
func TestADoorWaitingForTheDecisionHoldsNothing(t *testing.T) {
	s, run, _, _ := heldAcceptRun(t, "r-b529-lock-order")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	waiting := make(chan struct{}, 1)
	decisionWaiting = func(r *runState) {
		if r == rs {
			select {
			case waiting <- struct{}{}:
			default:
			}
		}
	}
	t.Cleanup(func() { decisionWaiting = func(*runState) {} })

	rs.decisionMu.Lock() // a door ahead, for as long as it likes
	rejected := make(chan error, 1)
	go func() { rejected <- s.RunRejectAs(context.Background(), run.ID, "", "human") }()
	_ = awaitWithin(t, waiting, "the reject queueing for the decision")
	// While it waits: acceptMu is free, and run_get answers at once.
	if !rs.acceptMu.TryLock() {
		rs.decisionMu.Unlock()
		t.Fatal("a door waiting for the decision holds acceptMu")
	}
	rs.acceptMu.Unlock()
	got := make(chan error, 1)
	go func() { _, err := s.RunGet(context.Background(), run.ID); got <- err }()
	if err := awaitWithin(t, got, "run_get beside a queued door"); err != nil {
		t.Fatal(err)
	}
	rs.decisionMu.Unlock()
	if err := awaitWithin(t, rejected, "the reject"); err != nil {
		t.Fatal(err)
	}
}

// run_get reads the settlement under its own leaf lock: a door queued
// behind another door's settleWorker freezes nothing (review P2).
func TestRunGetNeverWaitsBehindAQueuedDoor(t *testing.T) {
	s, run, _, _ := heldAcceptRun(t, "r-b529-liveness")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	tearingDown := make(chan struct{}) // a paused worker still tearing down
	rs.wmu.Lock()
	rs.done = tearingDown
	rs.wmu.Unlock()
	waiting := make(chan struct{}, 2)
	settleWorkerWaiting = func(r *runState) {
		if r == rs {
			select {
			case waiting <- struct{}{}:
			default:
			}
		}
	}
	t.Cleanup(func() { settleWorkerWaiting = func(*runState) {} })

	doors := make(chan error, 2)
	go func() { doors <- s.RunRejectAs(context.Background(), run.ID, "", "human") }()
	go func() { doors <- s.RunLand(context.Background(), run.ID, "deadbeef", "human", "") }()
	_ = awaitWithin(t, waiting, "the first door waiting for the worker")
	_ = awaitWithin(t, waiting, "the second door waiting for the worker")
	got := make(chan error, 1)
	go func() { _, err := s.RunGet(context.Background(), run.ID); got <- err }()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run_get waited behind a door queued for the decision")
	}
	// Let the worker finish and both doors answer before the run's files go.
	close(tearingDown)
	_ = awaitWithin(t, doors, "the first door")
	_ = awaitWithin(t, doors, "the second door")
}

// Review P1, the reproduced deadlock: aborts during an unattended accept,
// with the worker live. Every call returns, and run_get answers throughout.
func TestAbortDuringAnUnattendedAcceptDoesNotDeadlock(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-no-deadlock")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	entry, err := s.entryFor(rs)
	if err != nil {
		t.Fatal(err)
	}
	workerDone := make(chan struct{})
	// The worker is the run's live goroutine, as in yolo's auto-accept;
	// its record says so before it accepts.
	var once sync.Once
	rs.wmu.Lock()
	rs.run.Status, rs.run.Stage = "running", "build"
	rs.done = workerDone
	rs.cancel = func() { once.Do(func() {}) }
	rs.wmu.Unlock()
	go func() {
		_ = s.unattendedAccept(context.Background(), rs, entry, "")
		close(workerDone)
	}()
	gate.awaitStart(t)

	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- s.RunAbortAs(context.Background(), run.ID, "", "human") }()
	go func() { second <- s.RunAbortAs(context.Background(), run.ID, "", "human") }()
	got := make(chan error, 1)
	go func() { _, err := s.RunGet(context.Background(), run.ID); got <- err }()
	_ = awaitWithin(t, got, "run_get during the aborts")
	_ = awaitWithin(t, first, "abort #1")
	_ = awaitWithin(t, second, "abort #2")
	_ = awaitWithin(t, workerDone, "the worker")
	if d, _ := s.RunGet(context.Background(), run.ID); d.Run.Verdict != "ABORTED" {
		t.Fatalf("run after the aborts: %s / %s", d.Run.Status, d.Run.Verdict)
	}
}

// Review P3.1: an unattended accept (yolo, auto-triage) is the run's accept
// job — visible as accept_in_progress, and an accept arriving meanwhile
// joins it instead of replaying the gate again.
func TestAnAcceptDuringAnUnattendedAcceptJoinsIt(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-unattended-joined")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	entry, _ := s.entryFor(rs)
	workerDone := make(chan error, 1)
	go func() { workerDone <- s.unattendedAccept(context.Background(), rs, entry, "") }()
	gate.awaitStart(t)
	d, _ := s.RunGet(context.Background(), run.ID)
	if !d.Run.AcceptInProgress || d.Run.AcceptAttempt == "" {
		t.Fatalf("during an unattended accept: accept_in_progress=%v accept_attempt=%q; want it visible", d.Run.AcceptInProgress, d.Run.AcceptAttempt)
	}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := s.RunAcceptAs(gone, run.ID, "", "mcp:test")
	if err != nil || res == nil || !res.InProgress || !res.joined || res.Attempt != d.Run.AcceptAttempt {
		t.Fatalf("accept during the unattended accept = %+v, %v; want it to join attempt %s", res, err, d.Run.AcceptAttempt)
	}
	gate.open(t)
	if err := awaitWithin(t, workerDone, "the unattended accept"); err != nil {
		t.Fatal(err)
	}
	if n := gate.replays(t); n != 1 {
		t.Fatalf("gate replayed %d times, want once", n)
	}
}

// Review P3.2: a refusal before the accept held the decision writes nothing
// — and now leaves run_get's last_accept alone too, so it never disagrees
// with the log.
func TestAnUnheldRefusalLeavesLastAcceptAlone(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-unheld")
	first := acceptAsync(s, context.Background(), run.ID, "")
	gate.awaitStart(t)
	gate.failGate(t)
	if got := awaitWithin(t, first, "attempt one"); got.err == nil {
		t.Fatal("fixture: attempt one should be refused")
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	old := settleWorkerLimit
	settleWorkerLimit = 50 * time.Millisecond
	t.Cleanup(func() { settleWorkerLimit = old })
	rs.wmu.Lock()
	rs.done = make(chan struct{})
	rs.wmu.Unlock()
	if _, err := s.RunAcceptAs(context.Background(), run.ID, "", "mcp:test"); err == nil {
		t.Fatal("fixture: the un-held accept should be refused")
	}
	d, _ := s.RunGet(context.Background(), run.ID)
	var fromLog map[string]interface{}
	for _, e := range d.Events {
		if e.Type == "accept_settled" {
			fromLog = e.Data
		}
	}
	if d.Run.LastAccept["attempt"] == nil || d.Run.LastAccept["attempt"] != fromLog["attempt"] {
		t.Fatalf("run_get last_accept=%v, but the log's newest accept_settled=%v", d.Run.LastAccept, fromLog)
	}
}

// Review P3.3: the engine dies after the fast-forward landed the commit on
// main but before the record said Accepted. Recovery reads the commit on
// main as the accept — it is — and settles accepted, not "interrupted,
// nothing was decided".
func TestADeathAfterTheFastForwardRecoversAccepted(t *testing.T) {
	s, run, gate, dir := heldAcceptRun(t, "r-b529-ff-death")
	accept := acceptAsync(s, context.Background(), run.ID, "")
	gate.awaitStart(t)
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	// The record as it stood mid-replay: attempt open, not yet accepted.
	stateMid, _ := os.ReadFile(filepath.Join(rs.runDir, "state.json"))
	eventsMid, _ := os.ReadFile(filepath.Join(rs.runDir, "events.jsonl"))
	gate.open(t)
	got := awaitWithin(t, accept, "the accept")
	if got.err != nil {
		t.Fatal(got.err)
	}
	s.runsMu.Lock()
	delete(s.runs, run.ID)
	s.runsMu.Unlock()
	if err := os.WriteFile(filepath.Join(rs.runDir, "state.json"), stateMid, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rs.runDir, "events.jsonl"), eventsMid, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, _ := s.RunGet(context.Background(), run.ID)
	head := mustHead(t, vcs.New(dir))
	if head != got.res.CommitSHA {
		t.Fatalf("fixture: main %s, accepted %s", head, got.res.CommitSHA)
	}
	if d.Run.LastAccept["outcome"] != "accepted" || d.Run.LastAccept["commit_sha"] != head {
		t.Fatalf("last_accept after dying post-fast-forward = %v; want accepted %s", d.Run.LastAccept, head)
	}
	if !d.Run.Accepted || d.Run.Status != "done" || d.Run.CommitSHA != head || d.LandingOffer != nil {
		t.Fatalf("recovered run: accepted=%v status=%s commit=%s offer=%+v; want accepted, done, %s", d.Run.Accepted, d.Run.Status, d.Run.CommitSHA, d.LandingOffer, head)
	}
}

// Abort holds the decision while it waits for the run's worker. A worker
// that starts an accept just then (yolo finishing, the tdd chain) used to
// wait on that decision while abort waited on it: deadlock. The accept is
// refused as interrupted, the worker ends, the abort completes.
func TestAbortRefusesAnAcceptTheWorkerStartsWhileItWaits(t *testing.T) {
	s, run, _, _ := heldAcceptRun(t, "r-b529-worker-accepts-late")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	entry, err := s.entryFor(rs)
	if err != nil {
		t.Fatal(err)
	}
	workerDone := make(chan struct{})
	workerErr := make(chan error, 1)
	var once sync.Once
	rs.wmu.Lock()
	rs.run.Status, rs.run.Stage = "running", "build"
	rs.done = workerDone
	// The worker reacts to the cancel by finishing its turn — with an accept.
	rs.cancel = func() {
		once.Do(func() {
			go func() {
				workerErr <- s.unattendedAccept(context.Background(), rs, entry, "")
				close(workerDone)
			}()
		})
	}
	rs.wmu.Unlock()
	aborted := make(chan error, 1)
	go func() { aborted <- s.RunAbortAs(context.Background(), run.ID, "", "human") }()
	if err := awaitWithin(t, aborted, "the abort"); err != nil {
		t.Fatal(err)
	}
	if err := awaitWithin(t, workerErr, "the worker's accept"); !errors.Is(err, ErrDecisionInterrupted) {
		t.Fatalf("the worker's late accept = %v; want it refused as interrupted by the abort", err)
	}
}
