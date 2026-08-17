package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// The autopilot's whole discipline is knowing which steps are not its to
// take. A fresh project's guide says "describe what you want to build" — a
// human gate — so the loop must idle ON, saying what it needs, launching
// nothing.
func TestTheAutopilotIdlesAtAHumanGate(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")

	st, err := s.AutopilotSet(context.Background(), projectID, true, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !st.On || st.MaxTasks != 5 {
		t.Fatalf("enable = %+v", st)
	}

	// advance runs async with a settle delay; wait for it to conclude.
	deadline := time.Now().Add(3 * time.Second)
	for {
		st = s.AutopilotStatus(projectID)
		if strings.HasPrefix(st.LastAction, "needs you:") || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !st.On {
		t.Errorf("the loop switched off at a human gate instead of idling: %+v", st)
	}
	if !strings.Contains(st.LastAction, "needs you") {
		t.Errorf("last action = %q, want the human gate named", st.LastAction)
	}
	if st.Started != 0 {
		t.Errorf("started %d runs at a human gate", st.Started)
	}
}

// One failure earns one retry; the second consecutive failure is a pattern,
// and patterns are handed to a person: the loop switches off wearing the
// reason.
// A human-only guide step must not block an independent mechanical task.
// The status keeps both queues visible: the person still has to decide about
// the bug, while yolo autonomy starts the ready task below it.
func TestAutopilotSkipsHumanGateToStartMechanicalWork(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, err := s.registry.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(artifact.DocsDir(entry.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	for kind, body := range map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Build it\n\nThe project must build.\n",
		artifact.KindSpec:         "## SPEC-001 — Build it\n\nThe project builds.\n",
		artifact.KindPlan:         "## M-01 — Work\n\n### T-001 — Mechanical work\n\nDo the work.\n",
	} {
		if err := os.WriteFile(artifact.Path(entry.Path, kind), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BugAdd(context.Background(), projectID, BugRequest{Title: "Needs a decision"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(context.Background(), projectID, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AutopilotSet(context.Background(), projectID, true, 5); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		st := s.AutopilotStatus(projectID)
		if st.Started > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	st := s.AutopilotStatus(projectID)
	if !st.On {
		t.Fatalf("autopilot stopped instead of idling at the human gate: %+v", st)
	}
	if st.Started != 1 {
		t.Fatalf("started %d tasks, want the first independent mechanical task", st.Started)
	}
	if !strings.Contains(st.LastAction, "needs you") || !strings.Contains(st.LastAction, "B-001") {
		t.Errorf("last action = %q, want the human decision named", st.LastAction)
	}
	if !strings.Contains(st.LastAction, "started") || !strings.Contains(st.LastAction, "T-001") {
		t.Errorf("last action = %q, want the mechanical task named too", st.LastAction)
	}
}

func TestTwoConsecutiveFailuresStopTheLoop(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	if _, err := s.AutopilotSet(context.Background(), projectID, true, 5); err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{ID: "r-x", ProjectID: projectID}
	s.autopilotOnFail(run)
	if st := s.AutopilotStatus(projectID); !st.On {
		t.Fatalf("one failure switched the loop off: %+v", st)
	}
	s.autopilotOnFail(run)
	st := s.AutopilotStatus(projectID)
	if st.On {
		t.Fatalf("two consecutive failures left the loop on: %+v", st)
	}
	if !strings.Contains(st.StoppedReason, "consecutive failures") {
		t.Errorf("stopped reason = %q, want the failure pattern named", st.StoppedReason)
	}

	// An accept in between resets the count: fail, accept, fail is weather
	// twice, not a pattern.
	if _, err := s.AutopilotSet(context.Background(), projectID, true, 5); err != nil {
		t.Fatal(err)
	}
	s.autopilotOnFail(run)
	s.autopilotOnAccept(run)
	s.autopilotOnFail(run)
	if st := s.AutopilotStatus(projectID); !st.On {
		t.Errorf("an interleaved accept did not reset the failure count: %+v", st)
	}
}

// Off is the default and off means OFF: with the autopilot never enabled,
// the settle hooks are no-ops — guarded behavior is byte-identical with the
// autopilot compiled in.
func TestTheHooksAreInertWhenOff(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")

	run := &runlog.Run{ID: "r-x", ProjectID: projectID}
	s.autopilotOnAccept(run)
	s.autopilotOnFail(run)
	if st := s.AutopilotStatus(projectID); st.On || st.ConsecutiveFails != 0 || st.StoppedReason != "" {
		t.Errorf("hooks moved state while off: %+v", st)
	}
}

// The loop's leash is configuration, not code: the activation cap and the
// failure tolerance come from defaults a person can edit, bounded so a typo
// cannot configure an unsupervised thousand-task loop.
func TestAutopilotDefaultsRoundTripAndBounds(t *testing.T) {
	s := writableService(t, "pato-uno")

	d := s.AutopilotDefaults()
	if d.MaxTasks != autopilotDefaultMaxTasks || d.MaxFails != autopilotDefaultMaxFails {
		t.Fatalf("built-ins = %+v", d)
	}

	if err := s.AutopilotDefaultsSet(AutopilotDefaultsView{MaxTasks: 3, MaxFails: 1, Autonomy: "auto"}); err != nil {
		t.Fatal(err)
	}
	d = s.AutopilotDefaults()
	if d.MaxTasks != 3 || d.MaxFails != 1 || d.Autonomy != "auto" {
		t.Errorf("after set = %+v", d)
	}
	if s.autopilotConfigMaxFails() != 1 {
		t.Error("the driver does not read the configured failure tolerance")
	}

	for _, bad := range []AutopilotDefaultsView{
		{MaxTasks: 0, MaxFails: 2, Autonomy: "guarded"},
		{MaxTasks: 1000, MaxFails: 2, Autonomy: "guarded"},
		{MaxTasks: 5, MaxFails: 0, Autonomy: "guarded"},
		{MaxTasks: 5, MaxFails: 2, Autonomy: "cowboy"},
	} {
		if err := s.AutopilotDefaultsSet(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

// The default modes are a promise to every caller, not just the launcher
// that pre-fills them client-side: a run started with no mode — the CLI, the
// autopilot — gets the configured build mode, not a hardcoded solo. The
// autopilot's first production build ran solo past a config that said pair.
func TestAnEmptyModeTakesTheConfiguredDefault(t *testing.T) {
	s := writableService(t, "pato-uno", "pato-dos")
	v := s.ModeDefaults()
	v.BuildMode = "pair"
	v.TestMode = "pair"
	if err := s.ModeDefaultsSet(v); err != nil {
		t.Fatal(err)
	}
	if got := s.testModeDefault(""); got != "pair" {
		t.Errorf("empty test mode resolved to %q, want the configured pair", got)
	}
	if got := s.testModeDefault("solo"); got != "solo" {
		t.Errorf("an explicit mode was overridden: %q", got)
	}
	s.cfgMu.RLock()
	buildDefault := s.cfg.Defaults.BuildMode
	s.cfgMu.RUnlock()
	if buildDefault != "pair" {
		t.Errorf("build default = %q", buildDefault)
	}
}

// The per-project autonomy — the level runs and triage consult FIRST — had
// no control anywhere: the harness's own guidance was "edit the TOML".
func TestProjectAutonomyRoundTrip(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry0, _ := s.registry.Get(projectID)
	if err := os.MkdirAll(filepath.Join(entry0.Path, ".ducklab"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry0.Path, ".ducklab", "project.toml"),
		[]byte("id = \"proj\"\nname = \"proj\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.ProjectAutonomySet(projectID, "yolo"); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.ProjectAutonomy(projectID); a != "yolo" {
		t.Errorf("autonomy = %q, want yolo", a)
	}
	entry, _ := s.registry.Get(projectID)
	if got := s.triageAutonomy(entry.Path); got != "yolo" {
		t.Errorf("the triage resolver does not see it: %q", got)
	}
	// Empty clears the override back to the default.
	if err := s.ProjectAutonomySet(projectID, ""); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.ProjectAutonomy(projectID); a != "" {
		t.Errorf("clearing left %q", a)
	}
	if err := s.ProjectAutonomySet(projectID, "cowboy"); err == nil {
		t.Error("an invalid autonomy was accepted")
	}
}

// A blind relaunch of a deterministic failure reproduces it exactly. The
// autopilot's retry carries the previous attempt's failure as a note, and
// the note is consumed once — a note for one task must not haunt another.
// A no-changes outcome is evidence about the tree, not a transient failure to
// retry. The autopilot must wait for new human context before asking again.
func TestAutopilotDoesNotRetryNoChangesWithoutHumanNote(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")

	s.runsMu.Lock()
	s.runs["r-empty"] = &runState{run: &runlog.Run{
		ID: "r-empty", ProjectID: projectID, TaskID: "T-044", Stage: "build",
		Status: "done", NoChanges: true, Origin: "autopilot",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}}
	s.runsMu.Unlock()
	if !s.autopilotNoChangesNeedsHumanNote(context.Background(), projectID, "T-044") {
		t.Fatal("autopilot would retry an unchanged task without human context")
	}

	// A later human launch note is the explicit new context that permits another
	// attempt; an autopilot's generated retry note is deliberately not enough.
	s.runsMu.Lock()
	s.runs["r-note"] = &runState{run: &runlog.Run{
		ID: "r-note", ProjectID: projectID, TaskID: "T-044", Stage: "build",
		Status: "failed", Note: "The implementation moved to the new package.",
		StartedAt: time.Now().Add(time.Second).UTC().Format(time.RFC3339),
	}}
	s.runsMu.Unlock()
	if s.autopilotNoChangesNeedsHumanNote(context.Background(), projectID, "T-044") {
		t.Fatal("a later human note did not permit a fresh attempt")
	}
}

func TestTheRetryCarriesTheFailureAsANote(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	if _, err := s.AutopilotSet(context.Background(), projectID, true, 5); err != nil {
		t.Fatal(err)
	}

	s.autopilotOnFail(&runlog.Run{
		ID: "r-old", ProjectID: projectID, TaskID: "T-042",
		Failure: "the gate is still green, so the new test asserts nothing that is not already true",
	})

	s.apMu.Lock()
	st := s.autopilots[projectID]
	task, note := st.retryTask, st.retryNote
	s.apMu.Unlock()
	if task != "T-042" {
		t.Fatalf("retry task = %q", task)
	}
	if !strings.Contains(note, "r-old") || !strings.Contains(note, "still green") {
		t.Errorf("the note does not carry the failure: %q", note)
	}
	if !strings.Contains(note, "do not repeat") {
		t.Errorf("the note does not instruct: %q", note)
	}
}
