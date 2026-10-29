package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jrullan/ducklab/internal/engineclt"
)

// B-530: the CLI presents the person's credential only at an interactive
// terminal; piped — the way an agent's shell tool runs it — it presents the
// agent credential. `ducklab mcp serve` is an agent channel even when a
// person started it from a terminal.
func TestTheCLIPicksItsCredentialByWhoIsAtIt(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer engine.Close()
	u, _ := url.Parse(engine.URL)
	port, _ := strconv.Atoi(u.Port())
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dir := filepath.Join(state, "ducklab")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	info, _ := json.Marshal(map[string]interface{}{"port": port, "token": "agent-token", "pid": os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "engine.json"), info, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "human.token"), []byte("human-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := interactiveTerminal
	t.Cleanup(func() { interactiveTerminal = saved })

	for _, c := range []struct {
		interactive bool
		want        string
	}{{true, "human-token"}, {false, "agent-token"}} {
		interactiveTerminal = func() bool { return c.interactive }
		client, err := discoverEngine(true)
		if err != nil {
			t.Fatal(err)
		}
		if client.Token != c.want || client.Human != c.interactive {
			t.Fatalf("interactive=%v: CLI presents %q (human=%v), want %q", c.interactive, client.Token, client.Human, c.want)
		}
	}

	interactiveTerminal = func() bool { return true }
	client, err := discoverAgentEngine(true)
	if err != nil {
		t.Fatal(err)
	}
	if client.Token != "agent-token" || client.Human {
		t.Fatalf("mcp serve from a terminal presents %q (human=%v), want the agent credential", client.Token, client.Human)
	}

	// B-550: an agent's own token outranks a terminal that looks like the
	// person's, for the CLI and for mcp serve alike.
	t.Setenv("DUCKLAB_TOKEN", "dlk_own")
	for name, discover := range map[string]func(bool) (*engineclt.Client, error){
		"cli": discoverEngine, "mcp serve": discoverAgentEngine,
	} {
		client, err := discover(true)
		if err != nil {
			t.Fatal(err)
		}
		if client.Token != "dlk_own" || client.Human {
			t.Fatalf("%s with DUCKLAB_TOKEN presents %q (human=%v), want the agent's own token", name, client.Token, client.Human)
		}
	}
}
