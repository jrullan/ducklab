package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/config"
)

func testExecContext(t *testing.T) *ExecContext {
	root := t.TempDir()
	return &ExecContext{
		ProjectRoot:  root,
		RunID:        "test-run",
		Autonomy:     config.AutonomyGuarded,
		UnsafeWrites: false,
		ShellPolicy: config.ShellPolicy{
			Mode:          "guarded",
			Deny:          []string{"rm -rf /", "shutdown"},
			AllowPrefixes: []string{"go ", "ls", "cat", "echo "},
			TimeoutS:      10,
		},
	}
}

func TestPathJail(t *testing.T) {
	root := t.TempDir()
	// Create a file inside root
	inside := filepath.Join(root, "inside.txt")
	os.WriteFile(inside, []byte("test"), 0o644)

	// Valid paths
	tests := []struct {
		path    string
		wantErr bool
	}{
		{"inside.txt", false},
		{".", false},
		{"subdir/../inside.txt", false},
		{inside, false},
	}
	for _, tt := range tests {
		_, err := PathJail(root, tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("PathJail(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
		}
	}

	// Invalid paths (escape root)
	escapes := []string{
		"../outside.txt",
		"/etc/passwd",
		filepath.Join(root, "..", "outside.txt"),
	}
	for _, path := range escapes {
		_, err := PathJail(root, path)
		if err == nil {
			t.Errorf("PathJail(%q) should fail", path)
		}
	}
}

func TestWriteGuardJail(t *testing.T) {
	ectx := testExecContext(t)
	guard := WriteGuard(ectx, "../outside.txt", []byte("test"), true)
	if guard == nil {
		t.Error("WriteGuard should fail for escaping path")
	} else if !strings.Contains(guard.Content, "jail") {
		t.Errorf("guard should mention jail: %s", guard.Content)
	}
}

func TestWriteGuardDenylist(t *testing.T) {
	ectx := testExecContext(t)
	guard := WriteGuard(ectx, ".git/config", []byte("test"), true)
	if guard == nil {
		t.Error("WriteGuard should fail for .git path")
	}
}

func TestWriteGuardMarker(t *testing.T) {
	ectx := testExecContext(t)
	content := []byte("line1\n<<<<<<< HEAD\nline2\n")
	guard := WriteGuard(ectx, "test.txt", content, true)
	if guard == nil {
		t.Error("WriteGuard should fail for conflict marker")
	} else if !strings.Contains(guard.Content, "marker guard") {
		t.Errorf("guard should mention marker guard: %s", guard.Content)
	}
}

func TestWriteGuardDucklabMarker(t *testing.T) {
	ectx := testExecContext(t)
	content := []byte("line1\n```ducklab\n{\"tool\":\"test\"}\n```\n")
	guard := WriteGuard(ectx, "test.txt", content, true)
	if guard == nil {
		t.Error("WriteGuard should fail for ducklab marker")
	}
}

func TestWriteGuardTruncation(t *testing.T) {
	ectx := testExecContext(t)
	// Create a file > 200 bytes
	bigContent := strings.Repeat("x", 2000)
	os.WriteFile(filepath.Join(ectx.ProjectRoot, "big.txt"), []byte(bigContent), 0o644)

	// Try to shrink it to < 40%
	smallContent := strings.Repeat("y", 100)
	guard := WriteGuard(ectx, "big.txt", []byte(smallContent), true)
	if guard == nil {
		t.Error("WriteGuard should fail for truncation")
	} else if !strings.Contains(guard.Content, "truncation guard") {
		t.Errorf("guard should mention truncation guard: %s", guard.Content)
	}
}

func TestWriteGuardBinary(t *testing.T) {
	ectx := testExecContext(t)
	content := []byte{'a', 'b', 0, 'c'}
	guard := WriteGuard(ectx, "test.txt", content, true)
	if guard == nil {
		t.Error("WriteGuard should fail for NUL bytes")
	}
}

func TestWriteGuardSize(t *testing.T) {
	ectx := testExecContext(t)
	content := make([]byte, 1024*1024+1)
	guard := WriteGuard(ectx, "test.txt", content, true)
	if guard == nil {
		t.Error("WriteGuard should fail for > 1MB")
	}
}

func TestWriteGuardUnsafeWrites(t *testing.T) {
	ectx := testExecContext(t)
	ectx.UnsafeWrites = true
	content := []byte("line1\n<<<<<<< HEAD\nline2\n")
	guard := WriteGuard(ectx, "test.txt", content, true)
	if guard != nil {
		t.Errorf("WriteGuard with UnsafeWrites should allow markers: %s", guard.Content)
	}
}

func TestShellPolicyCheck(t *testing.T) {
	ectx := testExecContext(t)

	// Allowed command
	if guard := ShellPolicyCheck(ectx, "go test ./..."); guard != nil {
		t.Errorf("go test should be allowed: %s", guard.Content)
	}

	// Denied command
	if guard := ShellPolicyCheck(ectx, "rm -rf /"); guard == nil {
		t.Error("rm -rf / should be denied")
	}

	// Not in allowlist
	if guard := ShellPolicyCheck(ectx, "curl http://example.com"); guard == nil {
		t.Error("curl should not be allowed in guarded mode")
	}

	// Free mode
	ectx.ShellPolicy.Mode = "free"
	if guard := ShellPolicyCheck(ectx, "curl http://example.com"); guard != nil {
		t.Errorf("curl should be allowed in free mode: %s", guard.Content)
	}

	// Off mode
	ectx.ShellPolicy.Mode = "off"
	if guard := ShellPolicyCheck(ectx, "ls"); guard == nil {
		t.Error("shell should be disabled in off mode")
	}
}

func TestScrubEnv(t *testing.T) {
	os.Setenv("TEST_API_KEY", "secret")
	os.Setenv("DUCKLAB_TEST", "secret")
	defer os.Unsetenv("TEST_API_KEY")
	defer os.Unsetenv("DUCKLAB_TEST")

	providers := map[config.ProviderID]config.Provider{
		"test": {APIKeyEnv: "TEST_API_KEY"},
	}
	env := ScrubEnv(providers)
	for _, e := range env {
		if strings.HasPrefix(e, "TEST_API_KEY=") {
			t.Error("TEST_API_KEY should be scrubbed")
		}
		if strings.HasPrefix(e, "DUCKLAB_TEST=") {
			t.Error("DUCKLAB_TEST should be scrubbed")
		}
	}
}

func TestFSWrite(t *testing.T) {
	ectx := testExecContext(t)
	tool := &FSWrite{}
	args, _ := json.Marshal(map[string]string{
		"path":    "test.txt",
		"content": "hello world",
	})
	result, err := tool.Execute(context.Background(), ectx, args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("FSWrite failed: %s", result.Content)
	}
	// Verify file was written
	data, err := os.ReadFile(filepath.Join(ectx.ProjectRoot, "test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("content = %q, want %q", data, "hello world")
	}
}

func TestFSRead(t *testing.T) {
	ectx := testExecContext(t)
	os.WriteFile(filepath.Join(ectx.ProjectRoot, "test.txt"), []byte("line1\nline2\nline3"), 0o644)
	tool := &FSRead{}
	args, _ := json.Marshal(map[string]interface{}{
		"path":  "test.txt",
		"start": 2,
		"end":   3,
	})
	result, err := tool.Execute(context.Background(), ectx, args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("FSRead failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "line2") {
		t.Errorf("should contain line2: %s", result.Content)
	}
}

func TestFSPatch(t *testing.T) {
	ectx := testExecContext(t)
	os.WriteFile(filepath.Join(ectx.ProjectRoot, "test.txt"), []byte("hello world"), 0o644)
	tool := &FSPatch{}
	args, _ := json.Marshal(map[string]interface{}{
		"path": "test.txt",
		"edits": []map[string]string{
			{"search": "world", "replace": "ducklab"},
		},
	})
	result, err := tool.Execute(context.Background(), ectx, args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("FSPatch failed: %s", result.Content)
	}
	data, _ := os.ReadFile(filepath.Join(ectx.ProjectRoot, "test.txt"))
	if string(data) != "hello ducklab" {
		t.Errorf("content = %q, want %q", data, "hello ducklab")
	}
}

func TestFSPatchMultipleMatches(t *testing.T) {
	ectx := testExecContext(t)
	os.WriteFile(filepath.Join(ectx.ProjectRoot, "test.txt"), []byte("hello hello hello"), 0o644)
	tool := &FSPatch{}
	args, _ := json.Marshal(map[string]interface{}{
		"path": "test.txt",
		"edits": []map[string]string{
			{"search": "hello", "replace": "bye"},
		},
	})
	result, err := tool.Execute(context.Background(), ectx, args)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("FSPatch should fail when search matches multiple times")
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	expected := []string{"fs_list", "fs_read", "fs_search", "fs_write", "fs_patch", "fs_delete", "shell", "verify_run", "git_status", "git_diff", "git_log"}
	for _, name := range expected {
		if _, err := r.Get(name); err != nil {
			t.Errorf("tool %q not registered: %v", name, err)
		}
	}
	if _, err := r.Get("nonexistent"); err == nil {
		t.Error("Get(\"nonexistent\") should fail")
	}
}

// .ducklab/runs holds every run's llm.jsonl: the full prompt and response of
// every duckling that has worked here. A tool that can read it hands a
// reviewer the implementer's reasoning transcript, which I7 exists to prevent.
//
// Measured: a triager searching a project pulled 290 KB of a run's own
// llm.jsonl into a tool result, and the next request was refused for exceeding
// the model's context.
func TestTheRunLogIsNotProjectContent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ducklab", "runs", "r-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ducklab", "runs", "r-1", "llm.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		".ducklab/runs/r-1/llm.jsonl",
		".ducklab/runs",
		"./.ducklab/runs/r-1/../r-1/llm.jsonl",
	} {
		if _, err := PathJail(root, p); err == nil {
			t.Errorf("%q was reachable", p)
		}
	}
}

// The documents stay readable: a duckling reading the plan it is implementing
// is the point, and requirements and specs are meant to be read.
func TestTheProjectDocumentsStayReadable(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ducklab", "docs", "plan.md"), []byte("# plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PathJail(root, ".ducklab/docs/plan.md"); err != nil {
		t.Errorf("the plan became unreadable: %v", err)
	}
}

// Truncating in silence hands a model half a file and lets it reason about the
// half as though it were the whole.
func TestCapResultSaysItTruncated(t *testing.T) {
	got := CapResult(strings.Repeat("x", 100), 20)
	if !strings.Contains(got, "truncated") {
		t.Errorf("a truncated result does not say so: %q", got)
	}
	if len(got) <= 20 {
		t.Error("the marker replaced the content instead of preceding it")
	}
	if short := CapResult("small", 100); short != "small" {
		t.Errorf("an uncapped result was altered: %q", short)
	}
}

// A new file in a new directory is the ordinary case for work that adds a
// package, and fs_write's own description promises it creates parents. The
// jail used to check exactly one level up, so a path two levels deep into
// nothing was rejected — and rejected as "path escapes root", which is a
// different and alarming claim about a path that was always inside it. A real
// split run burned six minutes and its whole token budget re-reading that
// message before giving up.
func TestJailAllowsAPathWhoseDirectoriesDoNotExistYet(t *testing.T) {
	root := t.TempDir()
	got, err := PathJail(root, "fixture/nested/deeper/file.go")
	if err != nil {
		t.Fatalf("PathJail: %v", err)
	}
	if want := filepath.Join(root, "fixture/nested/deeper/file.go"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The escape check must survive the walk: an ancestor that does not exist
// cannot be used to climb out of the root.
func TestJailStillRefusesToEscapeThroughDirectoriesThatDoNotExist(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"../outside/new/file.go", "nope/../../outside/file.go"} {
		if _, err := PathJail(root, p); err == nil {
			t.Errorf("PathJail(%q) allowed an escape", p)
		}
	}
}

// A symlink pointing out of the root is still an escape even when the file
// beyond it has not been created.
func TestJailRefusesANewFileUnderASymlinkedDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := PathJail(root, "link/new/file.go"); err == nil {
		t.Error("PathJail followed a symlink out of the root")
	}
}

// I3: nothing is unbounded. RunShell took a context and a timeout and used
// neither, so `sleep 999` — or an npm install waiting on a prompt — hung the
// run until someone noticed. The shell policy's timeout_s was decorative.
func TestShellStopsAtItsTimeout(t *testing.T) {
	ectx := &ExecContext{ProjectRoot: t.TempDir(), ShellPolicy: config.ShellPolicy{Mode: "yolo"}}
	start := time.Now()
	out, code, err := RunShell(context.Background(), ectx, "sleep 30", 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("RunShell returned a transport error rather than a timed-out command: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("waited %v for a 1s timeout", elapsed)
	}
	if code == 0 {
		t.Error("a command that was killed reported success")
	}
	if !strings.Contains(out, "timed out") {
		t.Errorf("output does not say why the command stopped: %q", out)
	}
}

// A cancelled run must not leave a command running behind it.
func TestShellStopsWhenTheRunIsCancelled(t *testing.T) {
	ectx := &ExecContext{ProjectRoot: t.TempDir(), ShellPolicy: config.ShellPolicy{Mode: "yolo"}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()

	start := time.Now()
	if _, code, _ := RunShell(ctx, ectx, "sleep 30", 300); code == 0 {
		t.Error("a cancelled command reported success")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("waited %v after cancellation", elapsed)
	}
}

// A test-first run writes the test and nothing else.
//
// Without this the model writes the implementation in the same breath, the
// gate goes green immediately, and the test has proved nothing — which is the
// exact failure the whole flow exists to prevent. The restriction is enforced
// here rather than asked for in a prompt, because a prompt is a request and
// this is a rule.
func TestATestFirstRunCanOnlyWriteTests(t *testing.T) {
	root := t.TempDir()
	ectx := &ExecContext{ProjectRoot: root, TestPathsOnly: true}

	for _, path := range []string{"add_test.go", "tests/thing.py", "src/x.test.ts"} {
		if guard := WriteGuard(ectx, path, []byte("x"), true); guard != nil {
			t.Errorf("writing a test was refused: %s → %s", path, guard.Content)
		}
	}
	for _, path := range []string{"add.go", "src/main.ts", "README.md"} {
		guard := WriteGuard(ectx, path, []byte("x"), true)
		if guard == nil {
			t.Errorf("a test-first run wrote %s, which is not a test", path)
			continue
		}
		if !strings.Contains(guard.Content, "test") {
			t.Errorf("the refusal does not say why: %s", guard.Content)
		}
	}

	// And the restriction is off by default: an ordinary run writes anything.
	if guard := WriteGuard(&ExecContext{ProjectRoot: root}, "add.go", []byte("x"), true); guard != nil {
		t.Errorf("an ordinary run was restricted: %s", guard.Content)
	}
}

// A project that says where its tests live must be obeyed, or the restriction
// refuses the very files it is meant to allow.
func TestTestPathsOnlyUsesTheProjectsGlobs(t *testing.T) {
	ectx := &ExecContext{
		ProjectRoot:   t.TempDir(),
		TestPathsOnly: true,
		Verify:        config.Verify{TestGlobs: []string{"checks/**"}},
	}
	if guard := WriteGuard(ectx, "checks/thing.go", []byte("x"), true); guard != nil {
		t.Errorf("a project's own test path was refused: %s", guard.Content)
	}
	if guard := WriteGuard(ectx, "add_test.go", []byte("x"), true); guard == nil {
		t.Error("the default globs still applied after the project named its own")
	}
}

// T-068: the task was "untrack frontend/dist" and the denylist refused every
// write to .gitignore — the one file that task has to edit — because the
// guard tested a STRING prefix where a path boundary was meant. ".gitignore"
// begins with ".git" and lives in no directory of that name. The implementer
// tried four spellings (./.gitignore, frontend/../.gitignore…) and each was
// blamed for touching a directory the write never went near.
func TestTheDenylistGuardsTheDirectoryNotTheSpelling(t *testing.T) {
	ectx := testExecContext(t)

	for _, denied := range []string{".git", ".git/config", ".git/hooks/pre-commit"} {
		if WriteGuard(ectx, denied, []byte("x"), true) == nil {
			t.Errorf("a write to %s was allowed", denied)
		}
	}
	for _, allowed := range []string{".gitignore", ".gitattributes", "./.gitignore", "frontend/../.gitignore"} {
		if guard := WriteGuard(ectx, allowed, []byte("frontend/dist/\n"), true); guard != nil {
			t.Errorf("a write to %s was refused: %s", allowed, guard.Content)
		}
	}
}

// The same spelling-vs-boundary disease hid .gitignore from every listing
// and search: a model asked to edit it could not first see that it existed.
func TestListingsShowDotGitignoreButNotDotGit(t *testing.T) {
	ectx := testExecContext(t)
	for path, body := range map[string]string{
		".gitignore":  "node_modules/\n",
		".git/config": "[core]\n",
		"app.py":      "print()\n",
	} {
		full := filepath.Join(ectx.ProjectRoot, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	list := &FSList{}
	res, err := list.Execute(context.Background(), ectx, json.RawMessage(`{"path":"."}`))
	if err != nil || res.IsError {
		t.Fatalf("list: %v %v", err, res)
	}
	if !strings.Contains(res.Content, ".gitignore") {
		t.Error("the listing hides .gitignore")
	}
	if strings.Contains(res.Content, ".git/config") {
		t.Error("the listing exposes .git internals")
	}

	search := &FSSearch{}
	res, err = search.Execute(context.Background(), ectx, json.RawMessage(`{"pattern":"node_modules"}`))
	if err != nil || res.IsError {
		t.Fatalf("search: %v %v", err, res)
	}
	if !strings.Contains(res.Content, ".gitignore") {
		t.Error("search cannot see inside .gitignore")
	}
}

// A flat 32KB cap handed a 32k-token model a quarter of its whole context in
// ONE tool result — two verify_runs and the model had forgotten its task,
// which is what a "loop" is. The bound scales to the seat reading it.
func TestToolResultsScaleToTheSeat(t *testing.T) {
	cases := []struct {
		context int
		want    int
	}{
		{0, MaxToolResultBytes},         // unknown window: the flat cap
		{1_000_000, MaxToolResultBytes}, // huge window: ceilinged, unchanged
		{32_768, 16_384},                // 32k seat: an eighth of its context
		{4_096, 4_096},                  // tiny seat: floored to stay usable
	}
	for _, c := range cases {
		if got := resultCapFor(c.context); got != c.want {
			t.Errorf("resultCapFor(%d) = %d, want %d", c.context, got, c.want)
		}
	}
}

// {"id":"plan"} — the kind in the id field, six identical retries, because
// the old error named the valid VALUES without naming the FIELD. When the
// intent is unambiguous, honor it; when it is not, teach the field.
func TestArtifactReadForgivesTheKindInTheIDField(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ducklab", "docs", "plan.md"),
		[]byte("## M-001 — Core\n\n### T-001 — Schema\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := &ArtifactRead{}
	res, err := tool.Execute(context.Background(), &ExecContext{ProjectRoot: dir},
		json.RawMessage(`{"id":"plan"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(res.Content, "T-001") {
		t.Errorf("the unambiguous intent was not honored: %.150s", res.Content)
	}
	// Genuinely wrong args teach the FIELD, not just the values.
	res, _ = tool.Execute(context.Background(), &ExecContext{ProjectRoot: dir},
		json.RawMessage(`{"kind":"blueprints"}`))
	if !res.IsError || !strings.Contains(res.Content, `"kind" FIELD`) {
		t.Errorf("the error does not teach the field: %.150s", res.Content)
	}
}

// Identical inputs cannot produce a different answer. The third identical
// failing call is refused with orders to change something — the generic
// form of the gate brake, for the arguments-wrong loop.
func TestTheExecutorRefusesRepeatedFailingCalls(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&ArtifactRead{})
	ectx := &ExecContext{ProjectRoot: t.TempDir()}
	bad := json.RawMessage(`{"kind":"nope"}`)
	for i := 0; i < RepeatFailLimit; i++ {
		res, _ := reg.Execute(context.Background(), ectx, "artifact_read", bad)
		if !res.IsError || strings.Contains(res.Content, "REFUSED") {
			t.Fatalf("call %d should fail normally: %.100s", i+1, res.Content)
		}
	}
	res, _ := reg.Execute(context.Background(), ectx, "artifact_read", bad)
	if !strings.Contains(res.Content, "REFUSED") || !strings.Contains(res.Content, "CHANGE the arguments") {
		t.Errorf("the repeat was not refused with correction: %.200s", res.Content)
	}
	// Changing the arguments resets the brake.
	res, _ = reg.Execute(context.Background(), ectx, "artifact_read", json.RawMessage(`{"kind":"also-nope"}`))
	if strings.Contains(res.Content, "REFUSED") {
		t.Error("a CHANGED call must run, not be refused")
	}
}
