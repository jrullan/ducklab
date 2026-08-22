package service

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jrullan/ducklab/internal/config"

	"github.com/jrullan/ducklab/internal/bus"
)

// runQueue bounds how many runs execute at once (01 §9, AC-25).
//
// Without a bound, N concurrent runs each spawning M contestants would open
// N*M model connections and N*M worktrees. A run that cannot start is QUEUED
// and visible to clients, never rejected: the user asked for the work, and
// silently dropping it would be worse than making them wait.
type runQueue struct {
	mu          sync.Mutex
	limit       int
	running     int
	waiting     []*queued
	perProj     map[string]int
	perProvider map[string]int
	holders     map[string][]string
	providerCap func(string) (int, bool)
	// held answers "may a run for this task start in this project?" with a
	// reason when it may not — a run paused at its gate has released its
	// slot but its uncommitted diff still sits in the tree (and accept
	// commits the WHOLE tree, git add -A); a broken TDD chain keeps the
	// suite deliberately red for every task but its own. Empty string means
	// free. Injected so tests can stub it.
	held func(projectID, taskID string) string
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
	chained   bool
	providers []string
}

func newRunQueue(limit int) *runQueue {
	if limit <= 0 {
		limit = 2
	}
	return &runQueue{limit: limit, perProj: map[string]int{}, perProvider: map[string]int{}, holders: map[string][]string{}}
}

// submit either starts a run immediately or queues it.
func (q *runQueue) submit(s *Service, item *queued) {
	// Keep the lookup live: changing a provider's URL or explicit cap must
	// affect the next queue decision without restarting or migrating state.
	q.mu.Lock()
	if q.providerCap == nil {
		q.providerCap = func(id string) (int, bool) {
			if s == nil || s.cfg == nil {
				return 0, false
			}
			s.cfgMu.RLock()
			p, ok := s.cfg.Providers[config.ProviderID(id)]
			s.cfgMu.RUnlock()
			if !ok {
				return 0, false
			}
			if p.MaxConcurrent > 0 {
				return p.MaxConcurrent, true
			}
			if IsLocalHost(p.BaseURL) {
				return 1, true
			}
			return 8, true
		}
	}
	q.mu.Unlock()
	item.providers = item.providers[:0]
	seen := map[string]bool{}
	if s != nil && s.cfg != nil {
		s.cfgMu.RLock()
		for _, id := range item.rs.run.Roster {
			if d, ok := s.cfg.Ducklings[config.DucklingID(id)]; ok && !seen[string(d.Provider)] {
				item.providers = append(item.providers, string(d.Provider))
				seen[string(d.Provider)] = true
			}
		}
		s.cfgMu.RUnlock()
	}
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
		if q.held != nil {
			if r := q.held(item.rs.run.ProjectID, item.rs.run.TaskID); r != "" {
				reason = r
			}
		}
	}
	if p := q.providerHold(item); p != "" {
		reason = fmt.Sprintf("waiting for a slot on provider %s (held by %s)", p, strings.Join(q.holders[p], ", "))
	}
	if item.chained {
		q.waiting = append([]*queued{item}, q.waiting...)
	} else {
		q.waiting = append(q.waiting, item)
	}
	q.mu.Unlock()

	item.rs.run.Status = "queued"
	item.rs.run.QueuedReason = reason
	item.rs.writer.AppendEvent("run_queued", map[string]interface{}{
		"reason": reason,
	})
	item.rs.writer.WriteState()
}

func (q *runQueue) providerHold(item *queued) string {
	for _, p := range item.providers {
		cap := 8
		if q.providerCap != nil {
			if n, ok := q.providerCap(p); ok && n > 0 {
				cap = n
			}
		}
		if q.perProvider[p] >= cap {
			return p
		}
	}
	return ""
}

func (q *runQueue) canStart(item *queued) bool {
	if q.running >= q.limit {
		return false
	}
	for _, p := range item.providers {
		cap := 8
		if q.providerCap != nil {
			if n, ok := q.providerCap(p); ok && n > 0 {
				cap = n
			}
		}
		if q.perProvider[p] >= cap {
			return false
		}
	}
	// One run per project unless the caller opted in: two runs editing one
	// working tree would interleave writes — whether the other run is still
	// executing (counted in perProj) or paused at a gate with its diff
	// waiting in the tree (reported by held).
	if !item.parallel {
		if q.perProj[item.rs.run.ProjectID] > 0 {
			return false
		}
		if q.held != nil && q.held(item.rs.run.ProjectID, item.rs.run.TaskID) != "" {
			return false
		}
	}
	return true
}

func (q *runQueue) reserve(item *queued) {
	q.running++
	q.perProj[item.rs.run.ProjectID]++
	for _, p := range item.providers {
		q.perProvider[p]++
		q.holders[p] = append(q.holders[p], item.rs.run.ID)
	}
}

func (q *runQueue) start(s *Service, item *queued) {
	item.rs.run.Status = "running"
	item.rs.run.QueuedReason = ""
	item.rs.writer.WriteState()
	if s.bus != nil {
		s.bus.Publish(bus.Event{
			Type: "run_started", RunID: item.rs.run.ID, ProjectID: item.rs.run.ProjectID,
			// The desktop builds a provisional record from this event for runs
			// it did not launch (CLI, autopilot). Without these fields it
			// guessed: stage defaulted to "build" and started_at came from the
			// bus timestamp — which serializes with the LOCAL offset, so the
			// lexical sort against the API's UTC-Z strings buried a fresh run
			// hours deep in the list.
			Data: map[string]interface{}{
				"stage": item.rs.run.Stage, "mode": item.rs.run.Mode,
				"task_id": item.rs.run.TaskID, "started_at": item.rs.run.StartedAt,
			},
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
	for _, p := range item.providers {
		if n := q.perProvider[p]; n > 0 {
			q.perProvider[p] = n - 1
		}
		for i, id := range q.holders[p] {
			if id == item.rs.run.ID {
				q.holders[p] = append(q.holders[p][:i], q.holders[p][i+1:]...)
				break
			}
		}
	}
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
	for i := 0; i < len(q.waiting); {
		w := q.waiting[i]
		// A run that ended while it waited — aborted in the queue, mostly —
		// must never be promoted: start() stamps whatever it is handed
		// "running", and a dead run walked out of its grave wearing it.
		if w.rs.run.Status == "failed" || w.rs.run.Status == "done" {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			continue
		}
		if q.canStart(w) {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			q.reserve(w)
			return w
		}
		i++
	}
	return nil
}

// remove withdraws a waiting item — its run was aborted while it queued.
func (q *runQueue) remove(rs *runState) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, w := range q.waiting {
		if w.rs == rs {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			return true
		}
	}
	return false
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
