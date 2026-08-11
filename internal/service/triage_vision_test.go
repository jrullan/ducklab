package service

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
)

// A screenshot attached to a bug must reach the wire when the triager can
// see. The chain had four links — declared vision, the registry, the gate,
// the runner — and the LAST one dropped the images while the engine warned
// "shown to the triager": the model then truthfully reported the screenshot
// absent, twice, to the person who had attached it (B-036).
func TestATriagersScreenshotReachesTheWire(t *testing.T) {
	var sawImage atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "image_url") {
			sawImage.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"severity\":\"normal\",\"component\":\"ui\",\"reason\":\"seen\",\"task_title\":\"fix\",\"suspected_files\":[]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	isolate(t)
	cfg := config.DefaultGlobal()
	yes := true
	cfg.Providers = map[config.ProviderID]config.Provider{
		"test": {Kind: "openai", BaseURL: srv.URL},
	}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{
		"seer": {Provider: "test", Model: "m", Caps: config.Caps{Vision: &yes}},
	}
	s, err := New(cfg, Options{Bus: bus.New(64)})
	if err != nil {
		t.Fatal(err)
	}

	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	if err := os.MkdirAll(filepath.Join(entry.Path, ".ducklab"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry.Path, ".ducklab", "project.toml"),
		[]byte("id = \"proj\"\nname = \"proj\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b, err := s.BugAdd(ctx, projectID, BugRequest{Title: "broken badge", Severity: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	png, _ := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if _, err := s.BugAttach(ctx, projectID, b.ID, "shot.png", png); err != nil {
		t.Fatal(err)
	}

	run, err := s.BugTriage(ctx, projectID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(ctx, run.ID)

	if !sawImage.Load() {
		data, _ := os.ReadFile(filepath.Join(entry.Path, ".ducklab", "runs", run.ID, "events.jsonl"))
		t.Log(string(data))
		t.Fatal("the attached screenshot never reached the provider request")
	}
}
