package tools

import (
	"fmt"

	"context"
	"encoding/json"
	"github.com/jrullan/ducklab/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B-491: small models spell project paths with a leading slash. The jail
// reads them as project-relative when the first component exists in the
// project, and nothing else changes: host paths and symlink escapes still
// fail, now with the spelling that works.
func TestPathJailReadsALeadingSlashAsAProjectPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "parser.test.mjs"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	rootAbs, _ := filepath.EvalSymlinks(root)

	for path, want := range map[string]string{
		"/tests/parser.test.mjs":              filepath.Join(rootAbs, "tests", "parser.test.mjs"),
		"/tests/new.test.mjs":                 filepath.Join(root, "tests", "new.test.mjs"),
		filepath.Join(root, "brand-new.txt"):  filepath.Join(root, "brand-new.txt"),
		filepath.Join(root, "tests", "a.mjs"): filepath.Join(root, "tests", "a.mjs"),
	} {
		got, err := PathJail(root, path)
		if err != nil || got != want {
			t.Errorf("PathJail(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
	for _, path := range []string{"/etc/passwd", "/no-such-top/x", "/link/secret", filepath.Join(root, "link", "secret")} {
		if got, err := PathJail(root, path); err == nil {
			t.Errorf("PathJail(%q) = %q, want an escape error", path, got)
		}
	}
	_, err := PathJail(root, "/no-such-top/x")
	if err == nil || !strings.Contains(err.Error(), `write "no-such-top/x" with no leading slash`) {
		t.Errorf("escape error does not name the working spelling: %v", err)
	}
}

func researchBoundaryReached(t *testing.T) (*Registry, *ExecContext, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt", "tests/parser.test.mjs"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := NewRegistry()
	reg.Register(&FSRead{})
	reg.Register(&FSWrite{})
	ectx := &ExecContext{ProjectRoot: dir, ExplorationCallLimit: 2}
	ectx.BeginTurn()
	for _, name := range []string{"a.txt", "b.txt"} {
		if res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"`+name+`"}`)); res.IsError {
			t.Fatalf("read %s refused early: %s", name, res.Content)
		}
	}
	return reg, ectx, dir
}

// B-492: repeated read requests after the boundary close tool use and ask
// for the concrete next edit, instead of letting the seat spend every
// remaining call on refusals (atom-local: 10 in a row, three times, T-014).
// Not sooner than the fifth: most turns that hit the boundary edit after 1-3
// refusals (TI-36X T-005 lost its test to a brake at the second).
func TestRepeatedReadsAfterTheResearchBoundaryCloseTools(t *testing.T) {
	reg, ectx, _ := researchBoundaryReached(t)
	// Four, literally: the measured turns that edit after the boundary need
	// up to 3-4 refusals, so the limit must not drop below five.
	for i := 1; i <= 4; i++ {
		res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"a.txt"}`))
		if !res.IsError || !strings.Contains(res.Content, "RESEARCH BUDGET EXHAUSTED") || res.EndTurn || ectx.ToolsClosed {
			t.Fatalf("refusal %d should state the boundary and keep action tools: %+v", i, res)
		}
	}
	last, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"a.txt"}`))
	if !last.IsError || !last.EndTurn || !ectx.ToolsClosed || !strings.Contains(last.Content, "exact edit you would make next") {
		t.Fatalf("the fifth refusal should close tools and ask for the concrete edit: %+v", last)
	}
	if ectx.ToolAvailable("fs_write") {
		t.Fatal("tools stayed available after the close")
	}
}

// A file change between refusals is the action the boundary asks for: it
// reopens research and resets the count.
func TestAWriteBetweenRefusalsResetsTheBrake(t *testing.T) {
	reg, ectx, _ := researchBoundaryReached(t)
	if res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"a.txt"}`)); !strings.Contains(res.Content, "RESEARCH BUDGET EXHAUSTED") {
		t.Fatalf("expected the boundary: %+v", res)
	}
	if res, _ := reg.Execute(context.Background(), ectx, "fs_write", json.RawMessage(`{"path":"c.txt","content":"progress"}`)); res.IsError {
		t.Fatalf("write refused: %s", res.Content)
	}
	for _, name := range []string{"c.txt", "tests/parser.test.mjs"} {
		if res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"`+name+`"}`)); res.IsError {
			t.Fatalf("research did not reopen after the write: %s", res.Content)
		}
	}
	if res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"b.txt"}`)); res.EndTurn || ectx.ToolsClosed {
		t.Fatalf("a refusal after the write was counted with the one before it: %+v", res)
	}
}

// B-492: an invalid path past the boundary reports the path error and does
// not count as a research refusal; a leading-slash project path is not an
// error at all (B-491), so it meets the ordinary boundary.
func TestTheResearchBoundaryDoesNotMaskPathErrors(t *testing.T) {
	reg, ectx, _ := researchBoundaryReached(t)
	bad, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"../outside.txt"}`))
	if !bad.IsError || !strings.Contains(bad.Content, "path escapes root") || strings.Contains(bad.Content, "RESEARCH") {
		t.Fatalf("a bad path past the boundary should report the path: %+v", bad)
	}
	again, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"../outside.txt"}`))
	if again.EndTurn || ectx.ToolsClosed {
		t.Fatalf("path errors were counted as research refusals: %+v", again)
	}
	slash, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"/tests/parser.test.mjs"}`))
	if !strings.Contains(slash.Content, "RESEARCH BUDGET EXHAUSTED") {
		t.Fatalf("a leading-slash project path should meet the ordinary boundary: %+v", slash)
	}
}

// B-491 end to end: with research still open, a leading-slash read and patch
// reach the project file.
func TestALeadingSlashReadAndWriteReachTheProjectFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "parser.test.mjs"), []byte("expect 0.015625\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register(&FSRead{})
	reg.Register(&FSWrite{})
	ectx := &ExecContext{ProjectRoot: dir}
	ectx.BeginTurn()
	read, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"/tests/parser.test.mjs"}`))
	if read.IsError || !strings.Contains(read.Content, "0.015625") {
		t.Fatalf("leading-slash read failed: %+v", read)
	}
	written, _ := reg.Execute(context.Background(), ectx, "fs_write", json.RawMessage(`{"path":"/tests/parser.test.mjs","content":"expect 0.001953125\n"}`))
	if written.IsError {
		t.Fatalf("leading-slash write failed: %s", written.Content)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "tests", "parser.test.mjs"))
	if !strings.Contains(string(data), "0.001953125") {
		t.Fatalf("the write did not reach the project file: %q", data)
	}
}

func leadingSlashProject(t *testing.T) (string, *Registry) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tests/parser.test.mjs", "index.html", "logic.mjs"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := NewRegistry()
	reg.Register(&FSRead{})
	reg.Register(&FSWrite{})
	return root, reg
}

// Review of #145: the jail resolved "/tests/x", but the lane, test globs and
// protected globs still compared "/tests/x", which no lane contains. Every
// policy now sees one project-relative spelling.
func TestALeadingSlashWriteMeetsTheRealImplementerLane(t *testing.T) {
	root, reg := leadingSlashProject(t)
	ectx := &ExecContext{ProjectRoot: root, Role: config.RoleImplementer, TaskWritableFiles: []string{"tests/parser.test.mjs"}}
	ectx.BeginTurn()
	for _, path := range []string{"/tests/parser.test.mjs", filepath.Join(root, "tests", "parser.test.mjs")} {
		res, _ := reg.Execute(context.Background(), ectx, "fs_write", json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"fixed\n"}`, path)))
		if res.IsError {
			t.Errorf("in-lane write via %q refused: %s", path, res.Content)
		}
	}
	out, _ := reg.Execute(context.Background(), ectx, "fs_write", json.RawMessage(`{"path":"/index.html","content":"no\n"}`))
	if !out.IsError || !strings.Contains(out.Content, "lane: edit to index.html is outside") {
		t.Errorf("out-of-lane write via a leading slash should name the project path: %+v", out)
	}
}

func TestALeadingSlashWriteMeetsProtectedAndTestOnlyGlobs(t *testing.T) {
	root, reg := leadingSlashProject(t)
	protected := &ExecContext{ProjectRoot: root, ShellPolicy: config.ShellPolicy{Deny: []string{"tests/*"}}}
	protected.BeginTurn()
	res, _ := reg.Execute(context.Background(), protected, "fs_write", json.RawMessage(`{"path":"/tests/a.mjs","content":"x\n"}`))
	if !res.IsError || !strings.Contains(res.Content, "protected path") {
		t.Errorf("a leading slash slipped past a protected glob: %+v", res)
	}
	testsOnly := &ExecContext{ProjectRoot: root, TestPathsOnly: true}
	testsOnly.BeginTurn()
	if res, _ := reg.Execute(context.Background(), testsOnly, "fs_write", json.RawMessage(`{"path":"/tests/new.test.mjs","content":"x\n"}`)); res.IsError {
		t.Errorf("a test-only run refused a test file spelled with a leading slash: %s", res.Content)
	}
	if res, _ := reg.Execute(context.Background(), testsOnly, "fs_write", json.RawMessage(`{"path":"/logic.mjs","content":"x\n"}`)); !res.IsError || !strings.Contains(res.Content, "logic.mjs is not one") {
		t.Errorf("a test-only run accepted a source file via a leading slash: %+v", res)
	}
}

// Review of #145: past the boundary, the path pre-check must use the tool's
// own root. ref_read reads authorized references outside the project, and a
// read scope names another tree; both get the ordinary boundary, not an
// escape error.
func TestTheBoundaryPreCheckUsesEachToolsOwnRoot(t *testing.T) {
	root, _ := leadingSlashProject(t)
	external := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(external, []byte("# Spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	harness := t.TempDir()
	if err := os.WriteFile(filepath.Join(harness, "guide.md"), []byte("guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register(&FSRead{})
	reg.Register(&RefRead{})
	ectx := &ExecContext{ProjectRoot: root, RefPaths: []string{external}, ExplorationCallLimit: 1,
		ReadScopes: map[string]ReadScope{"harness": {ProjectRoot: harness}}}
	ectx.BeginTurn()
	if res, _ := reg.Execute(context.Background(), ectx, "ref_read", json.RawMessage(fmt.Sprintf(`{"path":%q}`, external))); res.IsError {
		t.Fatalf("authorized reference refused before the boundary: %s", res.Content)
	}
	ref, _ := reg.Execute(context.Background(), ectx, "ref_read", json.RawMessage(fmt.Sprintf(`{"path":%q}`, external)))
	if !strings.Contains(ref.Content, "RESEARCH BUDGET EXHAUSTED") {
		t.Errorf("an authorized reference past the boundary was reclassified: %+v", ref)
	}
	ectx.BeginTurn()
	ectx.ExplorationCallLimit = 0
	ectx.explorationCalls = ExplorationCallLimit
	scoped, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"scope":"harness","path":"/guide.md"}`))
	if !strings.Contains(scoped.Content, "RESEARCH BUDGET EXHAUSTED") {
		t.Errorf("a harness-scope read past the boundary was judged against the project root: %+v", scoped)
	}
}
