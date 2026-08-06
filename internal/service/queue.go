package service

import (
	"context"
	"sync"

	"github.com/jrullan/ducklab/internal/bus"
)

// runQueue bounds how many runs execute at once (01 §9, AC-25).
//
// Without a bound, N concurrent runs each spawning M contestants would open
// N*M model connections and N*M worktrees. A run that cannot start is QUEUED
// and visible to clients, never rejected: the user asked for the work, and
// silently dropping it would be worse than making them wait.
type runQueue struct {
	mu      sync.Mutex
	limit   int
	running int
	waiting []*queued
	perProj map[string]int
	// held answers "is this project's working tree spoken for by a run the
	// queue is not counting?" — a run paused at its gate has released its
	// slot but its uncommitted diff still sits in the tree, and accept
	// commits the WHOLE tree (git add -A). Injected so tests can stub it.
	held func(projectID string) bool
}

// queued is one unit of work waiting for a slot. The queue does not know what
// kind of run it is — build and test-first both come through here, each
// carrying its own exec.
type queued struct {
	rs   *runState
	exec func(ctx context.Context)
	ctx  context.Context
	// parallel is the caller's explicit opt-out of the one-run-per-project
	// rule; they are claiming the runs cannot collide.
	parallel bool
	// chained marks the second half of a TDD chain. It jumps to the FRONT of
	// the waiting line: the person authorized test-and-build as one unit, and
	// letting another task's test run in between would land that test on a
	// suite the chain has deliberately left red.
	chained bool
}

func newRunQueue(limit int) *runQueue {
	if limit <= 0 {
		limit = 2
	}
	return &runQueue{limit: limit, perProj: map[string]int{}}
}

// submit either starts a run immediately or queues it.
func (q *runQueue) submit(s *Service, item *queued) {
	q.mu.Lock()
	if q.canStart(item) {
		q.reserve(item)
		q.mu.Unlock()
		q.start(s, item)
		return
	}
	reason := "engine at max_concurrent_runs"
	if q.running < q.limit {
		reason = "another run holds this project's working tree"
	}
	if item.chained {
		q.waiting = append([]*queued{item}, q.waiting...)
	} else {
		q.waiting = append(q.waiting, item)
	}
	q.mu.Unlock()

	item.rs.run.Status = "queued"
	item.rs.writer.AppendEvent("run_queued", map[string]interface{}{
		"reason": reason,
	})
	item.rs.writer.WriteState()
}

func (q *runQueue) canStart(item *queued) bool {
	if q.running >= q.limit {
		return false
	}
	// One run per project unless the caller opted in: two runs editing one
	// working tree would interleave writes — whether the other run is still
	// executing (counted in perProj) or paused at a gate with its diff
	// waiting in the tree (reported by held).
	if !item.parallel {
		if q.perProj[item.rs.run.ProjectID] > 0 {
			return false
		}
		if q.held != nil && q.held(item.rs.run.ProjectID) {
			return false
		}
	}
	return true
}

func (q *runQueue) reserve(item *queued) {
	q.running++
	q.perProj[item.rs.run.ProjectID]++
}

func (q *runQueue) start(s *Service, item *queued) {
	item.rs.run.Status = "running"
	item.rs.writer.WriteState()
	if s.bus != nil {
		s.bus.Publish(bus.Event{
			Type: "run_started", RunID: item.rs.run.ID, ProjectID: item.rs.run.ProjectID,
		})
	}
	go func() {
		defer q.done(s, item)
		item.exec(item.ctx)
	}()
}

// done releases the slot and promotes the first waiting run that can now run.
func (q *runQueue) done(s *Service, item *queued) {
	q.mu.Lock()
	q.running--
	if n := q.perProj[item.rs.run.ProjectID]; n > 0 {
		q.perProj[item.rs.run.ProjectID] = n - 1
	}
	next := q.promoteLocked()
	q.mu.Unlock()

	if next != nil {
		q.start(s, next)
	}
}

// poke re-examines the waiting line after something OUTSIDE the queue changed
// the answer to canStart — a human accepted or rejected the paused run whose
// diff was holding the project's tree. Without this, a run queued behind a
// gate would wait forever: the gate's resolution frees the tree, but no slot
// was released, so done() never runs.
func (q *runQueue) poke(s *Service) {
	for {
		q.mu.Lock()
		next := q.promoteLocked()
		q.mu.Unlock()
		if next == nil {
			return
		}
		q.start(s, next)
	}
}

// promoteLocked pops and reserves the first waiting run that can start.
// Callers hold q.mu.
func (q *runQueue) promoteLocked() *queued {
	for i, w := range q.waiting {
		if q.canStart(w) {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			q.reserve(w)
			return w
		}
	}
	return nil
}

// drain removes every waiting run, marking it paused. Used on shutdown so a
// queued run is not lost: it is on disk and resumable.
func (q *runQueue) drain() []*queued {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.waiting
	q.waiting = nil
	return out
}

// stats reports the queue state for /v1/engine and the status bar.
func (q *runQueue) stats() (running, waiting, limit int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running, len(q.waiting), q.limit
}
