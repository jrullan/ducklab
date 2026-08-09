package service

import (
	"context"
	"fmt"
	"github.com/jrullan/ducklab/internal/strategy"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/registry"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

// pauseWaitPerRun bounds how long a graceful stop waits for one in-flight run
// to reach its next safe point before its state is written as paused anyway.
// I9 makes the hard case cheap: an unwaited run still resumes from its last
// checkpoint.
const pauseWaitPerRun = 5 * time.Second

// publishEvent forwards a persisted run event to the bus, carrying its seq so
// subscribers can deduplicate against a replayed backlog.
func (s *Service) publishEvent(projectID string, e *runlog.Event) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(bus.Event{
		Type:      e.Type,
		RunID:     e.RunID,
		ProjectID: projectID,
		Seq:       e.Seq,
		TS:        time.Now(),
		Data:      e.Data,
	})
}

// attachWriter wires a writer's event hook to the bus for this run.
func (s *Service) attachWriter(rs *runState, w *runlog.Writer) {
	projectID := rs.run.ProjectID
	w.OnEvent = func(e *runlog.Event) { s.publishEvent(projectID, e) }
}

// ensureWriter returns the run's writer, opening one for a rehydrated run.
// Historical runs are recovered without file handles; a writer is created only
// when something actually needs to mutate the run.
func (s *Service) ensureWriter(rs *runState) (*runlog.Writer, error) {
	rs.wmu.Lock()
	defer rs.wmu.Unlock()
	// A closed writer must not be handed back. Close leaves the struct in
	// place, so reusing one made every later append fail silently — the run's
	// own accept never reached its log while state.json recorded the commit.
	// OpenWriter appends and recovers the sequence, so reopening is safe.
	if rs.writer != nil && !rs.writer.Closed() {
		return rs.writer, nil
	}
	if rs.projectPath == "" {
		return nil, fmt.Errorf("run %q has no project path", rs.run.ID)
	}
	w, err := runlog.OpenWriter(rs.projectPath, rs.run)
	if err != nil {
		return nil, fmt.Errorf("open writer for run %q: %w", rs.run.ID, err)
	}
	s.attachWriter(rs, w)
	rs.writer = w
	return w, nil
}

// RecoverRuns rehydrates every run from disk and repairs runs that were left
// mid-flight by a dead engine.
//
// Without this the engine is amnesiac: state.json is written but never read
// back, so after a restart RunGet/RunResume/RunDir all report "not found" and
// a crashed run can never be resumed — a direct violation of I9.
//
// A run still marked "running" or "queued" cannot be running, because this
// runs before the HTTP server accepts connections and only one engine may
// hold the state directory. Such a run is moved to "paused" with reason
// engine_restart, which is the state RunResume accepts.
func (s *Service) RecoverRuns(ctx context.Context) error {
	entries := s.registry.List()
	recovered, repaired := 0, 0

	for _, entry := range entries {
		ids, err := runlog.ListRuns(entry.Path)
		if err != nil {
			// A project on an unmounted drive must not stop the engine.
			continue
		}
		sort.Strings(ids)
		for _, id := range ids {
			runDir := runlog.RunDirFor(entry.Path, id)
			run, err := runlog.ReadState(runDir)
			if err != nil {
				// No state.json, or a torn write: nothing to recover.
				continue
			}
			if run.ProjectID == "" {
				run.ProjectID = entry.ID
			}
			rs := &runState{
				run:         run,
				runDir:      runDir,
				projectPath: entry.Path,
				done:        closedChan(),
				cancel:      func() {},
			}
			s.runsMu.Lock()
			if _, exists := s.runs[id]; exists {
				s.runsMu.Unlock()
				continue
			}
			s.runs[id] = rs
			s.runsMu.Unlock()
			recovered++

			if run.Status == "running" || run.Status == "queued" {
				if err := s.markEngineRestart(rs); err == nil {
					repaired++
				}
			}
		}
	}

	// Reap scratch worktrees left by a dead engine. A stale record makes the
	// next tournament's `worktree add` fail on a path that looks free (AC-19).
	for _, entry := range entries {
		scratch := filepath.Join(entry.Path, ".ducklab", "worktrees")
		if _, err := os.Stat(scratch); err == nil {
			if err := strategy.ReapWorktrees(entry.Path, scratch); err != nil {
				continue
			}
		}
	}

	if recovered > 0 && s.bus != nil {
		s.bus.Publish(bus.Event{
			Type: "engine_recovered",
			TS:   time.Now(),
			Data: map[string]interface{}{
				"runs_recovered": recovered,
				"runs_repaired":  repaired,
			},
		})
	}
	return nil
}

// markEngineRestart moves an orphaned run to paused so it can be resumed.
func (s *Service) markEngineRestart(rs *runState) error {
	w, err := s.ensureWriter(rs)
	if err != nil {
		return err
	}
	rs.run.Status = "paused"
	rs.run.PendingKind = "engine_restart"
	if rs.run.PendingSince == "" {
		rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
	}
	w.AppendEvent("checkpoint", map[string]interface{}{
		"reason": "engine_restart",
		"status": "paused",
	})
	return w.WriteState()
}

// PauseAllRuns checkpoints every in-flight run as paused. Called on SIGTERM
// before the HTTP server stops, so a graceful stop loses no work and marks
// nothing FAILED.
func (s *Service) PauseAllRuns(ctx context.Context) error {
	s.shuttingDown.Store(true)
	// The apps the engine manages die with it — a process nobody can see or
	// stop from any surface is worse than a stopped app.
	s.stopAllApps()

	s.runsMu.RLock()
	var active []*runState
	for _, rs := range s.runs {
		if rs.run.Status == "running" || rs.run.Status == "queued" {
			active = append(active, rs)
		}
	}
	s.runsMu.RUnlock()

	// Cancel first so every run starts unwinding concurrently, then wait.
	for _, rs := range active {
		if rs.cancel != nil {
			rs.cancel()
		}
	}

	for _, rs := range active {
		if rs.done != nil {
			select {
			case <-rs.done:
			case <-time.After(pauseWaitPerRun):
			case <-ctx.Done():
			}
		}
		// The run goroutine may already have written a terminal state; only
		// an still-unfinished run is forced to paused.
		if rs.run.Status == "running" || rs.run.Status == "queued" {
			w, err := s.ensureWriter(rs)
			if err != nil {
				continue
			}
			rs.run.Status = "paused"
			rs.run.PendingKind = "engine_shutdown"
			rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
			w.AppendEvent("checkpoint", map[string]interface{}{
				"reason": "engine_shutdown",
				"status": "paused",
			})
			w.WriteState()
		}
	}
	return nil
}

// entryFor resolves a run's project entry, preferring the path recorded on the
// run state so a rehydrated run survives registry drift.
func (s *Service) entryFor(rs *runState) (*registry.ProjectEntry, error) {
	if entry, err := s.registry.Get(rs.run.ProjectID); err == nil {
		return entry, nil
	}
	if rs.projectPath != "" {
		return &registry.ProjectEntry{ID: rs.run.ProjectID, Path: rs.projectPath}, nil
	}
	return nil, fmt.Errorf("project %q not found for run %q", rs.run.ProjectID, rs.run.ID)
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// qaPair keeps a human's answer WITH the question that earned it, for the
// replayed prompt. The id-keyed map below matches only the exact question
// text — a replayed model that rewords its question hashes to a new id and
// asks the person the same thing again, in new words, forever.
type qaPair struct {
	q, a string
}

// answeredDecisions renders every answer this run has received, for
// prepending to a replayed prompt. A resumed run replays its turn from
// scratch: the model does not remember asking, so the decisions must be in
// front of it before it works, not filed under a hash it can no longer guess.
func (rs *runState) answeredDecisions() string {
	rs.wmu.Lock()
	defer rs.wmu.Unlock()
	if len(rs.qa) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Decisions the human already made for this run\n\n")
	b.WriteString("A prior attempt asked; the person answered. These are binding — do not ask about them again, in any wording:\n\n")
	for _, p := range rs.qa {
		fmt.Fprintf(&b, "Q: %s\nA: %s\n\n", p.q, p.a)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// answers returns the answers already given for this run, keyed by question id.
func (rs *runState) answers() map[string]string {
	rs.wmu.Lock()
	defer rs.wmu.Unlock()
	out := map[string]string{}
	for k, v := range rs.givenAnswers {
		out[k] = v
	}
	return out
}

// recordAnswer stores a human's answer so a resumed run can use it — both
// under the question's id (exact-match resolution in the ask_human tool) and
// beside its text (the replayed prompt, which is what survives rewording).
func (rs *runState) recordAnswer(id, question, answer string) {
	rs.wmu.Lock()
	defer rs.wmu.Unlock()
	if rs.givenAnswers == nil {
		rs.givenAnswers = map[string]string{}
	}
	rs.givenAnswers[id] = answer
	if question != "" {
		rs.qa = append(rs.qa, qaPair{q: question, a: answer})
	}
}

// pauseForQuestion checkpoints a run that stopped for human input.
//
// No goroutine is held: the run's goroutine returns here, and the run sits on
// disk until a client answers. That is what makes an unanswered question
// indefinitely cheap rather than a leak.
func (s *Service) pauseForQuestion(rs *runState, q *tools.PendingQuestion) {
	w, err := s.ensureWriter(rs)
	if err != nil {
		return
	}
	rs.run.Status = "paused"
	rs.run.PendingKind = "question"
	rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
	rs.run.PendingData = map[string]interface{}{
		"question_id": q.ID,
		"question":    q.Question,
	}
	if len(q.Options) > 0 {
		rs.run.PendingData["options"] = q.Options
	}
	w.AppendEvent("human_needed", map[string]interface{}{
		"kind":        "question",
		"question_id": q.ID,
		"question":    q.Question,
		"options":     q.Options,
	})
	w.WriteState()
	// The advisor drafts the answer while the question waits — a fleet of
	// models must not stall on one model's question when the human's real
	// role is to choose, not to research.
	s.adviseQuestion(rs, q)
}

// clearPending wipes the human-gate block when a run stops waiting.
//
// A finished run that still advertises pending_kind is stale state: the inbox
// filters on status today and so hides it, but anything keying on the field
// alone — a future badge, a report, RunAnswer — would treat a committed run as
// still blocked.
func clearPending(run *runlog.Run) {
	run.PendingKind = ""
	run.PendingSince = ""
	run.PendingData = nil
}
