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

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/service"
)

func postChatSwitch(t *testing.T, server *httptest.Server, id, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/runs/"+id+"/chat/consultant", bytes.NewBufferString(body))
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

// B-513: POST /v1/runs/{id}/chat/consultant moves a waiting chat to another
// duckling and returns the record with the new seat; an unknown duckling is
// the caller's mistake (400), a chat that is not waiting is a conflict (409).
func TestChatSwitchRoute(t *testing.T) {
	root := t.TempDir()
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, filepath.Join(root, k))
	}
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{"fake": {Kind: config.ProviderKindOpenAI, BaseURL: "fake://"}}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{"pato": {Provider: "fake", Model: "m"}, "seer": {Provider: "fake", Model: "s"}}
	svc, err := service.New(cfg, service.Options{Bus: bus.New(16), ConfigPath: filepath.Join(root, "config.toml")})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p, err := svc.ProjectInit(context.Background(), service.InitRequest{Path: dir, Name: "TI-36X", GitInit: true, GitName: "Ada", GitEmail: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []*runlog.Run{
		{ID: "r-chat", Stage: "chat", Mode: "solo", Status: "paused", PendingKind: "chat", Note: "chat about ducklab configuration"},
		{ID: "r-ended", Stage: "chat", Mode: "solo", Status: "done", Note: "chat about ducklab configuration"},
	} {
		run.ProjectID = p.ID
		run.Roster = map[string]string{"consultant": "pato"}
		run.StartedAt = "2026-10-09T10:00:00Z"
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

	code, out := postChatSwitch(t, server, "r-chat", `{"duckling":"seer"}`)
	if code != http.StatusOK {
		t.Fatalf("switch = %d %s", code, out)
	}
	var run runlog.Run
	if err := json.Unmarshal([]byte(out), &run); err != nil || run.Roster["consultant"] != "seer" {
		t.Fatalf("switch returned %s (%v)", out, err)
	}
	if code, out := postChatSwitch(t, server, "r-chat", `{"duckling":"nobody"}`); code != http.StatusBadRequest || !strings.Contains(out, "nobody") {
		t.Errorf("unknown duckling = %d %s, want 400 naming it", code, out)
	}
	if code, out := postChatSwitch(t, server, "r-ended", `{"duckling":"seer"}`); code != http.StatusConflict || !strings.Contains(out, "not waiting") {
		t.Errorf("ended chat = %d %s, want 409", code, out)
	}
}
