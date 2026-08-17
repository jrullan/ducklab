package service

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

// newClockedService builds a service whose clock and restart deadline are under
// the test's control, so bounded restart recovery is exercised without sleeping.
func newClockedService(t *testing.T, now func() time.Time, deadline time.Duration) *Service {
	t.Helper()
	isolate(t)
	cfg := config.DefaultGlobal()
	s, err := New(cfg, Options{Bus: bus.New(64), Now: now, RestartRecoveryDeadline: deadline})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// insertRunningRun plants a live run in the service as if a worker were mid-gate,
// returning its state so a test can drive restart recovery over it.
func insertRunningRun(t *testing.T, s *Service, projectPath, projectID, runID string) *runState {
	t.Helper()
	run := &runlog.Run{
		ID:        runID,
		ProjectID: projectID,
		Stage:     "build",
		Mode:      "solo",
		TaskID:    "T-001",
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	rs := &runState{
		run:         run,
		runDir:      runlog.RunDirFor(projectPath, runID),
		projectPath: projectPath,
		done:        closedChan(),
		cancel:      func() {},
	}
	s.runsMu.Lock()
	s.runs[runID] = rs
	s.runsMu.Unlock()
	return rs
}

func eventsByType(t *testing.T, dir, typ string) []*runlog.Event {
	t.Helper()
	events, err := runlog.ReadEvents(dir)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var out []*runlog.Event
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// B-046: a restart that checkpoints a live engine's runs but never completes
// must not park them forever. After the deadline the still-alive engine
// un-checkpoints its own runs and resumes them, recording who asked for the
// restart and that it was abandoned. The recovery path does not care who walks
// it: the checkpointed run offers resume, not only abort.
func TestRestartRecoversAbandonedCheckpointsAfterDeadline(t *testing.T) {
	var mu sync.Mutex
	clock := time.Date(2026, 8, 17, 0, 5, 26, 0, time.UTC)
	nowFn := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }

	deadline := 30 * time.Second
	s := newClockedService(t, nowFn, deadline)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	rs := insertRunningRun(t, s, entry.Path, projectID, "r-restart")

	// A restart is requested against a live engine, carrying its requester.
	if err := s.RequestRestart(context.Background(), "operator-alice"); err != nil {
		t.Fatalf("RequestRestart: %v", err)
	}

	// The request lands in the record with its requester.
	reqs := eventsByType(t, rs.runDir, "restart_request")
	if len(reqs) != 1 {
		t.Fatalf("restart_request events = %d, want 1", len(reqs))
	}
	if got, _ := reqs[0].Data["requester"].(string); got != "operator-alice" {
		t.Errorf("restart_request requester = %q, want operator-alice", got)
	}

	// The checkpoint carries a deadline so a stalled restart is recoverable.
	if rs.run.Status != "paused" || rs.run.PendingKind != "engine_restart" {
		t.Fatalf("run = %s/%s, want paused/engine_restart", rs.run.Status, rs.run.PendingKind)
	}
	if stringValue(rs.run.PendingData, "deadline") == "" {
		t.Fatal("checkpoint carries no deadline; a stalled restart could park forever")
	}

	// A checkpointed run offers resume to operators, not only abort.
	if next := runNext(rs.run); !slices.Contains(next, "resume") {
		t.Errorf("paused-by-restart actions = %v, want resume offered", next)
	}

	// Before the deadline, recovery leaves the checkpoint alone.
	if err := s.RecoverAbandonedRestarts(context.Background()); err != nil {
		t.Fatalf("early recovery: %v", err)
	}
	if got := eventsByType(t, rs.runDir, "restart_abandoned"); len(got) != 0 {
		t.Fatalf("restart abandoned before its deadline: %d events", len(got))
	}

	// Past the deadline the still-alive engine un-checkpoints and resumes.
	advance(deadline + time.Second)
	if err := s.RecoverAbandonedRestarts(context.Background()); err != nil {
		t.Fatalf("deadline recovery: %v", err)
	}

	abandoned := eventsByType(t, rs.runDir, "restart_abandoned")
	if len(abandoned) != 1 {
		t.Fatalf("restart_abandoned events = %d, want 1", len(abandoned))
	}
	if got, _ := abandoned[0].Data["requester"].(string); got != "operator-alice" {
		t.Errorf("restart_abandoned requester = %q, want operator-alice", got)
	}

	// The run left the paused/engine_restart state — it was resumed, not parked.
	s.runsMu.RLock()
	resumed := s.runs["r-restart"].run
	s.runsMu.RUnlock()
	if resumed.Status == "paused" && resumed.PendingKind == "engine_restart" {
		t.Error("run stayed checkpointed after its deadline; work was left parked")
	}

	// Let the resumed worker settle before the test's temp dir is torn down:
	// it would otherwise still be writing state.json into the project as
	// cleanup races it. A resume with no project config fails fast; either way
	// the worker's done channel is the settled point.
	s.runsMu.RLock()
	done := s.runs["r-restart"].done
	s.runsMu.RUnlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("the resumed run never settled")
		}
	}
}
