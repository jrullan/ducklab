package engineclt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The MCP server is a long-lived process holding a port and token resolved
// once at its own startup. The engine restarted, moved to a new port, and
// every tool the agent had answered "connection refused" until the whole
// gateway was bounced. engine.json is the engine's forwarding address: when
// the dial fails and the file names somewhere new, the client follows.
func TestTheClientFollowsARestartedEngine(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer live.Close()

	// The forwarding address, where daemon.ReadEngineJSON will look.
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	u, _ := url.Parse(live.URL)
	port, _ := strconv.Atoi(u.Port())
	info, _ := json.Marshal(map[string]interface{}{"port": port, "token": "fresh-token"})
	if err := os.MkdirAll(filepath.Join(state, "ducklab"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "ducklab", "engine.json"), info, 0o600); err != nil {
		t.Fatal(err)
	}

	// A client still holding the dead engine's address and token.
	c := &Client{
		BaseURL:    "http://127.0.0.1:1", // nothing listens on port 1
		Token:      "stale-token",
		HTTPClient: http.DefaultClient,
	}
	resp, err := c.do("GET", "/v1/anything", nil)
	if err != nil {
		t.Fatalf("the client never followed the forwarding address: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 from the restarted engine", resp.StatusCode)
	}
	if c.BaseURL != "http://127.0.0.1:"+u.Port() {
		t.Errorf("client still points at the dead engine: %s", c.BaseURL)
	}
}

// The other face of a restart: the OS reused the port, so the dial succeeds
// and the stale token earns a 401 instead of a refused connection.
func TestTheClientRefreshesAStaleToken(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer live.Close()

	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	u, _ := url.Parse(live.URL)
	port, _ := strconv.Atoi(u.Port())
	info, _ := json.Marshal(map[string]interface{}{"port": port, "token": "fresh-token"})
	os.MkdirAll(filepath.Join(state, "ducklab"), 0o755)
	os.WriteFile(filepath.Join(state, "ducklab", "engine.json"), info, 0o600)

	c := &Client{
		BaseURL:    live.URL, // same port — the dial works
		Token:      "stale-token",
		HTTPClient: http.DefaultClient,
	}
	resp, err := c.do("GET", "/v1/anything", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 after refreshing the token", resp.StatusCode)
	}
}
