package engineclt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineapi"
	"github.com/jrullan/ducklab/internal/service"
)

func writeEngineFiles(t *testing.T, port int, agentToken, humanToken string) {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dir := filepath.Join(state, "ducklab")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	info, _ := json.Marshal(map[string]interface{}{"port": port, "token": agentToken})
	if err := os.WriteFile(filepath.Join(dir, "engine.json"), info, 0o600); err != nil {
		t.Fatal(err)
	}
	if humanToken != "" {
		if err := os.WriteFile(filepath.Join(dir, "human.token"), []byte(humanToken+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// B-530: a reconnect re-reads the SAME kind of credential. Following an
// engine restart through engine.json alone would quietly turn the desktop
// into an agent — and, the other way, must never hand an agent's client the
// person's token.
func TestAReconnectKeepsItsKindOfCredential(t *testing.T) {
	seen := ""
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer live.Close()
	u, _ := url.Parse(live.URL)
	port, _ := strconv.Atoi(u.Port())
	writeEngineFiles(t, port, "agent-2", "human-2")

	for _, c := range []struct {
		human bool
		want  string
	}{{true, "Bearer human-2"}, {false, "Bearer agent-2"}} {
		client := &Client{BaseURL: "http://127.0.0.1:1", Token: "stale", Human: c.human, HTTPClient: http.DefaultClient}
		resp, err := client.do("GET", "/v1/anything", nil)
		if err != nil {
			t.Fatalf("human=%v: did not follow the restart: %v", c.human, err)
		}
		resp.Body.Close()
		if seen != c.want {
			t.Fatalf("human=%v: reconnected with %q, want %q", c.human, seen, c.want)
		}
	}
	if got := NewHuman(&daemon.EngineInfo{Port: port, Token: "agent-2"}); !got.Human || got.Token != "human-2" {
		t.Fatalf("NewHuman = %+v, want the person's token", got)
	}
	if got := New(&daemon.EngineInfo{Port: port, Token: "agent-2"}); got.Human || got.Token != "agent-2" {
		t.Fatalf("New = %+v, want the agent token", got)
	}
}

// Old clients after an engine restart: they read engine.json's `token` the
// way they always did, which is now the agent credential. Everything an
// agent may do keeps working; a person-only door answers with what to do
// instead, not a bare 401.
func TestAnOldClientKeepsWorkingAsAnAgent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	s, err := service.New(config.DefaultGlobal(), service.Options{Bus: bus.New(16), ConfigPath: filepath.Join(root, "config.toml")})
	if err != nil {
		t.Fatal(err)
	}
	api := engineapi.New(s, bus.New(16), "the-persons-token", "test", "")
	api.SetAgentToken("engine-json-token")
	server := httptest.NewServer(api)
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	writeEngineFiles(t, port, "engine-json-token", "the-persons-token")

	info, err := daemon.ReadEngineJSON()
	if err != nil {
		t.Fatal(err)
	}
	old := New(info) // exactly what a pre-split client builds
	if _, err := old.ProjectList(); err != nil {
		t.Fatalf("an old client lost ordinary access after the split: %v", err)
	}
	_, err = old.ApprovalRequestApprove("r-1", "ar-1")
	if err == nil || !strings.Contains(err.Error(), "person's credential") || !strings.Contains(err.Error(), "approval request") {
		t.Fatalf("person-only door for an old client = %v, want a message that says why and what to do", err)
	}
	// And a new client against an engine from before the split (no
	// human.token) still connects with the one token there is.
	os.Remove(filepath.Join(os.Getenv("XDG_STATE_HOME"), "ducklab", "human.token"))
	if got := NewHuman(info); got.Token != "engine-json-token" {
		t.Fatalf("NewHuman without human.token = %q, want engine.json's token", got.Token)
	}
}
