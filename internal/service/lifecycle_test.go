package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
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

// The registry was append-only: a throwaway project stayed in every client's
// list for good, and the only way out was hand-editing the daemon's state.
func TestProjectForgetUnregistersWithoutTouchingTheDirectory(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "Temp", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectForget(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ProjectList(context.Background())
	for _, got := range list {
		if got.ID == p.ID {
			t.Error("the project is still registered")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".ducklab")); err != nil {
		t.Errorf("forget deleted files it should not have touched: %v", err)
	}
	if err := s.ProjectForget(context.Background(), p.ID); err == nil {
		t.Error("forgetting an unknown project should fail, not silently succeed")
	}
}

// MarkMissing computed the flag and ProjectList dropped it, so a deleted
// directory looked like a perfectly healthy project to every client.
func TestProjectListReportsMissingDirectories(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "Temp", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ProjectList(context.Background())
	for _, got := range list {
		if got.ID == p.ID && !got.Missing {
			t.Error("a project whose directory is gone was reported as present")
		}
	}
}

// Two runs of the same task produce the same fix. Accepting the second after
// the first left the tree clean made git exit 1 with "nothing to commit", the
// raw error reached the user, and a run whose gate was green and whose
// reviewer approved was marked FAILED. Neither the person nor the run did
// anything wrong.
func TestAcceptingAnAlreadyCommittedChangeIsNotAFailure(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{
		ID: "r-dup", ProjectID: p.ID, TaskID: "T-001", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	// The tree is clean: Init already committed everything.
	res, err := s.RunAccept(context.Background(), "r-dup", "")
	if err != nil {
		t.Fatalf("accept failed on a clean tree: %v", err)
	}
	if res.CommitSHA == "" {
		t.Error("no commit reported; it should name the commit already carrying the change")
	}

	detail, err := s.RunGet(context.Background(), "r-dup")
	if err != nil {
		t.Fatal(err)
	}
	got := detail.Run
	if got.Status != "done" || !got.Accepted {
		t.Errorf("status=%q accepted=%v, want done/true", got.Status, got.Accepted)
	}
	if got.PendingKind != "" {
		t.Errorf("still waiting for someone: pending_kind=%q", got.PendingKind)
	}
}

// Close left eventsF non-nil, so a later AppendEvent wrote to a closed
// descriptor and returned an error every caller ignored. state.json kept
// updating — WriteState writes by path — while the events explaining the state
// vanished. Accepting a run recorded the commit and never recorded the accept,
// so every client still thought the run was waiting for a person: the desktop
// showed "waiting for you" and an enabled Accept button above the commit it
// had just made.
func TestAcceptIsWrittenToTheRunLog(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{
		ID: "r-log", ProjectID: p.ID, TaskID: "T-001", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.AppendEvent("human_needed", map[string]interface{}{"kind": "gate"})
	// The run finished, so its writer is closed — the state every accept
	// actually happens in.
	w.Close()
	s.RecoverRuns(context.Background())

	// Reproduce the state the real failure happened in: the run finished, so
	// its writer is still attached to the runState and closed. A recovered run
	// alone would not catch this — its writer is nil, so ensureWriter opens a
	// fresh one and the bug hides.
	s.runsMu.RLock()
	rs := s.runs["r-log"]
	s.runsMu.RUnlock()
	if rs == nil {
		t.Fatal("run not recovered")
	}
	rs.writer = w

	if _, err := s.RunAccept(context.Background(), "r-log", ""); err != nil {
		t.Fatal(err)
	}

	detail, err := s.RunGet(context.Background(), "r-log")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range detail.Events {
		kinds = append(kinds, e.Type)
	}
	for _, want := range []string{"human", "run_end"} {
		if !slices.Contains(kinds, want) {
			t.Errorf("no %q event in the log: %v", want, kinds)
		}
	}
}

// A writer that has been closed must say so rather than swallowing writes.
// Acceptance must prove the commit it records, not the working tree that
// happened to be available while it was made. In particular, git add -A obeys
// .gitignore: an ignored package can make the local gate green while the
// committed importer cannot compile in a fresh checkout.
func TestAcceptRejectsAnIgnoredPackageRequiredByCommittedCode(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	for path, contents := range map[string]string{
		"go.mod":     "module example.com/ignored-package\n\ngo 1.24\n",
		"add.go":     "package fixture\n",
		".gitignore": "build/\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "ignored-package", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}

	// The importer is staged and committed by acceptance, while build/ remains
	// on disk but is omitted because the unanchored ignore pattern matches it.
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package fixture\n\nimport _ \"example.com/ignored-package/internal/build\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "build", "build.go"), []byte("package build\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{
		ID: "r-ignored-import", ProjectID: p.ID, TaskID: "T-042", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, err = s.RunAccept(context.Background(), run.ID, "")
	if err == nil {
		t.Fatal("accepted a commit whose importer relies on ignored internal/build/")
	}
	if !strings.Contains(err.Error(), "internal/build") {
		t.Errorf("rejection does not name the omitted required path: %v", err)
	}
}

// An accepted run has two independent verification answers: its own verdict
// and the FULL gate result from the clean checkout acceptance check. The latter
// must survive onto both read surfaces and its durable event, including when the
// run itself never reached a gate.
func TestAcceptRecordsCleanCheckoutGateSeparatelyFromUnverifiedRunVerdict(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "reproduced", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"verify.mode":  "tests",
		"verify.tests": "true",
	}); err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{
		ID: "r-reproduced", ProjectID: p.ID, TaskID: "T-054", Stage: "build",
		Status: "paused", Verdict: "UNVERIFIED", PendingKind: "gate",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendEvent("human_needed", map[string]interface{}{"kind": "gate"}); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}

	assertReproducedGreen := func(where string, value interface{}) {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var record map[string]interface{}
		if err := json.Unmarshal(encoded, &record); err != nil {
			t.Fatal(err)
		}
		if record["verdict"] != "UNVERIFIED" {
			t.Errorf("%s verdict = %#v, want the run's own UNVERIFIED verdict", where, record["verdict"])
		}
		gate, ok := record["acceptance_gate"].(map[string]interface{})
		if !ok {
			t.Errorf("%s omitted acceptance_gate: %s", where, encoded)
			return
		}
		if gate["green"] != true || gate["exit_code"] != float64(0) || gate["gate"] != "tests" || gate["command"] != "true" {
			t.Errorf("%s acceptance_gate = %#v, want complete passing clean-checkout result", where, gate)
		}
		if _, ok := gate["output"]; !ok {
			t.Errorf("%s acceptance_gate lost gate output: %#v", where, gate)
		}
	}

	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertReproducedGreen("run_get", detail.Run)
	foundReproduced := false
	for _, event := range detail.Events {
		if event.Type != "gate_reproduced" {
			continue
		}
		foundReproduced = true
		if event.Data["green"] != true || event.Data["exit_code"] != 0 || event.Data["gate"] != "tests" || event.Data["command"] != "true" {
			t.Errorf("gate_reproduced event = %#v, want the passing acceptance gate result", event.Data)
		}
		if _, ok := event.Data["output"]; !ok {
			t.Errorf("gate_reproduced event lost gate output: %#v", event.Data)
		}
	}
	if !foundReproduced {
		t.Error("acceptance did not record a gate_reproduced event")
	}

	runs, err := s.RunList(context.Background(), RunFilter{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs list has %d records, want 1", len(runs))
	}
	assertReproducedGreen("runs list", runs[0])
}

// Accept must make the commit itself visible in the transcript, not merely
// announce the clean-checkout reproduction after it has landed. This applies
// when acceptance creates a commit and when another accepted run already left
// the tree clean.
func TestAcceptTranscriptAnnouncesCommitBeforeCleanCheckoutReproduction(t *testing.T) {
	for _, tc := range []struct {
		name  string
		dirty bool
	}{
		{name: "dirty tree creates a commit", dirty: true},
		{name: "clean tree reuses existing commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package fixture\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "accept-transcript", GitInit: true})
			if err != nil {
				t.Fatal(err)
			}
			if tc.dirty {
				if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package fixture\n\n// accepted change\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			run := &runlog.Run{
				ID: "r-accept-transcript", ProjectID: p.ID, TaskID: "T-080", Stage: "build",
				Status: "paused", Verdict: "PASSED", PendingKind: "gate",
				StartedAt: time.Now().UTC().Format(time.RFC3339),
			}
			w, err := runlog.NewWriter(dir, run)
			if err != nil {
				t.Fatal(err)
			}
			w.Close()
			if err := s.RecoverRuns(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
				t.Fatal(err)
			}

			detail, err := s.RunGet(context.Background(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			commitAnnouncement, reproductionAnnouncement, reproduced := -1, -1, -1
			for i, event := range detail.Events {
				if event.Type == "gate_reproduced" {
					reproduced = i
					continue
				}
				if event.Type != "gate_started" || (event.Data["phase"] != "accept" && event.Data["phase"] != "commit") {
					continue
				}
				detail, _ := event.Data["detail"].(string)
				if strings.Contains(detail, "reproducing the gate from a clean checkout") {
					reproductionAnnouncement = i
					continue
				}
				if strings.Contains(strings.ToLower(detail), "commit") {
					if strings.Contains(strings.ToLower(detail), "committed ") {
						t.Errorf("pre-commit announcement names a commit that does not exist yet: %q", detail)
					}
					commitAnnouncement = i
				}
			}
			if commitAnnouncement < 0 {
				t.Fatal("accept transcript has no pre-commit announcement")
			}
			if reproductionAnnouncement < 0 {
				t.Fatal("accept transcript lost the clean-checkout reproduction announcement")
			}
			if reproduced < 0 {
				t.Fatal("accept transcript lost gate_reproduced")
			}
			if commitAnnouncement >= reproductionAnnouncement {
				t.Errorf("commit announcement event %d must precede reproduction announcement event %d", commitAnnouncement, reproductionAnnouncement)
			}
			if reproductionAnnouncement >= reproduced {
				t.Errorf("reproduction announcement event %d must be closed by gate_reproduced event %d", reproductionAnnouncement, reproduced)
			}
		})
	}
}

// The commit-progress event is an operator promise, not a retrospective
// transcript entry: it must be durable before git starts staging, and remain
// visible if staging or committing aborts acceptance.
func TestAcceptLogsCommitProgressBeforeGitMutatesTheTree(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for acceptance tests")
	}
	for _, failure := range []string{"", "add", "commit"} {
		t.Run(map[string]string{"": "commit", "add": "staging failure", "commit": "commit failure"}[failure], func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			id, dir := projectWithDocs(t, s, nil)
			g := gitProject(t, dir)
			if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("accepted change\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			run := &runlog.Run{
				ID: "r-commit-progress-" + strings.ReplaceAll(failure, "commit", "failure"), ProjectID: id, TaskID: "T-090", Stage: "build",
				Status: "paused", Verdict: "PASSED", PendingKind: "gate", StartedAt: time.Now().UTC().Format(time.RFC3339),
			}
			w, err := runlog.NewWriter(dir, run)
			if err != nil {
				t.Fatal(err)
			}
			w.Close()
			if err := s.RecoverRuns(context.Background()); err != nil {
				t.Fatal(err)
			}

			// The git shim observes the durable run log at the precise boundaries
			// this UX event promises to precede. It deliberately fails selected
			// operations only after proving the event is already recorded.
			bin := t.TempDir()
			events := filepath.Join(runlog.RunDirFor(dir, run.ID), "events.jsonl")
			shim := "#!/bin/sh\n" +
				"if [ \"$1\" = add ] || [ \"$1\" = commit ]; then\n" +
				"  grep -Fq 'committing accepted work before clean-checkout verification' \"$DUCKLAB_T090_EVENTS\" || exit 97\n" +
				"fi\n" +
				"if [ \"$1\" = \"$DUCKLAB_T090_FAIL\" ]; then exit 98; fi\n" +
				"exec " + realGit + " \"$@\"\n"
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("DUCKLAB_T090_EVENTS", events)
			t.Setenv("DUCKLAB_T090_FAIL", failure)

			_, err = s.RunAccept(context.Background(), run.ID, "")
			if failure == "" && err != nil {
				t.Fatalf("accept: %v", err)
			}
			if failure != "" && err == nil {
				t.Fatalf("accept succeeded despite forced git %s failure", failure)
			}
			detail, getErr := s.RunGet(context.Background(), run.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			progress, committed := -1, -1
			for i, event := range detail.Events {
				if event.Type != "gate_started" {
					continue
				}
				detail, _ := event.Data["detail"].(string)
				if detail == "committing accepted work before clean-checkout verification" {
					progress = i
				}
				if strings.HasPrefix(detail, "committed ") {
					committed = i
				}
			}
			if progress < 0 {
				t.Fatal("commit-progress gate_started event was not recorded")
			}
			if failure == "" && committed < 0 {
				t.Fatal("accepted run did not record its commit")
			}
			if failure == "" && progress >= committed {
				t.Errorf("commit-progress event %d must precede recorded commit %d", progress, committed)
			}
			if failure == "" && mustHead(t, g) != detail.Run.CommitSHA {
				t.Error("accepted commit was not recorded on HEAD")
			}
		})
	}
}

// The final gate is the failure a person reads after a build. Its event must
// carry the same bounded, named result as other gate records, while Failure
// preserves the diagnostic tail for the failed-run card.
func TestFailingFinalGateRecordsBoundedOutputAndFailureTail(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	project, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "gate diagnostics", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	projectID := project.ID
	if err := os.MkdirAll(artifact.DocsDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan), []byte("## M-001 — Gate diagnostics\n\n### T-001 — Preserve the final gate failure\n\nMake the failure visible.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const outputTail = "FINAL-GATE-DIAGNOSTIC: survey-inventory fails only in the full suite"
	gateScript := "#!/bin/sh\nprintf 'prefix:'\nprintf '%05000d' 0 | tr '0' x\nprintf '\\n" + outputTail + "\\n'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "failing-gate.sh"), []byte(gateScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), projectID, map[string]string{
		"verify.mode": "tests", "verify.tests": "./failing-gate.sh",
	}); err != nil {
		t.Fatal(err)
	}

	run, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-001", Mode: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	if rs == nil {
		t.Fatal("started run was not registered")
	}
	select {
	case <-rs.done:
	case <-time.After(15 * time.Second):
		t.Fatal("run did not reach its final gate")
	}

	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Verdict != "FAILED" {
		t.Fatalf("verdict = %q, want FAILED (failure = %q)", detail.Run.Verdict, detail.Run.Failure)
	}
	if !strings.Contains(detail.Run.Failure, outputTail) {
		t.Errorf("Failure = %q, want final gate output tail %q", detail.Run.Failure, outputTail)
	}
	for _, event := range detail.Events {
		if event.Type != "gate" {
			continue
		}
		for _, field := range []string{"gate", "command", "exit_code", "output", "duration_s"} {
			if _, ok := event.Data[field]; !ok {
				t.Errorf("final gate event missing %q: %#v", field, event.Data)
			}
		}
		output, ok := event.Data["output"].(string)
		if !ok {
			t.Errorf("final gate output = %#v, want string", event.Data["output"])
		} else {
			if !strings.Contains(output, outputTail) {
				t.Errorf("final gate output = %q, want its diagnostic tail", output)
			}
			if len(output) > 4003 {
				t.Errorf("final gate output has %d bytes, want bounded tail", len(output))
			}
		}
		return
	}
	t.Error("failing final gate did not record a gate event")
}

func TestAppendToAClosedLogFails(t *testing.T) {
	dir := t.TempDir()
	w, err := runlog.NewWriter(dir, &runlog.Run{ID: "r-x", StartedAt: "now"})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := w.AppendEvent("human", nil); err == nil {
		t.Error("appending to a closed log succeeded; the event went nowhere")
	}
}

// A split run spent its architect's whole turn producing a good decomposition
// and then died inside phase 3 on a raw "fatal: not a git repository". Every
// mode needs a HEAD — to branch from, to diff against, to build a worktree on
// — so finding out first costs nothing and saves the work.
func TestARunRefusesAProjectItCannotWorkIn(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ducklab"), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := s.registry.Register(dir, "no-git")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.RunStart(context.Background(), id, RunRequest{TaskID: "T-001", Mode: "solo"})
	if err == nil {
		t.Fatal("a run started in a directory that is not a repository")
	}
	// The message has to say what to do, not just what is wrong.
	if !strings.Contains(err.Error(), "project init") {
		t.Errorf("the refusal does not say how to fix it: %v", err)
	}

	runs, _ := s.RunList(context.Background(), RunFilter{ProjectID: id})
	if len(runs) != 0 {
		t.Errorf("a run record was created for a project that cannot host one: %d", len(runs))
	}
}

// Forget-and-reopen greeted the next open with an intake proposal whose run
// the engine no longer knew — every decision on it failed. Forget refuses
// while runs are in flight, naming them; and a re-opened project brings its
// runs back without waiting for an engine restart (B-083).
func TestForgetRefusesInFlightRunsAndReopenRecoversThem(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, err := s.registry.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	// ProjectOpen loads the config, which the bare test project lacks.
	if err := os.WriteFile(filepath.Join(entry.Path, ".ducklab", "project.toml"),
		[]byte("schema = 1\nid = \""+projectID+"\"\nname = \"proj\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-gate", ProjectID: projectID, Stage: "intake", Mode: "council",
		Status: "paused", PendingKind: "gate", Verdict: "UNVERIFIED",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(entry.Path, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	err = s.ProjectForget(context.Background(), projectID)
	if err == nil || !strings.Contains(err.Error(), "r-gate") {
		t.Fatalf("forget with a gated run = %v, want a refusal naming it", err)
	}

	// Decide it, forget, and reopen: the runs come back with the project.
	if err := s.RunReject(context.Background(), "r-gate", "abandoned"); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectForget(context.Background(), projectID); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.ProjectOpen(context.Background(), entry.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunGet(context.Background(), "r-gate"); err != nil {
		t.Fatalf("reopened project %s does not know its run: %v", reopened.ID, err)
	}
}
