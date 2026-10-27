package engineapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/service"
)

// heldAcceptProject is a project whose clean-checkout gate the test holds:
// the gate writes the started FIFO (blocking until the test reads it), then
// blocks reading the release FIFO. Rendezvous only — no clocks (B-529).
func heldAcceptProject(t *testing.T, runID string) (s *service.Service, project, started, release string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the held gate rendezvous uses POSIX FIFOs")
	}
	s, projectID, project := landedResolutionService(t)
	fifos := t.TempDir()
	started, release = filepath.Join(fifos, "started"), filepath.Join(fifos, "release")
	for _, p := range []string{started, release} {
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(project, ".ducklab", "project.toml")
	cfg, err := config.LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Verify.Mode = "custom"
	cfg.Verify.Custom = fmt.Sprintf("echo started > %s; cat %s > /dev/null", started, release)
	if err := config.SaveProject(path, cfg); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "held gate"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = project
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	// Paused at its gate with nothing left to commit: accept still reproduces
	// the gate from a clean checkout of HEAD before it settles.
	w, err := runlog.NewWriter(project, &runlog.Run{
		ID: runID, ProjectID: projectID, TaskID: "T-001", Stage: "build", Mode: "solo",
		Status: "paused", PendingKind: "gate", Verdict: "PASSED", StartedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, project, started, release
}

func postAccept(ctx context.Context, url, runID string) (*http.Response, error) {
	body, _ := json.Marshal(map[string]string{"message": "suite passes out of band", "actor": "mcp:test"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/runs/"+runID+"/accept", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer token")
	return http.DefaultClient.Do(req)
}

func landAfterRelease(t *testing.T, s *service.Service, runID, release string) *service.AcceptResult {
	t.Helper()
	done := make(chan struct{})
	var res *service.AcceptResult
	var err error
	go func() {
		defer close(done)
		// Joins the accept already in flight and waits for its outcome.
		res, err = s.RunAccept(context.Background(), runID, "")
	}()
	fifoStep(t, "the held gate being released", func() error {
		return os.WriteFile(release, []byte("go\n"), 0o600)
	})
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the accept never settled after its gate was released")
	}
	if err != nil || res == nil || res.CommitSHA == "" {
		t.Fatalf("accept did not land: %+v, %v", res, err)
	}
	return res
}

// fifoStep runs one blocking FIFO rendezvous under a failure bound — not a
// synchronization clock: a broken accept fails here by name instead of
// hanging the package until go test's own timeout.
func fifoStep(t *testing.T, what string, step func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- step() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("%s never happened", what)
	}
}

func awaitGateStart(t *testing.T, started string) {
	t.Helper()
	fifoStep(t, "the gate replay starting", func() error {
		_, err := os.ReadFile(started)
		return err
	})
}

func assertNoInterruptedGate(t *testing.T, s *service.Service, runID string) {
	t.Helper()
	detail, err := s.RunGet(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Run.Accepted || detail.Run.Status != "done" {
		t.Fatalf("run after landing: accepted=%v status=%s", detail.Run.Accepted, detail.Run.Status)
	}
	for _, e := range detail.Events {
		if e.Type == "human_needed" || e.Type == "accept_refused" {
			t.Errorf("%s recorded although the gate replay finished green: %v", e.Type, e.Data)
		}
		if e.Type == "gate_reproduced" && fmt.Sprint(e.Data["exit_code"]) == "-1" {
			t.Errorf("exit -1 reproduction recorded: %v", e.Data)
		}
	}
}

// B-529, at the HTTP door: the MCP client gave up at 30 s while the gate was
// replaying, the request context was cancelled, and the replay was killed and
// recorded as an exit -1 gate for a person to decide. A caller that leaves
// must only stop waiting.
func TestAcceptSurvivesItsHTTPClientLeaving(t *testing.T) {
	s, _, started, release := heldAcceptProject(t, "r-b529-http-leave")
	server := httptest.NewServer(New(s, bus.New(16), "token", "test", ""))
	t.Cleanup(server.Close)

	caller, hangUp := context.WithCancel(context.Background())
	answered := make(chan error, 1)
	go func() {
		resp, err := postAccept(caller, server.URL, "r-b529-http-leave")
		if resp != nil {
			resp.Body.Close()
		}
		answered <- err
	}()
	awaitGateStart(t, started)
	hangUp()
	var err error
	select {
	case err = <-answered:
	case <-time.After(60 * time.Second):
		t.Fatal("the client's cancellation never returned")
	}
	if err == nil {
		t.Fatal("the cancelled request still received an answer; the test did not model a client leaving")
	}
	landAfterRelease(t, s, "r-b529-http-leave", release)
	assertNoInterruptedGate(t, s, "r-b529-http-leave")
}

// The door's contract: past its bounded wait it answers in_progress, in
// words, instead of holding the connection until a client timeout cuts it.
func TestAcceptAnswersInProgressPastItsBoundedWait(t *testing.T) {
	s, _, started, release := heldAcceptProject(t, "r-b529-http-wait")
	api := New(s, bus.New(16), "token", "test", "")
	// The gate is held until release below, so any wait expires first.
	api.acceptWait = time.Millisecond
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)

	type answer struct {
		status int
		body   map[string]interface{}
		err    error
	}
	answered := make(chan answer, 1)
	go func() {
		resp, err := postAccept(context.Background(), server.URL, "r-b529-http-wait")
		if err != nil {
			answered <- answer{err: err}
			return
		}
		defer resp.Body.Close()
		var body map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&body)
		answered <- answer{resp.StatusCode, body, err}
	}()
	awaitGateStart(t, started)
	var got answer
	select {
	case got = <-answered:
	case <-time.After(60 * time.Second):
		// Failure bound only: the held gate cannot finish, so a correct door
		// has already answered by its bounded wait.
		t.Fatal("the accept door held the request instead of answering in_progress")
	}
	if got.err != nil || got.status != http.StatusOK {
		t.Fatalf("accept answer: status %d, %v", got.status, got.err)
	}
	if got.body["in_progress"] != true || got.body["commit_sha"] != "" {
		t.Fatalf("accept answer = %v; want in_progress with no commit", got.body)
	}
	if info, _ := got.body["info"].(string); !strings.Contains(info, "accept_settled") {
		t.Fatalf("in-progress answer does not say where the outcome arrives: %q", info)
	}
	res := landAfterRelease(t, s, "r-b529-http-wait", release)
	assertNoInterruptedGate(t, s, "r-b529-http-wait")
	detail, _ := s.RunGet(context.Background(), "r-b529-http-wait")
	if detail.Run.LastAccept["outcome"] != "accepted" || detail.Run.LastAccept["commit_sha"] != res.CommitSHA {
		t.Fatalf("last_accept = %v, want accepted %s", detail.Run.LastAccept, res.CommitSHA)
	}
}
