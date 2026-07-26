package service

import (
	"sync"
	"testing"
	"time"
)

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

	b.req.Parallel = true
	if !q.canStart(b) {
		t.Error("--parallel did not override the per-project limit")
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
