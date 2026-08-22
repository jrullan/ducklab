package service

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/config"
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
	cancel    context.CancelFunc
	pid       int
	startedAt time.Time
	logPath   string
	done      chan struct{}
	exitErr   error
}

// AppStatus is the running-app answer for one project.
type AppStatus struct {
	Configured bool   `json:"configured"`
	Command    string `json:"command,omitempty"`
	URL        string `json:"url,omitempty"`
	Running    bool   `json:"running"`
	PID        int    `json:"pid,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
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
	// LogTail is the end of the app's combined output, for the same reason.
	LogTail string `json:"log_tail,omitempty"`
}

// AppStart launches the project's configured run.command as a managed
// process. Output goes to .ducklab/app.log, truncated on each start.
func (s *Service) AppStart(ctx context.Context, projectID string) (*AppStatus, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return nil, err
	}
	if cfg.Run.Command == "" {
		return nil, fmt.Errorf("not started — this project has no run.command; set how the app starts in Projects (project set run.command \"…\")")
	}

	// The environment check, before the process: a failed preflight names
	// what is missing in its own words, where a failed launch is a crash to
	// decode from a log tail.
	appEnv := append(os.Environ(), "DUCKLAB_RUN_ID=manual-"+fmt.Sprint(time.Now().Unix()), "DUCKLAB_PROJECT_ID="+projectID)
	if cfg.Run.Preflight != "" {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out, perr := xplat.ShellContext(pctx, entry.Path, appEnv, cfg.Run.Preflight).CombinedOutput()
		if perr != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = perr.Error()
			}
			return nil, fmt.Errorf("not started — the environment is not ready (preflight %q failed): %s", cfg.Run.Preflight, firstN(msg, 400))
		}
	}

	s.appMu.Lock()
	defer s.appMu.Unlock()
	if s.apps == nil {
		s.apps = map[string]*appState{}
	}
	if st := s.apps[projectID]; st != nil && st.alive() {
		return nil, fmt.Errorf("not started — the app is already running (pid %d); stop it first", st.pid)
	}

	logPath := filepath.Join(entry.Path, ".ducklab", "app.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("open app log: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	cmd := xplat.ShellContext(runCtx, entry.Path, appEnv, cfg.Run.Command)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		cancel()
		logFile.Close()
		return nil, fmt.Errorf("start %q: %w", cfg.Run.Command, err)
	}

	st := &appState{
		cancel: cancel, pid: cmd.Process.Pid,
		startedAt: time.Now(), logPath: logPath,
		done: make(chan struct{}),
	}
	s.apps[projectID] = st
	go func() {
		st.exitErr = cmd.Wait()
		logFile.Close()
		close(st.done)
	}()
	return s.appStatusLocked(projectID, cfg), nil
}

// AppStop kills the managed process — the whole group, so a shell's children
// die with it.
func (s *Service) AppStop(ctx context.Context, projectID string) error {
	s.appMu.Lock()
	st := s.apps[projectID]
	s.appMu.Unlock()
	if st == nil || !st.alive() {
		return fmt.Errorf("not stopped — no managed app is running for this project")
	}
	st.cancel()
	select {
	case <-st.done:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("the app did not exit within 5s of the kill; check pid %d by hand", st.pid)
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
	defer s.appMu.Unlock()
	return s.appStatusLocked(projectID, cfg), nil
}

// appStatusLocked assembles the status. Callers hold appMu.
func (s *Service) appStatusLocked(projectID string, cfg *config.Project) *AppStatus {
	out := &AppStatus{
		Configured: cfg.Run.Command != "",
		Command:    cfg.Run.Command,
		URL:        cfg.Run.URL,
		Preflight:  cfg.Run.Preflight,
		Requires:   cfg.Run.Requires,
	}
	st := s.apps[projectID]
	if st == nil {
		return out
	}
	out.LogTail = tailFile(st.logPath, 2048)
	if !st.alive() {
		if st.exitErr != nil {
			out.ExitError = st.exitErr.Error()
		}
		return out
	}
	out.Running = true
	out.PID = st.pid
	out.StartedAt = st.startedAt.UTC().Format(time.RFC3339)
	if cfg.Run.Health != "" {
		out.Health = probeHealth(cfg.Run.Health)
	}
	return out
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
