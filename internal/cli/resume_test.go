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

	"github.com/jrullan/ducklab/internal/daemon"
)

// B-493: `ducklab run resume <id> --note <text>` sends the note; a stray flag
// or a --note without its text is a usage error, never a note-less resume.
func TestParseResumeArgs(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		id, note string
		ok       bool
	}{
		{[]string{"r-1"}, "r-1", "", true},
		{[]string{"r-1", "--note", "2^-9 is 0.001953125"}, "r-1", "2^-9 is 0.001953125", true},
		{[]string{"r-1", "--note"}, "", "", false},
		{[]string{"r-1", "--nite", "x"}, "", "", false},
		{[]string{"--note", "x"}, "", "", false},
		{nil, "", "", false},
	} {
		id, note, ok := parseResumeArgs(tc.args)
		if id != tc.id || note != tc.note || ok != tc.ok {
			t.Errorf("parseResumeArgs(%q) = %q %q %v, want %q %q %v", tc.args, id, note, ok, tc.id, tc.note, tc.ok)
		}
	}
}

func TestRunResumeNoteReachesTheEngine(t *testing.T) {
	const note = "Write the path without a leading slash."
	var received map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/r-bbdk/resume" {
			t.Fatalf("unexpected engine request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		// Refused, so the CLI stops before following the run's stream.
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"stop here"}}`))
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	enginePath, err := daemon.EngineJSONPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(enginePath), 0o755); err != nil {
		t.Fatal(err)
	}
	engine, _ := json.Marshal(daemon.EngineInfo{Port: port, Token: "test"})
	if err := os.WriteFile(enginePath, engine, 0o600); err != nil {
		t.Fatal(err)
	}

	if code := runCmd("resume", []string{"r-bbdk", "--note", note}, t.TempDir()); code != 1 {
		t.Fatalf("exit code = %d, want the engine's refusal (1)", code)
	}
	if received["note"] != note || received["actor"] != "" {
		t.Errorf("resume body = %#v, want the note from a person", received)
	}
}
