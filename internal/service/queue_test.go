package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

// queuedRun builds a queue item with a real writer — submit and start write
// run state, so a nil writer only works for tests that never enqueue.
func queuedRun(t *testing.T, id, proj string, exec func()) *queued {
	t.Helper()
	run := &runlog.Run{ID: id, ProjectID: proj, Status: "running"}
	w, err := runlog.NewWriter(t.TempDir(), run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return &queued{
		rs:   &runState{run: run, writer: w},
		ctx:  context.Background(),
		exec: func(context.Context) { exec() },
	}
}

// AC-25: with a limit of 1, a second run is QUEUED, not rejected, and starts
// automatically when the first finishes.
func TestQueueLimitsConcurrencyAndPromotesWaiting(t *testing.T) {
	q := newRunQueue(1)

	var mu sync.Mutex
	var order []string
	release := make(chan struct{})

	// Simulate the queue's start/done cycle without a real Service.
	run := func(id string, done func()) {
		mu.Lock()
		order = append(order, id)
		mu.Unlock()
		<-release
		done()
	}

	first := &queued{rs: fakeRunState("r-1", "proj")}
	second := &queued{rs: fakeRunState("r-2", "proj")}

	q.mu.Lock()
	if !q.canStart(first) {
		t.Fatal("first run could not start on an empty queue")
	}
	q.reserve(first)
	q.mu.Unlock()

	q.mu.Lock()
	canSecond := q.canStart(second)
	q.mu.Unlock()
	if canSecond {
		t.Error("second run was allowed to start while the limit was reached")
	}

	go run("r-1", func() {
		q.mu.Lock()
		q.running--
		q.perProj["proj"]--
		q.mu.Unlock()
	})
	close(release)
	time.Sleep(50 * time.Millisecond)

	q.mu.Lock()
	canNow := q.canStart(second)
	q.mu.Unlock()
	if !canNow {
		t.Error("second run still blocked after the first finished")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 1 || order[0] != "r-1" {
		t.Errorf("execution order = %v", order)
	}
}

// One run per project unless the caller opts in: two runs editing one working
// tree would interleave writes.
func TestQueueSerialisesRunsWithinAProject(t *testing.T) {
	q := newRunQueue(4)
	a := &queued{rs: fakeRunState("r-1", "proj")}
	b := &queued{rs: fakeRunState("r-2", "proj")}
	c := &queued{rs: fakeRunState("r-3", "other")}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.reserve(a)

	if q.canStart(b) {
		t.Error("a second run in the same project was allowed to start")
	}
	if !q.canStart(c) {
		t.Error("a run in a different project was blocked")
	}

	b.parallel = true
	if !q.canStart(b) {
		t.Error("--parallel did not override the per-project limit")
	}
}

// A run paused at its gate has released its slot, but its uncommitted diff
// still owns the working tree — and accept commits the WHOLE tree. The queue
// must treat that project as busy, and must re-examine the line when the gate
// resolves, because no slot release will ever do it.
func TestAHeldTreeBlocksTheQueueUntilPoked(t *testing.T) {
	held := true
	q := newRunQueue(2)
	q.held = func(string) bool { return held }
	s := &Service{}

	started := make(chan struct{})
	item := queuedRun(t, "r-1", "proj", func() { close(started) })
	q.submit(s, item)

	if item.rs.run.Status != "queued" {
		t.Fatalf("status = %q, want queued — the paused run's diff is in the tree", item.rs.run.Status)
	}
	select {
	case <-started:
		t.Fatal("the run started over another run's undecided diff")
	case <-time.After(30 * time.Millisecond):
	}

	// The human decides the gate; accept/reject poke the queue.
	held = false
	q.poke(s)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the queue never promoted the waiting run after the hold cleared")
	}
}

// The TDD chain is one unit: its build jumps the line, so no other task's
// test lands on the suite the chain has deliberately left red. Here a test
// run occupies the slot, another task queues behind it, and the chained build
// arrives last — yet runs first.
func TestAChainedBuildJumpsTheWaitingLine(t *testing.T) {
	q := newRunQueue(1)
	s := &Service{}

	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	record := func(id string) func() {
		return func() {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			wg.Done()
		}
	}

	release := make(chan struct{})
	wg.Add(3)
	occupier := queuedRun(t, "r-test-a", "proj", func() { <-release; wg.Done() })
	q.submit(s, occupier)

	otherTest := queuedRun(t, "r-test-b", "proj", record("other-test"))
	q.submit(s, otherTest)

	chained := queuedRun(t, "r-build-a", "proj", record("chained-build"))
	chained.chained = true
	q.submit(s, chained)

	close(release)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "chained-build" || order[1] != "other-test" {
		t.Errorf("execution order = %v, want the chained build first", order)
	}
}

func TestQueueStats(t *testing.T) {
	q := newRunQueue(2)
	running, waiting, limit := q.stats()
	if running != 0 || waiting != 0 || limit != 2 {
		t.Errorf("stats = %d/%d/%d, want 0/0/2", running, waiting, limit)
	}
}

func TestQueueDefaultLimit(t *testing.T) {
	if _, _, limit := newRunQueue(0).stats(); limit != 2 {
		t.Errorf("default limit = %d, want 2", limit)
	}
}

// Test-first arrived after the queue and was never wired in: launching
// several TDD tasks at once raced their test runs over one working tree.
// Now it queues like every run that writes the tree — here, behind a build
// paused at its gate whose diff the person has not yet decided on.
func TestATestFirstQueuesBehindAPausedBuild(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"verify.mode": "tests", "verify.tests": "true",
	}); err != nil {
		t.Fatal(err)
	}
	paused := &runlog.Run{
		ID: "r-held", ProjectID: p.ID, TaskID: "T-001", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		StartedAt: "2026-08-06T10:00:00Z",
	}
	w, err := runlog.NewWriter(dir, paused)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	run, err := s.TestStart(context.Background(), p.ID, TestFirstRequest{TaskID: "T-002"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "queued" {
		t.Errorf("status = %q, want queued — the paused build's diff still owns the tree", run.Status)
	}
}

// Draining on shutdown must hand back every waiting run so none is lost.
func TestQueueDrainReturnsWaitingRuns(t *testing.T) {
	q := newRunQueue(1)
	q.waiting = []*queued{{rs: fakeRunState("r-1", "p")}, {rs: fakeRunState("r-2", "p")}}
	drained := q.drain()
	if len(drained) != 2 {
		t.Fatalf("drained %d runs, want 2", len(drained))
	}
	if _, waiting, _ := q.stats(); waiting != 0 {
		t.Errorf("%d runs still waiting after drain", waiting)
	}
}
