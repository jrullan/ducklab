package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
	"github.com/jrullan/ducklab/internal/verify"
	"github.com/jrullan/ducklab/internal/xplat"
)

// The application process, engine-managed.
//
// Ducklab's cycle used to end at "accepted diffs and a green suite" — and a
// project reached all-tasks-accepted with no way to start, because nothing in
// the loop ever needed it to. The run story makes the running system a
// first-class object: [run] in project.toml says how it starts, the engine
// owns the process the way it owns gates, and the desktop gets a Launch
// button beside the work the launches are for.

// appState is one project's managed application process.
type appState struct {
	cancel     context.CancelFunc
	cmd        *exec.Cmd
	pid        int
	startedAt  time.Time
	logPath    string
	done       chan struct{}
	exitErr    error
	builtSHA   string
	builtAt    time.Time
	stoppedBy  string
	stopSignal string
	// runID is set when the process runs a paused run's worktree rather than
	// the registered tree (B-552); root is the tree it runs in.
	runID string
	root  string
}

// AppStatus is the running-app answer for one project.
type AppStatus struct {
	Configured bool   `json:"configured"`
	Command    string `json:"command,omitempty"`
	Smoke      string `json:"smoke,omitempty"`
	// SmokeCommand and SmokeSource state exactly what the gate will execute;
	// the former may be Command when the explicit Smoke field is absent.
	SmokeCommand     string `json:"smoke_command,omitempty"`
	SmokeSource      string `json:"smoke_source,omitempty"`
	SmokeTimeoutS    int    `json:"smoke_timeout_s"`
	SmokeExpectation string `json:"smoke_expectation"`
	URL              string `json:"url,omitempty"`
	// URLSource says where URL came from: "run.url" when configured, or
	// "output" when the running app printed its own local address (B-553).
	// A launched app with no link left the person guessing the port.
	URLSource string `json:"url_source,omitempty"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	// Health is "healthy", "unhealthy", or empty when no health URL is
	// configured or the app is not running. A process alive and a service
	// answering are different claims, and only the second one is the app.
	Health string `json:"health,omitempty"`
	// Preflight and Requires mirror the config, so the card can say what the
	// environment must provide before the first launch.
	Preflight string `json:"preflight,omitempty"`
	Requires  string `json:"requires,omitempty"`
	// ExitError is how the last managed process ended, when it ended badly —
	// the first thing a person needs when Launch appears to do nothing.
	ExitError string `json:"exit_error,omitempty"`
	// StoppedBy/StopSignal distinguish an intentional stop from a crash.
	StoppedBy  string `json:"stopped_by,omitempty"`
	StopSignal string `json:"stop_signal,omitempty"`
	// LogTail is the end of the app's combined output, for the same reason.
	LogTail string `json:"log_tail,omitempty"`
	// BuiltSHA/BuiltAt identify the source state whose configured build gate
	// ran immediately before launch. They are absent when no build command is
	// configured, so the UI never implies freshness it did not establish.
	BuiltSHA string `json:"built_sha,omitempty"`
	BuiltAt  string `json:"built_at,omitempty"`
	// RunID names the run whose worktree the managed process runs (B-552):
	// a person verifying a paused run's live checks must see that run's
	// change, not main. Empty means the registered project tree. Root is the
	// tree either way, while a process is or was managed.
	RunID string `json:"run_id,omitempty"`
	Root  string `json:"root,omitempty"`
	// Suggestions are inferred by stack providers from authoritative build
	// metadata. They are proposals only; choosing run.command remains a human
	// configuration decision.
	Suggestions []AppSuggestion `json:"suggestions,omitempty"`
}

type AppSuggestion struct {
	Command    string `json:"command"`
	Capability string `json:"capability"`
	Evidence   string `json:"evidence"`
}

// AppStart launches the project's configured run.command as a managed
// process. Output goes to .ducklab/app.log, truncated on each start.
func (s *Service) AppStart(ctx context.Context, projectID string) (*AppStatus, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	return s.appStart(ctx, projectID, appTarget{
		root:    entry.Path,
		logPath: filepath.Join(entry.Path, ".ducklab", "app.log"),
		runEnv:  "manual-" + fmt.Sprint(time.Now().Unix()),
	})
}

// appLaunch is a run's app being prepared: preflight, setup and build run in
// the worktree before the process exists, and a decision must end them too.
type appLaunch struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// appBaselinePath keeps a run's launch baseline with the run, not in memory:
// an engine restart kills the app but leaves what it wrote in the worktree.
func appBaselinePath(projectRoot, runID string) string {
	return filepath.Join(runlog.RunDirFor(projectRoot, runID), "app-baseline.json")
}

// forgetRunApp drops a run's launch baseline: once Accept has checked the
// tree it commits it, and a decided run's worktree is gone. A later launch
// records a fresh baseline from the tree as it then is.
func (s *Service) forgetRunApp(projectRoot, runID string) {
	_ = os.Remove(appBaselinePath(projectRoot, runID))
}

// stagedFingerprint is the worktree fingerprint restricted to what Accept
// would stage: build products, linked dependencies and the configured
// build_products are left out of the commit, so they are not drift either.
func stagedFingerprint(root, projectRoot string, linkedDeps []string) (map[string]string, error) {
	fp, err := vcs.New(root).WorkingFingerprint()
	if err != nil {
		return nil, err
	}
	excluded := append(append([]string(nil), linkedDeps...), landingExclusions(root, projectRoot)...)
	for path := range fp {
		for _, ex := range excluded {
			ex = strings.Trim(filepath.ToSlash(ex), "/")
			if ex != "" && (path == ex || strings.HasPrefix(path, ex+"/")) {
				delete(fp, path)
				break
			}
		}
	}
	return fp, nil
}

// runAppDrift names what changed in a run's worktree since its app was first
// launched there. Called by Accept after stopping the app: the person
// approved a diff, and the app or its build may have written beside it.
func (s *Service) runAppDrift(run *runlog.Run, projectRoot string) error {
	runID, root := run.ID, run.WorktreePath
	raw, err := os.ReadFile(appBaselinePath(projectRoot, runID))
	if os.IsNotExist(err) {
		return nil
	}
	var baseline map[string]string
	if err == nil {
		err = json.Unmarshal(raw, &baseline)
	}
	if err != nil {
		return fmt.Errorf("cannot read the worktree's state from before the app was launched: %w", err)
	}
	now, err := stagedFingerprint(root, projectRoot, run.LinkedDeps)
	if err != nil {
		return fmt.Errorf("cannot compare the worktree with its state before the app was launched: %w", err)
	}
	var changed []string
	for path, sum := range now {
		if baseline[path] != sum {
			changed = append(changed, path)
		}
	}
	for path := range baseline {
		if _, ok := now[path]; !ok {
			changed = append(changed, path)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	sort.Strings(changed)
	if len(changed) > 10 {
		changed = append(changed[:10], fmt.Sprintf("… %d more", len(changed)-10))
	}
	return fmt.Errorf("not accepted — the worktree changed after this run's app was launched, and those changes were never in the reviewed diff: %s. Restore them in %s (or add them to .gitignore) and accept again, or reject", strings.Join(changed, ", "), root)
}

// appTarget is the tree a managed app runs in.
type appTarget struct {
	root       string
	logPath    string
	runEnv     string
	runID      string
	linkedDeps []string
	// stillValid says whether the target may be launched. It takes the
	// run's own locks, so it is called outside appMu; an accept that begins
	// after it answered is caught by appStops instead.
	stillValid func() error
}

// RunAppStart launches the project's app from a paused run's worktree
// (B-552). A run that asks the person to verify something in the live
// environment has to offer that environment: the project's own Launch builds
// the registered tree, which does not contain the change awaiting decision.
//
// The command, build and preflight are the person's registered configuration,
// never the worktree's copy of project.toml. That keeps the run from naming
// the command; it does not sandbox it — run.command typically calls the
// worktree's own scripts, the same code the gate already executed. One
// managed app per project still holds: a run's app and main's would share a
// port. Only a gate pause qualifies: a question or budget pause resumes the
// worker in this tree, and the app would write into its diff.
func (s *Service) RunAppStart(ctx context.Context, runID string) (*AppStatus, error) {
	s.runsMu.RLock()
	rs, ok := s.runs[runID]
	s.runsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	launchable := func() error {
		if kind := rs.snapshotRun().PendingKind; kind != "gate" {
			return fmt.Errorf("not started — run %s is waiting on %s, not on a decision about its change", runID, kind)
		}
		rs.acceptMu.Lock()
		accepting := rs.accepting != nil
		rs.acceptMu.Unlock()
		if accepting {
			return fmt.Errorf("not started — run %s is being accepted; its worktree is being committed", runID)
		}
		run := rs.snapshotRun()
		if run.Status != "paused" || run.Accepted {
			return fmt.Errorf("not started — run %s is %s; only a run waiting for a decision has a change to try", runID, run.Status)
		}
		if inRebase, _ := run.PendingData["rebase_in_progress"].(bool); inRebase {
			return fmt.Errorf("not started — run %s's worktree is mid-rebase; finish resolving it and accept, or reject", runID)
		}
		return nil
	}
	if err := launchable(); err != nil {
		return nil, err
	}
	run := rs.snapshotRun()
	if run.WorktreePath == "" {
		return nil, fmt.Errorf("not started — run %s works in the project tree; Launch app in the project runs its change", runID)
	}
	if info, err := os.Stat(run.WorktreePath); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("not started — run %s's worktree %s is gone", runID, run.WorktreePath)
	}
	entry, err := s.registry.Get(run.ProjectID)
	if err != nil {
		return nil, err
	}
	return s.appStart(ctx, run.ProjectID, appTarget{
		root: run.WorktreePath,
		// The log lives with the run, never in the worktree: a file there
		// would ride into the diff the person is about to accept.
		logPath:    filepath.Join(runlog.RunDirFor(entry.Path, runID), "app.log"),
		runEnv:     runID,
		runID:      runID,
		linkedDeps: run.LinkedDeps,
		stillValid: launchable,
	})
}

// stopRunApp ends the app a run's worktree is serving, if any. Called before
// a decision touches the worktree: the process could write into the tree that
// is being committed, and outlives nothing it was started to show.
func (s *Service) stopRunApp(runID string) {
	s.appMu.Lock()
	if s.appStops == nil {
		s.appStops = map[string]int{}
	}
	s.appStops[runID]++
	if launch := s.appLaunches[runID]; launch != nil {
		s.appMu.Unlock()
		launch.cancel()
		<-launch.done
		s.appMu.Lock()
	}
	var st *appState
	for _, candidate := range s.apps {
		if candidate.runID == runID && candidate.alive() {
			st = candidate
		}
	}
	if st != nil {
		st.stoppedBy = "decision"
		st.stopSignal = "SIGTERM"
	}
	s.appMu.Unlock()
	if st == nil {
		return
	}
	_ = xplat.TerminateProcessGroup(st.cmd)
	select {
	case <-st.done:
		return
	case <-time.After(3 * time.Second):
	}
	s.appMu.Lock()
	st.stopSignal = "SIGKILL"
	s.appMu.Unlock()
	st.cancel()
	select {
	case <-st.done:
	case <-time.After(2 * time.Second):
	}
}

func (s *Service) appStart(ctx context.Context, projectID string, target appTarget) (*AppStatus, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return nil, err
	}
	root := target.root
	// A decision that stops this run's app between the check below and the
	// launch bumps appStops: the launch then refuses rather than serve a
	// tree that is being committed or removed (B-552). The decision also
	// cancels this launch's preflight/setup/build and waits for them, so
	// nothing is still writing into the tree when it is staged.
	s.appMu.Lock()
	stopsSeen := s.appStops[target.runID]
	if target.runID != "" {
		if s.appLaunches[target.runID] != nil {
			s.appMu.Unlock()
			return nil, fmt.Errorf("not started — run %s's app is already being prepared", target.runID)
		}
		launchCtx, cancelLaunch := context.WithCancel(ctx)
		launch := &appLaunch{cancel: cancelLaunch, done: make(chan struct{})}
		if s.appLaunches == nil {
			s.appLaunches = map[string]*appLaunch{}
		}
		s.appLaunches[target.runID] = launch
		ctx = launchCtx
		defer func() {
			s.appMu.Lock()
			delete(s.appLaunches, target.runID)
			s.appMu.Unlock()
			cancelLaunch()
			close(launch.done)
		}()
	}
	s.appMu.Unlock()
	if target.stillValid != nil {
		if err := target.stillValid(); err != nil {
			return nil, err
		}
	}
	if target.runID != "" {
		// The tree as the person is about to see it. Anything the app or its
		// build changes from here would be staged by Accept without having
		// been in the diff anyone reviewed; Accept compares and refuses.
		// The first launch's state is the reviewed one; a relaunch after the
		// person restored the tree must still compare against it.
		baselinePath := appBaselinePath(entry.Path, target.runID)
		if _, statErr := os.Stat(baselinePath); os.IsNotExist(statErr) {
			fp, fpErr := stagedFingerprint(root, entry.Path, target.linkedDeps)
			if fpErr != nil {
				return nil, fmt.Errorf("not started — cannot record the worktree's state before launch: %w", fpErr)
			}
			raw, _ := json.Marshal(fp)
			if err := xplat.AtomicWrite(baselinePath, raw, 0o644); err != nil {
				return nil, fmt.Errorf("not started — cannot record the worktree's state before launch: %w", err)
			}
		}
	}
	if cfg.Run.Command == "" {
		return nil, fmt.Errorf("not started — this project has no run.command; set how the app starts in Projects (project set run.command \"…\")")
	}
	// Do not rebuild under an already-running process. The locked check below
	// remains authoritative for two concurrent starts; this early check avoids
	// the ordinary second click mutating its live binary before being refused.
	s.appMu.Lock()
	running := s.apps[projectID]
	if running != nil && running.alive() {
		pid := running.pid
		s.appMu.Unlock()
		return nil, fmt.Errorf("not started — %s is already running (pid %d); stop it first", running.describe(), pid)
	}
	s.appMu.Unlock()

	// The environment check, before the process: a failed preflight names
	// what is missing in its own words, where a failed launch is a crash to
	// decode from a log tail.
	// PYTHONUNBUFFERED: the log is a file, and a block-buffered Python app
	// (python -m http.server among them) would hold back the very line that
	// says where it is listening until the buffer fills (B-553).
	appEnv := append(os.Environ(), "DUCKLAB_RUN_ID="+target.runEnv, "DUCKLAB_PROJECT_ID="+projectID, "PYTHONUNBUFFERED=1")
	preflightRanBuild := false
	if cfg.Run.Preflight != "" {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out, perr := xplat.ShellContext(pctx, root, appEnv, cfg.Run.Preflight).CombinedOutput()
		if perr != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = perr.Error()
			}
			return nil, fmt.Errorf("not started — the environment is not ready (preflight %q failed): %s", cfg.Run.Preflight, firstN(msg, 400))
		}
		preflightRanBuild = strings.TrimSpace(cfg.Run.Preflight) == strings.TrimSpace(cfg.Verify.Build) && strings.TrimSpace(cfg.Verify.Build) != ""
	}

	// A configured build command is the authoritative way to refresh an app
	// artifact. Run it in the registered project tree immediately before the
	// process starts; otherwise Launch can serve a binary produced by an old
	// checkout even though every accepted run built elsewhere in a worktree.
	builtSHA := ""
	builtAt := time.Time{}
	if strings.TrimSpace(cfg.Verify.Build) != "" {
		if !preflightRanBuild {
			if strings.TrimSpace(cfg.Verify.Setup) != "" {
				setup := cfg.Verify
				setup.Mode = string(verify.GateCustom)
				setup.Custom = cfg.Verify.Setup
				result, runErr := verify.Run(ctx, root, setup, verify.Identity{RunID: "app-setup", ProjectID: projectID})
				if runErr != nil || result.ExitCode != 0 {
					return nil, appBuildError("setup", result, runErr)
				}
			}
			build := cfg.Verify
			build.Mode = string(verify.GateBuild)
			result, runErr := verify.Run(ctx, root, build, verify.Identity{RunID: "app-build", ProjectID: projectID})
			if runErr != nil || result.ExitCode != 0 {
				return nil, appBuildError("build", result, runErr)
			}
		}
		builtAt = time.Now()
		// A run's worktree is its base commit plus uncommitted work: naming
		// HEAD would claim a source state the build did not use.
		if target.runID == "" {
			builtSHA, _ = vcs.New(root).HeadSHA()
		}
	}

	s.appMu.Lock()
	defer s.appMu.Unlock()
	if s.apps == nil {
		s.apps = map[string]*appState{}
	}
	if st := s.apps[projectID]; st != nil && st.alive() {
		return nil, fmt.Errorf("not started — %s is already running (pid %d); stop it first", st.describe(), st.pid)
	}
	if target.runID != "" && s.appStops[target.runID] != stopsSeen {
		return nil, fmt.Errorf("not started — run %s was decided while its app was being prepared", target.runID)
	}

	logPath := target.logPath
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("open app log: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	cmd := xplat.ShellContext(runCtx, root, appEnv, cfg.Run.Command)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		cancel()
		logFile.Close()
		return nil, fmt.Errorf("start %q: %w", cfg.Run.Command, err)
	}

	st := &appState{
		cancel: cancel, cmd: cmd, pid: cmd.Process.Pid,
		startedAt: time.Now(), logPath: logPath,
		done: make(chan struct{}), builtSHA: builtSHA, builtAt: builtAt,
		runID: target.runID, root: root,
	}
	s.apps[projectID] = st
	go func() {
		st.exitErr = cmd.Wait()
		logFile.Close()
		close(st.done)
	}()
	return s.appStatusLocked(projectID, cfg), nil
}

// AppStop politely stops the managed process group, then force-kills it only
// after the configured grace period.
func (s *Service) AppStop(ctx context.Context, projectID string) error {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return err
	}
	cfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return err
	}
	s.appMu.Lock()
	st := s.apps[projectID]
	if st == nil || !st.alive() {
		s.appMu.Unlock()
		return fmt.Errorf("not stopped — no managed app is running for this project")
	}
	st.stoppedBy = "person"
	st.stopSignal = "SIGTERM"
	s.appMu.Unlock()
	if err := xplat.TerminateProcessGroup(st.cmd); err != nil {
		s.appMu.Lock()
		st.stopSignal = "SIGKILL"
		s.appMu.Unlock()
		st.cancel()
	}
	grace := time.Duration(cfg.Run.StopGraceS) * time.Second
	if grace == 0 {
		grace = 3 * time.Second
	}
	select {
	case <-st.done:
		return nil
	case <-time.After(grace):
		s.appMu.Lock()
		st.stopSignal = "SIGKILL"
		s.appMu.Unlock()
		st.cancel()
	}
	select {
	case <-st.done:
	case <-time.After(2 * time.Second):
		return fmt.Errorf("the app did not exit after SIGTERM and SIGKILL; check pid %d by hand", st.pid)
	}
	return nil
}

// AppStatus reports the app's configuration and, when managed here, its life.
func (s *Service) AppStatus(ctx context.Context, projectID string) (*AppStatus, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return nil, err
	}
	s.appMu.Lock()
	out := s.appStatusLocked(projectID, cfg)
	s.appMu.Unlock()
	if !out.Configured {
		candidates, _ := capability.DefaultRegistry().ResolveRunCommands(
			capability.Context{ProjectRoot: entry.Path, Policies: cfg.Capabilities.Policy},
			cfg.Capabilities.Auto, cfg.Capabilities.Enabled, cfg.Capabilities.Disabled,
		)
		seen := map[string]bool{}
		for _, candidate := range candidates {
			if candidate.Command == "" || seen[candidate.Command] {
				continue
			}
			seen[candidate.Command] = true
			out.Suggestions = append(out.Suggestions, AppSuggestion{
				Command: candidate.Command, Capability: candidate.Capability, Evidence: candidate.Evidence,
			})
		}
	}
	return out, nil
}

// appStatusLocked assembles the status. Callers hold appMu.
func (s *Service) appStatusLocked(projectID string, cfg *config.Project) *AppStatus {
	smokeCommand, smokeSource, smokeExpectation, smokeTimeoutS := productSmokeConfig(cfg.Run)
	out := &AppStatus{
		Configured:       cfg.Run.Command != "",
		Command:          cfg.Run.Command,
		Smoke:            cfg.Run.Smoke,
		SmokeCommand:     smokeCommand,
		SmokeSource:      smokeSource,
		SmokeTimeoutS:    smokeTimeoutS,
		SmokeExpectation: smokeExpectation,
		URL:              cfg.Run.URL,
		Preflight:        cfg.Run.Preflight,
		Requires:         cfg.Run.Requires,
	}
	if out.URL != "" {
		out.URLSource = "run.url"
	}
	st := s.apps[projectID]
	if st == nil {
		return out
	}
	out.LogTail = tailFile(st.logPath, 2048)
	out.RunID = st.runID
	out.Root = st.root
	out.BuiltSHA = st.builtSHA
	if !st.builtAt.IsZero() {
		out.BuiltAt = st.builtAt.UTC().Format(time.RFC3339)
	}
	if !st.alive() {
		out.StoppedBy = st.stoppedBy
		out.StopSignal = st.stopSignal
		if st.exitErr != nil && st.stoppedBy == "" {
			out.ExitError = st.exitErr.Error()
		}
		return out
	}
	out.Running = true
	if out.URL == "" {
		if url := detectAppURL(headFile(st.logPath, 16384)); url != "" {
			out.URL, out.URLSource = url, "output"
		}
	}
	out.PID = st.pid
	out.StartedAt = st.startedAt.UTC().Format(time.RFC3339)
	if cfg.Run.Health != "" {
		out.Health = probeHealth(cfg.Run.Health)
	}
	return out
}

func appBuildError(phase string, result *verify.Result, err error) error {
	if err != nil {
		return fmt.Errorf("not started — the configured %s command could not run: %w", phase, err)
	}
	detail := strings.TrimSpace(result.Output)
	if detail == "" {
		detail = fmt.Sprintf("exit %d", result.ExitCode)
	}
	return fmt.Errorf("not started — the configured %s command failed: %s", phase, firstN(detail, 400))
}

func (st *appState) describe() string {
	if st.runID != "" {
		return "run " + st.runID + "'s app"
	}
	return "the project's app"
}

func (st *appState) alive() bool {
	select {
	case <-st.done:
		return false
	default:
		return true
	}
}

// probeHealth asks the app itself. Short timeout: this rides a status request.
func probeHealth(url string) string {
	client := &http.Client{Timeout: 900 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return "unhealthy"
	}
	resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return "healthy"
	}
	return "unhealthy"
}

// localURL finds a local http(s) address in an app's own output: dev
// servers announce where they listen ("Serving HTTP on … (http://127.0.0.1:
// 41666/)", "Local: http://localhost:5173/"). Only loopback and wildcard
// hosts: a URL the app merely mentions elsewhere is not where it serves.
var localURL = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1?\]|\[::\]):\d{2,5}(?:/[^\s)"'<>]*)?`)

// detectAppURL returns the first local address the app printed, with a
// wildcard bind turned into the loopback address a browser can open.
func detectAppURL(output string) string {
	url := localURL.FindString(output)
	if url == "" {
		return ""
	}
	url = strings.Replace(url, "0.0.0.0", "127.0.0.1", 1)
	url = strings.Replace(url, "[::]", "localhost", 1)
	url = strings.Replace(url, "[::1]", "localhost", 1)
	return url
}

// headFile returns up to the first n bytes of a file: where a server says
// where it listens, before request logs push it out of the tail.
func headFile(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, n)
	read, _ := f.Read(buf)
	return string(buf[:read])
}

// tailFile returns the last n bytes of a file, from the first full line.
func tailFile(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	off := info.Size() - n
	if off < 0 {
		off = 0
	}
	buf := make([]byte, info.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && len(buf) == 0 {
		return ""
	}
	return string(buf)
}

// stopAllApps kills every managed app; called on engine shutdown so the
// processes do not outlive the thing that promised to manage them.
func (s *Service) stopAllApps() {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	for _, st := range s.apps {
		if st.alive() {
			st.cancel()
		}
	}
}
