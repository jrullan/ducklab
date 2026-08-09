package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	// Stopping again is refused honestly.
	if err := s.AppStop(context.Background(), p.ID); err == nil {
		t.Error("a second stop found something to stop")
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
