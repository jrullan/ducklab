package engineapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/service"
)

// resumeServer is an engine with one project holding the paused runs given.
func resumeServer(t *testing.T, runs ...*runlog.Run) (*httptest.Server, *service.Service) {
	t.Helper()
	root := t.TempDir()
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, filepath.Join(root, k))
	}
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{"fake": {Kind: config.ProviderKindOpenAI, BaseURL: "fake://"}}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{"pato": {Provider: "fake", Model: "m"}}
	svc, err := service.New(cfg, service.Options{Bus: bus.New(16), ConfigPath: filepath.Join(root, "config.toml")})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p, err := svc.ProjectInit(context.Background(), service.InitRequest{Path: dir, Name: "TI-36X", GitInit: true, GitName: "Ada", GitEmail: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		run.ProjectID = p.ID
		w, err := runlog.NewWriter(dir, run)
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
	}
	if err := svc.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(svc, bus.New(16), "token", "test", ""))
	t.Cleanup(server.Close)
	return server, svc
}

func postResume(t *testing.T, server *httptest.Server, id, body string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/runs/"+id+"/resume", reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// B-493: POST /resume takes an optional {"note","actor"}. The note and its
// speaker reach the record; no body resumes as it always did; a body that is
// not JSON is refused rather than resumed without the words.
func TestResumeCarriesAnOptionalNoteAndItsActor(t *testing.T) {
	paused := func(id, stage, kind string) *runlog.Run {
		return &runlog.Run{ID: id, TaskID: "T-014", Stage: stage, Mode: "solo", Status: "paused", PendingKind: kind,
			Roster: map[string]string{"implementer": "pato"}, StartedAt: "2026-10-03T14:56:18Z"}
	}
	server, svc := resumeServer(t,
		paused("r-error", "build", "error"),
		paused("r-gate", "build", "gate"),
		paused("r-bad", "build", "error"))

	code, out := postResume(t, server, "r-error", `{"note":"2^-9 is 0.001953125","actor":"mcp:elena"}`)
	if code != http.StatusAccepted {
		t.Fatalf("resume with a note = %d %s", code, out)
	}
	var run runlog.Run
	if err := json.Unmarshal([]byte(out), &run); err != nil {
		t.Fatal(err)
	}
	if len(run.ResumeNotes) != 1 || run.ResumeNotes[0].Note != "2^-9 is 0.001953125" || run.ResumeNotes[0].Actor != "mcp:elena" || run.ResumeNotes[0].PendingKind != "error" {
		t.Errorf("resume notes = %+v", run.ResumeNotes)
	}
	// The resumed run executes against the fake provider; let it settle so the
	// temp dirs are not removed under it.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		d, err := svc.RunGet(context.Background(), "r-error")
		if err == nil && d.Run.Status != "running" && d.Run.Status != "queued" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// No body: today's behaviour — a gate is returned untouched.
	if code, out := postResume(t, server, "r-gate", ""); code != http.StatusAccepted || !strings.Contains(out, `"pending_kind":"gate"`) {
		t.Errorf("resume without a body = %d %s", code, out)
	}
	// The note reaches the service, which refuses to drop it at a gate.
	if code, out := postResume(t, server, "r-gate", `{"note":"fix the path"}`); code != http.StatusConflict || !strings.Contains(out, "waits at its gate") {
		t.Errorf("a note at a gate = %d %s, want 409 naming the gate", code, out)
	}
	if code, out := postResume(t, server, "r-bad", `fix the path`); code != http.StatusBadRequest {
		t.Errorf("a malformed body = %d %s, want 400", code, out)
	}
	if d, _ := svc.RunGet(context.Background(), "r-bad"); d == nil || d.Run.Status != "paused" {
		t.Error("a malformed resume body resumed the run anyway")
	}
}
