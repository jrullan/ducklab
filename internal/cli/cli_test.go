package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/daemon"
)

func TestTaskRemoveUsesTheTaskDeleteRouteAndPrintsItsRefusal(t *testing.T) {
	repo := t.TempDir()
	var deleteCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects":
			if r.Method != http.MethodGet {
				t.Fatalf("project lookup method = %s, want GET", r.Method)
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"calc","path":"` + filepath.ToSlash(repo) + `"}]}`))
		case "/v1/projects/calc/tasks/T-061":
			deleteCalled = r.Method == http.MethodDelete
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"message":"task T-061 has committed work"}}`))
		default:
			t.Fatalf("unexpected engine request: %s %s", r.Method, r.URL.Path)
		}
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

	state := t.TempDir()
	oldState := os.Getenv("XDG_STATE_HOME")
	t.Setenv("XDG_STATE_HOME", state)
	defer os.Setenv("XDG_STATE_HOME", oldState)
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

	oldErr := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	code := taskCmd("remove", []string{"T-061"}, repo)
	write.Close()
	os.Stderr = oldErr
	out, _ := io.ReadAll(read)
	read.Close()
	if code != 1 {
		t.Errorf("task remove exit code = %d, want 1 for engine refusal", code)
	}
	if !deleteCalled {
		t.Error("task remove did not DELETE /v1/projects/calc/tasks/T-061")
	}
	if !strings.Contains(string(out), "task T-061 has committed work") {
		t.Errorf("refusal was not printed verbatim: %q", out)
	}
}

func TestReleaseRequiresAnExplicitVerb(t *testing.T) {
	repo := t.TempDir()
	var planCalls int
	var planBump string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects":
			if r.Method != http.MethodGet {
				t.Fatalf("project lookup method = %s, want GET", r.Method)
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"calc","path":"` + filepath.ToSlash(repo) + `"}]}`))
		case "/v1/projects/calc/releases":
			if r.Method != http.MethodPost {
				t.Fatalf("release plan method = %s, want POST", r.Method)
			}
			planCalls++
			var req map[string]string
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			planBump = req["bump"]
			_, _ = w.Write([]byte(`{"id":"r-1"}`))
		case "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"run_end\",\"run_id\":\"r-1\",\"data\":{\"verdict\":\"PASSED\"}}\n\n"))
		default:
			t.Fatalf("unexpected engine request: %s %s", r.Method, r.URL.Path)
		}
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

	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
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

	oldErr := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	code := releaseCmd("", nil, repo)
	_ = write.Close()
	os.Stderr = oldErr
	out, _ := io.ReadAll(read)
	_ = read.Close()
	if code != 2 {
		t.Errorf("bare release exit code = %d, want 2", code)
	}
	if got := string(out); !strings.Contains(got, "ducklab release plan [--bump major|minor|patch]") || !strings.Contains(got, "ducklab release cut <version>") {
		t.Errorf("bare release usage = %q", got)
	}
	if planCalls != 0 {
		t.Errorf("bare release started %d plan(s)", planCalls)
	}

	if code := releaseCmd("plan", []string{"--bump", "patch"}, repo); code != 0 {
		t.Errorf("release plan exit code = %d, want 0", code)
	}
	if planCalls != 1 || planBump != "patch" {
		t.Errorf("release plan calls = %d, bump = %q; want 1, patch", planCalls, planBump)
	}
}

func TestVersionPrintsInstalledProvenance(t *testing.T) {
	oldArgs, oldVersion := os.Args, Version
	defer func() { os.Args, Version = oldArgs, oldVersion }()
	os.Args = []string{"ducklab", "--version"}
	Version = "0.4.0"
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = write
	if code := Run([]string{"--version"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	write.Close()
	os.Stdout = oldStdout
	// The exact values are build-time data; the operator-facing contract is that
	// branch and commit are present, rather than an untraceable version number.
	buf := make([]byte, 4096)
	n, _ := read.Read(buf)
	text := string(buf[:n])
	if !strings.Contains(text, "dev") || !strings.Contains(text, "0.4.0") {
		t.Fatalf("version omitted installed provenance: %q", text)
	}
}

// A word in the subcommand position used to fall through to "it must be a task
// ID", so `ducklab run diff <id>` started a model run on a task called "diff"
// instead of printing a diff — and any typo did the same. A mistake should not
// cost tokens.
func TestAnUnknownRunSubcommandDoesNotStartARun(t *testing.T) {
	for _, arg := range []string{"diffs", "acept", "sohw", "deploy"} {
		if runVerbs[arg] || taskIDRe.MatchString(arg) {
			t.Errorf("%q would still be dispatched instead of refused", arg)
		}
	}
	for _, id := range []string{"T-001", "BUG-42", "M-7"} {
		if !taskIDRe.MatchString(id) {
			t.Errorf("%q is a task ID and must still run", id)
		}
	}
	for _, v := range []string{"diff", "accept", "show", "list", "watch", "resume", "abort", "reject", "answer", "gc"} {
		if !runVerbs[v] {
			t.Errorf("%q is a documented subcommand but is not dispatched", v)
		}
	}
}

// The note is prose. Parsing it as flags would reject the first sentence that
// happens to start with a word the parser knows.
func TestReviseTakesTheRestOfTheLineAsANote(t *testing.T) {
	// Mirrors the loop in stageCmd: once `revise` is seen, nothing else is a
	// flag.
	args := []string{"revise", "SPEC-004", "should", "also", "lock", "--from", "the", "opposite"}
	sub := ""
	var note []string
	for _, a := range args {
		if sub == "revise" {
			note = append(note, a)
			continue
		}
		if a == "revise" {
			sub = a
		}
	}
	got := strings.Join(note, " ")
	if got != "SPEC-004 should also lock --from the opposite" {
		t.Errorf("note = %q", got)
	}
}
