package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/vcs"
)

// The lesson that forced this feature: a project reached all-tasks-accepted
// with 161 green tests and could not start, because nothing in the loop ever
// needed it to. The engine now owns the app process the way it owns gates.
func TestTheEngineStartsProbesAndStopsTheApp(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}

	// Unconfigured: the status says so, and start refuses with the fix named.
	st, err := s.AppStatus(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Configured || st.Running {
		t.Fatalf("a fresh project claims an app: %+v", st)
	}
	if _, err := s.AppStart(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "run.command") {
		t.Fatalf("start without config did not name the missing key: %v", err)
	}

	// A stand-in for the app's own health endpoint.
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer health.Close()

	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"run.command": "echo booted && sleep 30",
		"run.url":     "http://localhost:9999",
		"run.health":  health.URL,
	}); err != nil {
		t.Fatal(err)
	}

	started, err := s.AppStart(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !started.Running || started.PID == 0 {
		t.Fatalf("started app not running: %+v", started)
	}
	// A second start is refused while the first lives.
	if _, err := s.AppStart(context.Background(), p.ID); err == nil {
		t.Fatal("a second start was allowed beside a live app")
	}

	st, err = s.AppStatus(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Health != "healthy" {
		t.Errorf("health = %q, want healthy — the probe asked the app itself", st.Health)
	}
	// The log carries the app's own output.
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(st.LogTail, "booted") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		st, _ = s.AppStatus(context.Background(), p.ID)
	}
	if !strings.Contains(st.LogTail, "booted") {
		t.Errorf("log tail missing the app's output: %q", st.LogTail)
	}

	if err := s.AppStop(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	st, _ = s.AppStatus(context.Background(), p.ID)
	if st.Running {
		t.Error("stopped app still reports running")
	}
	if st.StoppedBy != "person" || st.ExitError != "" {
		t.Errorf("intentional stop reported as a crash: %+v", st)
	}
	// Stopping again is refused honestly.
	if err := s.AppStop(context.Background(), p.ID); err == nil {
		t.Error("a second stop found something to stop")
	}
}

func TestAppStopGivesTheProcessAGracefulShutdownWindow(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "stopped.marker")
	ready := filepath.Join(dir, "ready.marker")
	command := "trap 'printf stopped > " + marker + "; exit 0' TERM; printf ready > " + ready + "; while :; do sleep 0.05; done"
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"run.command": command, "run.stop_grace_s": "2",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppStart(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("app did not reach its ready point")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.AppStop(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("SIGTERM cleanup did not run: %v", err)
	}
	st, err := s.AppStatus(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.StoppedBy != "person" || st.StopSignal != "SIGTERM" || st.ExitError != "" {
		t.Fatalf("graceful stop status = %+v", st)
	}
}

func TestAppStatusSuggestsButDoesNotAdoptDetectedRunCommands(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meson.build"), []byte("project('fixture', 'c')\nexecutable('fixture-app', 'main.c')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	status, err := s.AppStatus(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Configured || status.Command != "" {
		t.Fatalf("detection silently configured the app: %+v", status)
	}
	if len(status.Suggestions) != 1 || status.Suggestions[0].Command != "./build/fixture-app" || status.Suggestions[0].Capability != "meson" {
		t.Fatalf("run suggestions = %+v", status.Suggestions)
	}
	findings, err := s.ConfigDoctor(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range findings {
		if finding.Key == "run.command" && finding.Proposed == "./build/fixture-app" && strings.Contains(finding.Reason, "meson.build") {
			found = true
		}
	}
	if !found {
		t.Fatalf("doctor did not propose the detected run.command: %+v", findings)
	}
}

func TestAppStatusNamesTheEffectiveProductSmoke(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: t.TempDir(), Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"run.command": "./interactive-ui", "run.smoke_timeout_s": "9",
	}); err != nil {
		t.Fatal(err)
	}
	status, err := s.AppStatus(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.SmokeCommand != "./interactive-ui" || status.SmokeSource != "run.command" || status.SmokeExpectation != "live" || status.SmokeTimeoutS != 9 {
		t.Fatalf("command fallback status = %+v", status)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{"run.smoke": "./interactive-ui --headless"}); err != nil {
		t.Fatal(err)
	}
	status, err = s.AppStatus(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.SmokeCommand != "./interactive-ui --headless" || status.SmokeSource != "run.smoke" || status.SmokeExpectation != "exit" {
		t.Fatalf("explicit smoke status = %+v", status)
	}
}

// A command that dies on its own leaves its exit and its last words on the
// status — the first thing a person needs when Launch appears to do nothing.
func TestACrashedAppReportsItsExit(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"run.command": "echo cannot bind port && exit 3",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppStart(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	var st *AppStatus
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st, _ = s.AppStatus(context.Background(), p.ID)
		if st != nil && !st.Running && st.ExitError != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st == nil || st.Running {
		t.Fatal("the crashed app still reports running")
	}
	if !strings.Contains(st.ExitError, "3") {
		t.Errorf("exit error does not carry the code: %q", st.ExitError)
	}
	if !strings.Contains(st.LogTail, "cannot bind port") {
		t.Errorf("log tail missing the app's last words: %q", st.LogTail)
	}
}

// The environment check runs before the process: a failed preflight names
// what is missing in its own words — "postgres is not running" — where a
// failed launch is a crash to decode from a log tail. The environment is the
// person's; this is the engine making its edge visible.
func TestPreflightGuardsTheLaunch(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"run.command":   "sleep 30",
		"run.preflight": "echo postgres is not running on :55433 && false",
		"run.requires":  "PostgreSQL on :55433; database exercise_tracker created",
	}); err != nil {
		t.Fatal(err)
	}

	_, err = s.AppStart(context.Background(), p.ID)
	if err == nil {
		t.Fatal("the launch proceeded over a failed preflight")
	}
	for _, want := range []string{"not started", "environment is not ready", "postgres is not running"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}

	// The checklist rides the status for the card to show.
	st, err := s.AppStatus(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Requires, "PostgreSQL") {
		t.Errorf("status does not carry the requires checklist: %+v", st)
	}

	// A passing preflight lets the launch through.
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"run.preflight": "true",
	}); err != nil {
		t.Fatal(err)
	}
	started, err := s.AppStart(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !started.Running {
		t.Fatal("a green preflight did not start the app")
	}
	if err := s.AppStop(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAppLaunchBuildsCurrentHeadBeforeStarting(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"verify.build": "printf fresh > app.bin",
		"run.command":  "test \"$(cat app.bin)\" = fresh && sleep 30",
	}); err != nil {
		t.Fatal(err)
	}

	started, err := s.AppStart(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.AppStop(context.Background(), p.ID) //nolint:errcheck
	wantSHA, err := vcs.New(dir).HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if started.BuiltSHA != wantSHA || started.BuiltAt == "" {
		t.Fatalf("launch build provenance = sha %q at %q, want HEAD %q", started.BuiltSHA, started.BuiltAt, wantSHA)
	}
	if body, err := os.ReadFile(filepath.Join(dir, "app.bin")); err != nil || string(body) != "fresh" {
		t.Fatalf("launch did not build the current tree: body=%q err=%v", body, err)
	}
}

func TestAppLaunchRefusesAFailedBuild(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"verify.build": "echo compiler rejected main.c && false",
		"run.command":  "sleep 30",
	}); err != nil {
		t.Fatal(err)
	}

	_, err = s.AppStart(context.Background(), p.ID)
	if err == nil || !strings.Contains(err.Error(), "configured build command failed") || !strings.Contains(err.Error(), "compiler rejected main.c") {
		t.Fatalf("failed build launch error = %v", err)
	}
	st, statusErr := s.AppStatus(context.Background(), p.ID)
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if st.Running {
		t.Fatal("app started despite a failed build")
	}
}

func TestAppLaunchDoesNotRepeatABuildUsedAsPreflight(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	command := "printf built\\n >> build-count"
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"verify.build":  command,
		"run.preflight": command,
		"run.command":   "sleep 30",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppStart(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	defer s.AppStop(context.Background(), p.ID) //nolint:errcheck
	body, err := os.ReadFile(filepath.Join(dir, "build-count"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(body), "built"); got != 1 {
		t.Fatalf("shared preflight/build command ran %d times, want once", got)
	}
}

// B-552: a paused run that asks the person to verify something live has to
// offer that environment. Launch for the run serves its worktree — with the
// person's registered command, never the worktree's copy — keeps its log out
// of the diff, shares the project's one-app slot, and ends with the decision.
func TestARunsAppServesItsWorktreeUntilTheDecision(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{
		"run.command": "pwd; echo registered; sleep 30",
	}); err != nil {
		t.Fatal(err)
	}
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-live-check")
	// The run under review rewrites its own copy of the config; the launch
	// must not obey it.
	wtConfig := filepath.Join(run.WorktreePath, ".ducklab", "project.toml")
	if err := os.MkdirAll(filepath.Dir(wtConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wtConfig, []byte("[run]\n  command = \"echo hijacked; sleep 30\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := s.RunAppStart(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.RunID != run.ID || st.Root != run.WorktreePath {
		t.Fatalf("run app status = %+v, want running in %s for %s", st, run.WorktreePath, run.ID)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(st.LogTail, "registered") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		st, _ = s.AppStatus(context.Background(), id)
	}
	if !strings.Contains(st.LogTail, "registered") || strings.Contains(st.LogTail, "hijacked") {
		t.Fatalf("the launch did not run the registered command: %q", st.LogTail)
	}
	realWT, _ := filepath.EvalSymlinks(run.WorktreePath)
	if !strings.Contains(st.LogTail, run.WorktreePath) && !strings.Contains(st.LogTail, realWT) {
		t.Fatalf("the app did not run in the worktree: %q", st.LogTail)
	}
	if _, err := os.Stat(filepath.Join(run.WorktreePath, ".ducklab", "app.log")); err == nil {
		t.Fatal("the run's app log was written into the worktree, where it would ride into the diff")
	}
	// One app per project: main's launch is refused while the run's lives,
	// and says whose app holds the slot.
	if _, err := s.AppStart(context.Background(), id); err == nil || !strings.Contains(err.Error(), run.ID) {
		t.Fatalf("project launch beside the run's app = %v, want refused naming the run", err)
	}

	if err := s.RunReject(context.Background(), run.ID, "not this one"); err != nil {
		t.Fatal(err)
	}
	st, _ = s.AppStatus(context.Background(), id)
	if st.Running || st.StoppedBy != "decision" {
		t.Fatalf("the run's app outlived the decision: %+v", st)
	}
	if _, err := s.RunAppStart(context.Background(), run.ID); err == nil {
		t.Fatal("a decided run's app was launched")
	}
}

// A run that works in the project tree has no separate change to serve, and
// a run that is not waiting for a decision has nothing to try.
func TestARunsAppNeedsAPausedWorktreeRun(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"run.command": "sleep 30"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAppStart(context.Background(), "r-missing"); err == nil {
		t.Fatal("an unknown run's app was launched")
	}
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-not-paused")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	rs.wmu.Lock()
	rs.run.Status = "running"
	rs.wmu.Unlock()
	if _, err := s.RunAppStart(context.Background(), run.ID); err == nil || !strings.Contains(err.Error(), "waiting for a decision") {
		t.Fatalf("a running run's app = %v, want refused", err)
	}
	rs.wmu.Lock()
	rs.run.Status = "paused"
	rs.run.WorktreePath = ""
	rs.wmu.Unlock()
	if _, err := s.RunAppStart(context.Background(), run.ID); err == nil || !strings.Contains(err.Error(), "project tree") {
		t.Fatalf("an in-place run's app = %v, want pointed at the project's launch", err)
	}
}

// Accept commits the worktree the run's app is serving. What the app wrote
// there was never in the reviewed diff: Accept stops the app, names the
// files, and refuses until the tree is back to what the person reviewed.
func TestAcceptRefusesWhatTheRunsAppWroteIntoItsWorktree(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{
		"run.command": "echo rows > appdata.db; sleep 30",
	}); err != nil {
		t.Fatal(err)
	}
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-live-accept")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "change.txt"), []byte("the change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAppStart(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(run.WorktreePath, "appdata.db")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(db); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, err := s.RunAccept(context.Background(), run.ID, "")
	if err == nil || !strings.Contains(err.Error(), "appdata.db") || strings.Contains(err.Error(), "change.txt") {
		t.Fatalf("accept over the app's own writes = %v, want refused naming appdata.db only", err)
	}
	st, _ := s.AppStatus(context.Background(), id)
	if st.Running || st.StoppedBy != "decision" {
		t.Fatalf("the run's app survived the accept: %+v", st)
	}

	if err := os.Remove(db); err != nil {
		t.Fatal(err)
	}
	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", dir, "show", "--name-only", "--format=", result.CommitSHA).Output()
	if err != nil {
		t.Fatal(err)
	}
	if files := strings.Fields(string(out)); len(files) != 1 || files[0] != "change.txt" {
		t.Fatalf("accepted files = %v, want only the run's change", files)
	}
}

// A decision during the launch's build ends the build too, and the launch is
// refused: nothing may still be writing into a tree being committed or removed.
func TestADecisionEndsARunAppStillBuilding(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{
		"verify.build": "sleep 30",
		"run.command":  "sleep 30",
	}); err != nil {
		t.Fatal(err)
	}
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-live-build")
	launched := make(chan error, 1)
	go func() {
		_, err := s.RunAppStart(context.Background(), run.ID)
		launched <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.appMu.Lock()
		building := s.appLaunches[run.ID] != nil
		s.appMu.Unlock()
		if building {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the launch never started building")
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	if err := s.RunReject(context.Background(), run.ID, "no"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("reject waited %v on the build", took)
	}
	select {
	case err := <-launched:
		if err == nil {
			t.Fatal("a launch survived the decision that ended its build")
		}
	default:
		t.Fatal("the reject returned while the launch was still building")
	}
	if st, _ := s.AppStatus(context.Background(), id); st.Running {
		t.Fatalf("an app runs for a rejected run: %+v", st)
	}
}

// Only a gate pause is a decision about the change; a question pause resumes
// the worker in this tree.
func TestARunsAppNeedsAGatePause(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"run.command": "sleep 30"}); err != nil {
		t.Fatal(err)
	}
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-question")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	rs.wmu.Lock()
	rs.run.PendingKind = "question"
	rs.wmu.Unlock()
	if _, err := s.RunAppStart(context.Background(), run.ID); err == nil || !strings.Contains(err.Error(), "question") {
		t.Fatalf("a question pause's app = %v, want refused", err)
	}
}

// What Accept would never stage is not drift: the app's own build output in
// a configured build_products path leaves the accept alone (review of B-552).
func TestRunAppBuildProductsAreNotDrift(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{
		"verify.build_products": "dist",
		"run.command":           "mkdir -p dist && echo bundle > dist/app.js; sleep 30",
	}); err != nil {
		t.Fatal(err)
	}
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-live-products")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "change.txt"), []byte("the change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAppStart(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(run.WorktreePath, "dist", "app.js")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(bundle); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(bundle); err != nil {
		t.Fatal("the app never wrote its build product")
	}
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatalf("accept refused over an excluded build product: %v", err)
	}
}

// B-553: an app with no run.url still has an address — the one it printed.
// The status names it, and where it came from, while the app runs.
func TestAppStatusFindsTheAddressTheAppPrinted(t *testing.T) {
	cases := map[string]string{
		"Serving HTTP on 127.0.0.1 port 41666 (http://127.0.0.1:41666/) ...": "http://127.0.0.1:41666/",
		"  ➜  Local:   http://localhost:5173/\n  ➜  Network: use --host":       "http://localhost:5173/",
		"Running on http://0.0.0.0:8000 (Press CTRL+C to quit)":              "http://127.0.0.1:8000",
		"docs at https://example.com:8443/guide; no local server":            "",
	}
	for output, want := range cases {
		if got := detectAppURL(output); got != want {
			t.Errorf("detectAppURL(%q) = %q, want %q", output, got, want)
		}
	}

	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	// Python, block-buffered into a file, is the case that hid the line.
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"run.command": `python3 -c "import time; print('Serving HTTP on 127.0.0.1 port 41666 (http://127.0.0.1:41666/) ...'); time.sleep(30)"`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppStart(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	defer s.AppStop(context.Background(), p.ID) //nolint:errcheck
	deadline := time.Now().Add(5 * time.Second)
	var st *AppStatus
	for time.Now().Before(deadline) {
		st, _ = s.AppStatus(context.Background(), p.ID)
		if st.URL != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st.URL != "http://127.0.0.1:41666/" || st.URLSource != "output" {
		t.Fatalf("status url = %q from %q, want the printed address from output", st.URL, st.URLSource)
	}
}
