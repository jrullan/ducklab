package service

import (
	"context"
	"sync"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/registry"
)

// runQueue bounds how many runs execute at once (01 §9, AC-25).
//
// Without a bound, N concurrent runs each spawning M contestants would open
// N*M model connections and N*M worktrees. A run that cannot start is QUEUED
// and visible to clients, never rejected: the user asked for the work, and
// silently dropping it would be worse than making them wait.
type runQueue struct {
	mu       sync.Mutex
	limit    int
	running  int
	waiting  []*queued
	perProj  map[string]int
	allowPar bool
}

type queued struct {
	rs      *runState
	entry   *registry.ProjectEntry
	req     RunRequest
	ctx     context.Context
	release chan struct{}
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
	q.waiting = append(q.waiting, item)
	q.mu.Unlock()

	item.rs.run.Status = "queued"
	item.rs.writer.AppendEvent("run_queued", map[string]interface{}{
		"reason": "engine at max_concurrent_runs",
	})
	item.rs.writer.WriteState()
}

func (q *runQueue) canStart(item *queued) bool {
	if q.running >= q.limit {
		return false
	}
	// One run per project unless the caller opted in: two runs editing one
	// working tree would interleave writes.
	if !item.req.Parallel && q.perProj[item.rs.run.ProjectID] > 0 {
		return false
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
		s.executeRun(item.ctx, item.rs, item.entry, item.req)
	}()
}

// done releases the slot and promotes the first waiting run that can now run.
func (q *runQueue) done(s *Service, item *queued) {
	q.mu.Lock()
	q.running--
	if n := q.perProj[item.rs.run.ProjectID]; n > 0 {
		q.perProj[item.rs.run.ProjectID] = n - 1
	}
	var next *queued
	for i, w := range q.waiting {
		if q.canStart(w) {
			next = w
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			q.reserve(w)
			break
		}
	}
	q.mu.Unlock()

	if next != nil {
		q.start(s, next)
	}
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
