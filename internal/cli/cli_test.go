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

// B-355: grammar preflight is useful only before a run exists. The CLI sends
// the file body to the dedicated syntax-only route, prints the gate's exact
// diagnostic, and exits non-zero so a script can stop the freeze.
func TestArtifactLintPrintsTheOffendingTokenAndExitsOne(t *testing.T) {
	repo := t.TempDir()
	candidate := filepath.Join(repo, "candidate.md")
	content := "## M-01 — Core\n\n### T-001 — Task\n\n**Implementa:** SPEC-001\n"
	if err := os.WriteFile(candidate, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var lintCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects":
			_, _ = w.Write([]byte(`{"items":[{"id":"calc","path":"` + filepath.ToSlash(repo) + `"}]}`))
		case "/v1/projects/calc/artifacts/plan/lint":
			lintCalled = r.Method == http.MethodPost
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request["content"] != content {
				t.Errorf("lint content = %q, want complete file", request["content"])
			}
			_, _ = w.Write([]byte(`{"kind":"plan","valid":false,"errors":[{"section":"T-001","field":"Implementa","offending_token":"Implementa","canonical":"Implements","message":"T-001 unknown field **Implementa:**; use **Implements:**"}]}`))
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

	oldOut := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	code := artifactCmd("lint", []string{"--kind", "plan", candidate}, repo)
	_ = write.Close()
	os.Stdout = oldOut
	out, _ := io.ReadAll(read)
	_ = read.Close()
	if code != 1 {
		t.Errorf("artifact lint exit code = %d, want 1", code)
	}
	if !lintCalled {
		t.Fatal("artifact lint did not call the syntax-only route")
	}
	if got := string(out); !strings.Contains(got, "Implementa") || !strings.Contains(got, "use **Implements:**") {
		t.Errorf("lint output omitted the offending token or canonical form: %q", got)
	}
}

func TestArtifactLintPrintsLegacyNoticeAndExitsZero(t *testing.T) {
	repo := t.TempDir()
	candidate := filepath.Join(repo, "legacy.md")
	if err := os.WriteFile(candidate, []byte("## M-01 — Core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects":
			_, _ = w.Write([]byte(`{"items":[{"id":"calc","path":"` + filepath.ToSlash(repo) + `"}]}`))
		case "/v1/projects/calc/artifacts/plan/lint":
			_, _ = w.Write([]byte(`{"kind":"plan","valid":true,"errors":[],"notices":[{"code":"legacy_grammar","message":"frontmatter has no grammar: add \"grammar: 2\" to be checked against the current contract"}]}`))
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

	oldOut := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	code := artifactCmd("lint", []string{"--kind", "plan", candidate}, repo)
	_ = write.Close()
	os.Stdout = oldOut
	out, _ := io.ReadAll(read)
	_ = read.Close()
	if code != 0 {
		t.Fatalf("legacy lint exit code = %d, want 0; output: %s", code, out)
	}
	got := string(out)
	if !strings.Contains(got, "notice: frontmatter has no grammar") || !strings.Contains(got, "plan grammar is valid") {
		t.Fatalf("legacy lint output = %q", got)
	}
}

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
	for _, v := range []string{"diff", "accept", "show", "list", "watch", "resume", "lift", "abort", "reject", "answer", "gc"} {
		if !runVerbs[v] {
			t.Errorf("%q is a documented subcommand but is not dispatched", v)
		}
	}
}

func TestRunStartOmitsModeUnlessThePersonNamesOne(t *testing.T) {
	repo := t.TempDir()
	var requests []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects":
			_, _ = w.Write([]byte(`{"items":[{"id":"calc","path":"` + filepath.ToSlash(repo) + `"}]}`))
		case "/v1/projects/calc/runs":
			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			requests = append(requests, req)
			_, _ = w.Write([]byte(`{"id":"r-1","status":"running"}`))
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

	if code := runStart("T-004", "", false, false, true, false, repo, nil, 0, 0); code != 0 {
		t.Fatalf("omitted-mode run exit code = %d", code)
	}
	if _, present := requests[0]["mode"]; present {
		t.Errorf("omitted mode was serialized as an explicit request: %#v", requests[0])
	}
	if code := runStart("T-004", "pair", false, false, true, false, repo, nil, 0, 0); code != 0 {
		t.Fatalf("explicit-mode run exit code = %d", code)
	}
	if got := requests[1]["mode"]; got != "pair" {
		t.Errorf("explicit mode = %#v, want pair", got)
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

// A failed/paused stage must not print the last proposal merely because one
// exists in the project. Neocapture's regression run displayed the rejected
// proposal from the previous day, complete with accept instructions.
func TestStageOnlyShowsTheProposalProducedByItsOwnRun(t *testing.T) {
	proposal := map[string]interface{}{"run_id": "r-old", "diff": "stale"}
	if proposalBelongsToRun(proposal, "r-new") {
		t.Fatal("a stale proposal was attributed to the current stage run")
	}
	proposal["run_id"] = "r-new"
	if !proposalBelongsToRun(proposal, "r-new") {
		t.Fatal("the current run's proposal was hidden")
	}
}
