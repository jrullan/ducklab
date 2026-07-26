package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

// isolate points every ducklab directory at a temp dir so tests never touch
// the developer's real registry.
func isolate(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, filepath.Join(root, k))
	}
	t.Setenv("LocalAppData", filepath.Join(root, "local"))
	t.Setenv("AppData", filepath.Join(root, "roaming"))
}

// newTestProject creates a project directory registered with the service.
func newTestProject(t *testing.T, s *Service, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ducklab", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := s.registry.Register(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.registry.Save(); err != nil {
		t.Fatal(err)
	}
	return id
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	isolate(t)
	cfg := config.DefaultGlobal()
	s, err := New(cfg, Options{Bus: bus.New(64)})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// writeRun plants a run on disk as if a previous engine had created it.
func writeRun(t *testing.T, projectPath, projectID, runID, status string) {
	t.Helper()
	run := &runlog.Run{
		ID:        runID,
		ProjectID: projectID,
		Stage:     "build",
		Mode:      "solo",
		TaskID:    "T-001",
		Status:    status,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(projectPath, run)
	if err != nil {
		t.Fatal(err)
	}
	w.AppendEvent("run_start", map[string]interface{}{"mode": "solo"})
	w.AppendEvent("turn_start", map[string]interface{}{"turn": 0})
	w.Close()
}

// AC-10: an engine that died mid-run leaves the run resumable, not orphaned.
//
// Before RecoverRuns existed, state.json was written but never read back: a
// restarted engine reported "run not found" for every past run, and a run
// stuck in "running" could never be resumed because RunResume requires
// "paused". That is a direct violation of I9.
func TestRecoverRunsRepairsOrphanedRun(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, err := s.registry.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	writeRun(t, entry.Path, projectID, "r-orphan", "running")

	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	detail, err := s.RunGet(context.Background(), "r-orphan")
	if err != nil {
		t.Fatalf("run not found after recovery: %v", err)
	}
	if detail.Run.Status != "paused" {
		t.Errorf("status = %q, want paused", detail.Run.Status)
	}
	if detail.Run.PendingKind != "engine_restart" {
		t.Errorf("pending_kind = %q, want engine_restart", detail.Run.PendingKind)
	}

	// The repair must be durable, not just in memory.
	onDisk, err := runlog.ReadState(runlog.RunDirFor(entry.Path, "r-orphan"))
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Status != "paused" {
		t.Errorf("state.json status = %q, want paused", onDisk.Status)
	}
}

func TestRecoverRunsPreservesTerminalRuns(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-done", "done")

	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	detail, err := s.RunGet(context.Background(), "r-done")
	if err != nil {
		t.Fatalf("completed run not visible after restart: %v", err)
	}
	if detail.Run.Status != "done" {
		t.Errorf("status = %q, want done (a finished run must not be repaired)", detail.Run.Status)
	}
}

// A rehydrated run must be listable and its directory resolvable, or the SSE
// backlog silently returns nothing after every restart.
func TestRecoveredRunIsListableAndHasRunDir(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-list", "done")

	s.RecoverRuns(context.Background())

	runs, err := s.RunList(context.Background(), RunFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != "r-list" {
		t.Fatalf("RunList = %+v, want one run r-list", runs)
	}
	dir := s.RunDir("r-list")
	if dir == "" {
		t.Fatal("RunDir empty for a recovered run — SSE backlog would be silently empty")
	}
	events, err := runlog.ReadEvents(dir)
	if err != nil || len(events) != 2 {
		t.Fatalf("events from recovered run dir: %d, err=%v", len(events), err)
	}
}

// Recovery must continue past a project whose directory has gone missing.
func TestRecoverRunsToleratesMissingProject(t *testing.T) {
	s := newTestService(t)
	goodID := newTestProject(t, s, "good")
	goodEntry, _ := s.registry.Get(goodID)
	writeRun(t, goodEntry.Path, goodID, "r-good", "running")

	badDir := t.TempDir()
	badID, _ := s.registry.Register(badDir, "bad")
	s.registry.Save()
	os.RemoveAll(badDir)
	_ = badID

	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatalf("recovery aborted on a missing project: %v", err)
	}
	if _, err := s.RunGet(context.Background(), "r-good"); err != nil {
		t.Errorf("healthy project was skipped: %v", err)
	}
}

// A torn state.json must not stop recovery of the other runs.
func TestRecoverRunsSkipsUnreadableState(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-ok", "running")

	tornDir := runlog.RunDirFor(entry.Path, "r-torn")
	os.MkdirAll(tornDir, 0o755)
	os.WriteFile(filepath.Join(tornDir, "state.json"), []byte(`{"id":"r-torn","stat`), 0o644)

	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunGet(context.Background(), "r-ok"); err != nil {
		t.Errorf("valid run skipped because a sibling was torn: %v", err)
	}
	if _, err := s.RunGet(context.Background(), "r-torn"); err == nil {
		t.Error("torn run was loaded; it should have been skipped")
	}
}

func TestRecoverRunsIsIdempotent(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-x", "running")

	s.RecoverRuns(context.Background())
	first, _ := s.RunGet(context.Background(), "r-x")
	firstEvents := len(first.Events)

	s.RecoverRuns(context.Background())
	second, _ := s.RunGet(context.Background(), "r-x")
	if len(second.Events) != firstEvents {
		t.Errorf("second recovery appended events: %d then %d", firstEvents, len(second.Events))
	}
	if second.Run.Status != "paused" {
		t.Errorf("status = %q, want paused", second.Run.Status)
	}
}

// PauseAllRuns checkpoints in-flight work on a graceful stop. Nothing may be
// marked FAILED: shutting the engine down is not a failure of the work.
func TestPauseAllRunsCheckpointsInsteadOfFailing(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-live", "running")
	s.RecoverRuns(context.Background())

	// Put it back to running, as though it had been resumed and was working.
	s.runsMu.RLock()
	rs := s.runs["r-live"]
	s.runsMu.RUnlock()
	rs.run.Status = "running"
	rs.run.PendingKind = ""

	if err := s.PauseAllRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rs.run.Status != "paused" {
		t.Errorf("status = %q, want paused", rs.run.Status)
	}
	if rs.run.Verdict == "FAILED" {
		t.Error("graceful shutdown marked the run FAILED")
	}
	if rs.run.PendingKind != "engine_shutdown" {
		t.Errorf("pending_kind = %q, want engine_shutdown", rs.run.PendingKind)
	}

	onDisk, err := runlog.ReadState(runlog.RunDirFor(entry.Path, "r-live"))
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Status != "paused" {
		t.Errorf("state.json status = %q, want paused", onDisk.Status)
	}
}

// The full AC-10 cycle: engine stops with a run in flight, a new engine starts,
// and the run is resumable.
func TestShutdownThenRestartLeavesRunResumable(t *testing.T) {
	isolate(t)
	cfg := config.DefaultGlobal()

	s1, err := New(cfg, Options{Bus: bus.New(64)})
	if err != nil {
		t.Fatal(err)
	}
	projectID := newTestProject(t, s1, "proj")
	entry, _ := s1.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-cycle", "running")
	s1.RecoverRuns(context.Background())
	s1.runsMu.RLock()
	s1.runs["r-cycle"].run.Status = "running"
	s1.runsMu.RUnlock()

	// Engine 1 stops gracefully.
	if err := s1.PauseAllRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Engine 2 starts fresh: nothing in memory, everything from disk.
	s2, err := New(cfg, Options{Bus: bus.New(64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	detail, err := s2.RunGet(context.Background(), "r-cycle")
	if err != nil {
		t.Fatalf("run invisible to the new engine: %v", err)
	}
	if detail.Run.Status != "paused" {
		t.Fatalf("status = %q, want paused — RunResume would refuse it", detail.Run.Status)
	}
	if detail.Run.TaskID != "T-001" {
		t.Errorf("task_id lost across restart: %q", detail.Run.TaskID)
	}
}

// fakeRunState builds a minimal runState for queue tests.
func fakeRunState(id, projectID string) *runState {
	return &runState{run: &runlog.Run{ID: id, ProjectID: projectID, Status: "running"}}
}
