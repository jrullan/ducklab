package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Third adversarial review of #181: recovery's "landed before the crash" bookkeeping vs the live
// accept. The live path writes the acceptance receipt (read by the PR copy
// and committed as the run record); recovery does not.
func TestARecoveredLandedAcceptEndsLikeALiveOne(t *testing.T) {
	s, run, gate, dir := heldAcceptRun(t, "r-b529-ff-receipt")
	accept := acceptAsync(s, context.Background(), run.ID, "")
	gate.awaitStart(t)
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	stateMid, _ := os.ReadFile(filepath.Join(rs.runDir, "state.json"))
	eventsMid, _ := os.ReadFile(filepath.Join(rs.runDir, "events.jsonl"))
	gate.open(t)
	if got := awaitWithin(t, accept, "the accept"); got.err != nil {
		t.Fatal(got.err)
	}
	receipt := filepath.Join(dir, ".ducklab", "runs", run.ID, "receipt.json")
	if _, err := os.Stat(receipt); err != nil {
		t.Fatalf("fixture: live accept wrote no receipt: %v", err)
	}
	_ = os.Remove(receipt) // the engine died before writing it
	s.runsMu.Lock()
	delete(s.runs, run.ID)
	s.runsMu.Unlock()
	_ = os.WriteFile(filepath.Join(rs.runDir, "state.json"), stateMid, 0o644)
	_ = os.WriteFile(filepath.Join(rs.runDir, "events.jsonl"), eventsMid, 0o644)
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, _ := s.RunGet(context.Background(), run.ID)
	if !d.Run.Accepted {
		t.Fatalf("fixture: recovery did not settle accepted")
	}
	if _, err := os.Stat(receipt); err != nil {
		t.Fatalf("recovered-accepted run has no acceptance receipt (%v); resolution=%q", err, d.Run.Resolution)
	}
	// The same ending as live: who accepted, done, the worktree released —
	// and what recovery could not replay said on the run.
	if !strings.Contains(d.Run.Resolution, "accepted by mcp:test") || d.Run.Status != "done" {
		t.Fatalf("recovered resolution = %q, status %s; want it to name the actor", d.Run.Resolution, d.Run.Status)
	}
	if !strings.Contains(d.Run.Warning, "publication") || !strings.Contains(d.Run.Warning, "checkout was not synced") {
		t.Fatalf("recovered run's warning = %q; want what recovery did not do named", d.Run.Warning)
	}
	if _, err := os.Stat(rs.run.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("recovered run's worktree %s was not released: %v", rs.run.WorktreePath, err)
	}
}

// Third adversarial review of #181: a paused run whose worker is still live accepts itself (the
// tdd chain and auto-triage both publish "paused" and then accept on the
// worker). A person's accept that arrived a moment earlier is the run's job;
// it waits in settleWorker for that worker. The worker's accept JOINS it and
// waits for its done: a cycle broken only by settleWorkerLimit, after which
// both callers get RunFinishingError and the worker's own accept (which
// before #181 simply waited for decisionMu and then succeeded) is lost.
func TestAWorkerAcceptJoiningAPersonsWaitingAcceptPromotesIt(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-worker-join")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	entry, err := s.entryFor(rs)
	if err != nil {
		t.Fatal(err)
	}
	old := settleWorkerLimit
	settleWorkerLimit = 500 * time.Millisecond
	t.Cleanup(func() { settleWorkerLimit = old })

	workerDone := make(chan struct{})
	rs.wmu.Lock()
	rs.done = workerDone // paused (published), worker still live
	rs.wmu.Unlock()

	human := acceptAsync(s, context.Background(), run.ID, "")
	for rs.acceptInFlight() == nil {
		time.Sleep(time.Millisecond)
	}
	workerErr := make(chan error, 1)
	go func() {
		err := s.unattendedAccept(context.Background(), rs, entry, "auto-triage")
		workerErr <- err
		close(workerDone)
	}()
	var werr error
	select {
	case werr = <-workerErr:
	case <-time.After(5 * time.Second):
		// Not answered: it holds the decision and is replaying the gate.
		gate.awaitStart(t)
		gate.open(t)
		werr = awaitWithin(t, workerErr, "worker accept")
	}
	h := awaitWithin(t, human, "human accept")
	t.Logf("human: res=%+v err=%v", h.res, h.err)
	var finishing *RunFinishingError
	if errors.As(werr, &finishing) || werr != nil {
		t.Fatalf("the worker's own accept = %v; want it to accept (it is the worker; nothing it waits for is pending)", werr)
	}
}

// Third adversarial review of #181: the caller's bound (HTTP acceptWait, 20 s, under a 30 s
// client timeout) does not bound the answer. Until the job holds the
// decision the caller waits — behind a door holding decisionMu and then the
// job's own settleWorker (settleWorkerLimit, 30 s) — whatever ctx says.
func TestAnAcceptAnswersWithinItsCallersBound(t *testing.T) {
	s, run, _, _ := heldAcceptRun(t, "r-b529-bound")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	old := settleWorkerLimit
	settleWorkerLimit = 1500 * time.Millisecond
	t.Cleanup(func() { settleWorkerLimit = old })
	rs.wmu.Lock()
	rs.done = make(chan struct{}) // paused worker still tearing down
	rs.wmu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := s.RunAcceptAs(ctx, run.ID, "", "mcp:test")
	took := time.Since(start)
	t.Logf("answer after %v: res=%+v err=%v", took, res, err)
	if took > time.Second {
		t.Fatalf("caller bounded at 100ms answered after %v", took)
	}
}

// Third adversarial review of #181: abort's blockAccepts comes AFTER its settleWorker. A paused
// run whose worker accepts itself on the way out (auto-triage, the tdd
// chain) installs an accept that polls for the decision abort holds, while
// abort's settleWorker waits for that worker: abort stalls the full
// settleWorkerLimit, then fails "still finishing", and the accept it was
// meant to stop then takes the decision and lands.
func TestAbortStopsAPausedWorkersSelfAccept(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-abort-paused-worker")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	entry, err := s.entryFor(rs)
	if err != nil {
		t.Fatal(err)
	}
	old := settleWorkerLimit
	settleWorkerLimit = 1500 * time.Millisecond
	t.Cleanup(func() { settleWorkerLimit = old })
	workerDone := make(chan struct{})
	rs.wmu.Lock()
	rs.done = workerDone // published paused; worker live
	rs.wmu.Unlock()

	aborted := make(chan error, 1)
	start := time.Now()
	go func() { aborted <- s.RunAbortAs(context.Background(), run.ID, "", "human") }()
	for {
		if rs.decisionMu.TryLock() {
			rs.decisionMu.Unlock()
			time.Sleep(time.Millisecond)
			continue
		}
		break // abort holds it (in settleWorker)
	}
	workerErr := make(chan error, 1)
	go func() {
		workerErr <- s.unattendedAccept(context.Background(), rs, entry, "auto-triage")
		close(workerDone)
	}()
	var aerr error
	select {
	case aerr = <-aborted:
	case <-time.After(5 * time.Second):
		gate.awaitStart(t)
		gate.open(t)
		aerr = awaitWithin(t, aborted, "abort")
	}
	took := time.Since(start)
	var werr error
	select {
	case werr = <-workerErr:
	case <-time.After(5 * time.Second):
		gate.awaitStart(t)
		gate.open(t)
		werr = awaitWithin(t, workerErr, "worker accept")
	}
	d, _ := s.RunGet(context.Background(), run.ID)
	t.Logf("abort after %v: %v; worker accept: %v; run %s/%s accepted=%v", took, aerr, werr, d.Run.Status, d.Run.Verdict, d.Run.Accepted)
	if aerr != nil || d.Run.Accepted {
		t.Fatalf("abort = %v (after %v), run accepted=%v; want the abort to stop the worker's accept", aerr, took, d.Run.Accepted)
	}
}

// Review repro (yolo shape): the worker is running and auto-accepts. Any
// accept already installed — here a person's, held behind a door for a
// moment — is joined, and its outcome, decided for a caller that is NOT the
// worker, becomes the worker's.
func TestAYoloAcceptNeverInheritsAnotherCallersOutcome(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-yolo-join")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	entry, err := s.entryFor(rs)
	if err != nil {
		t.Fatal(err)
	}
	workerDone := make(chan struct{})
	rs.wmu.Lock()
	rs.run.Status, rs.run.Stage = "running", "build"
	rs.done = workerDone
	rs.wmu.Unlock()

	rs.decisionMu.Lock() // a short door ahead of both
	human := acceptAsync(s, context.Background(), run.ID, "")
	for rs.acceptInFlight() == nil {
		time.Sleep(time.Millisecond)
	}
	workerErr := make(chan error, 1)
	go func() {
		workerErr <- s.unattendedAccept(context.Background(), rs, entry, "")
		close(workerDone)
	}()
	time.Sleep(100 * time.Millisecond) // let the worker reach startAccept and join
	rs.decisionMu.Unlock()
	var werr error
	select {
	case werr = <-workerErr:
	case <-time.After(5 * time.Second):
		gate.awaitStart(t)
		gate.open(t)
		werr = awaitWithin(t, workerErr, "worker accept")
	}
	h := awaitWithin(t, human, "human accept")
	t.Logf("human: res=%+v err=%v", h.res, h.err)
	if werr != nil {
		t.Fatalf("yolo's own accept = %v; want it to accept as the worker", werr)
	}
}

// The worker's own accept never inherits an outcome decided for another
// caller. A person's accept of a running (yolo) run holds the decision and
// is refused by its gate; the worker's auto-accept, which arrived while it
// ran, then accepts for itself — a second replay, its own, which lands.
func TestAWorkerAcceptAfterAPersonsRefusedAcceptAcceptsItself(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-worker-after-refused")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	entry, err := s.entryFor(rs)
	if err != nil {
		t.Fatal(err)
	}
	workerDone := make(chan struct{})
	rs.wmu.Lock()
	rs.run.Status, rs.run.Stage = "running", "build"
	rs.done = workerDone
	rs.wmu.Unlock()

	human := acceptAsync(s, context.Background(), run.ID, "")
	gate.awaitStart(t) // the person's accept holds the decision
	waiting := make(chan struct{}, 1)
	workerWaitsForOtherAccept = func(r *runState) {
		if r == rs {
			waiting <- struct{}{}
		}
	}
	t.Cleanup(func() { workerWaitsForOtherAccept = func(*runState) {} })
	workerErr := make(chan error, 1)
	go func() {
		workerErr <- s.unattendedAccept(context.Background(), rs, entry, "")
		close(workerDone)
	}()
	// The worker waits for the person's accept rather than joining it.
	_ = awaitWithin(t, waiting, "the worker waiting for the person's accept")
	gate.failGate(t)
	if h := awaitWithin(t, human, "the person's accept"); h.err == nil {
		t.Fatal("fixture: the person's accept should be refused")
	}
	gate.awaitStart(t) // the worker's own replay
	gate.open(t)
	if err := awaitWithin(t, workerErr, "the worker's own accept"); err != nil {
		t.Fatalf("the worker's own accept = %v; want it to accept itself, not inherit the person's refusal", err)
	}
	if d, _ := s.RunGet(context.Background(), run.ID); !d.Run.Accepted {
		t.Fatal("the worker's own accept did not land")
	}
}

// Third adversarial review of #181: reject and land waited for a paused
// worker while holding the decision; a worker that accepts its own run on
// the way out (auto-triage, the tdd chain) then waited for that decision —
// a cycle the limit broke with "still finishing" on both. They now wait for
// the worker holding nothing, so its accept settles first, and answer about
// the settled run.
func TestRejectAndLandWaitForAPausedWorkersSelfAccept(t *testing.T) {
	for _, door := range []string{"reject", "land"} {
		t.Run(door, func(t *testing.T) {
			s, run, gate, _ := heldAcceptRun(t, "r-b529-door-"+door)
			s.runsMu.RLock()
			rs := s.runs[run.ID]
			s.runsMu.RUnlock()
			entry, err := s.entryFor(rs)
			if err != nil {
				t.Fatal(err)
			}
			old := settleWorkerLimit
			settleWorkerLimit = 20 * time.Second
			t.Cleanup(func() { settleWorkerLimit = old })
			workerDone := make(chan struct{})
			rs.wmu.Lock()
			rs.done = workerDone // published paused; worker still live
			rs.wmu.Unlock()
			waiting := make(chan struct{}, 1)
			settleWorkerWaiting = func(r *runState) {
				if r == rs {
					select {
					case waiting <- struct{}{}:
					default:
					}
				}
			}
			t.Cleanup(func() { settleWorkerWaiting = func(*runState) {} })

			answered := make(chan error, 1)
			go func() {
				if door == "reject" {
					answered <- s.RunRejectAs(context.Background(), run.ID, "", "human")
				} else {
					answered <- s.RunLand(context.Background(), run.ID, "deadbeef", "human", "")
				}
			}()
			_ = awaitWithin(t, waiting, "the door waiting for the worker")
			workerErr := make(chan error, 1)
			go func() {
				workerErr <- s.unattendedAccept(context.Background(), rs, entry, "auto-triage")
				close(workerDone)
			}()
			gate.awaitStart(t)
			gate.open(t)
			if err := awaitWithin(t, workerErr, "the worker's own accept"); err != nil {
				t.Fatalf("the worker's own accept = %v; want it to land", err)
			}
			start := time.Now()
			err = awaitWithin(t, answered, "the door")
			if err == nil || !strings.Contains(err.Error(), "already accepted") || time.Since(start) > 5*time.Second {
				t.Fatalf("%s during the worker's own accept = %v; want it to answer about the accepted run", door, err)
			}
		})
	}
}
