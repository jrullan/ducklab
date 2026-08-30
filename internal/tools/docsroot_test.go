package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// A run in an isolated worktree still reads the project's documents: the
// worktree has the code, the project has .ducklab/docs. A build implementer
// asked for its task and the spec and was told neither existed.
func TestDocumentToolsReadFromTheProjectNotTheWorktree(t *testing.T) {
	project := t.TempDir()
	worktree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".ducklab", "docs", "plan.md"),
		[]byte("## M-001 — Core\n\n### T-001 — Scaffold\n\n**Implements:** SPEC-001\n\nWrite meson.build.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ectx := &ExecContext{ProjectRoot: worktree, DocsRoot: project}
	res, _ := (&TaskRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"id":"T-001"}`))
	if res.IsError || !strings.Contains(res.Content, "Write meson.build") {
		t.Fatalf("task_read from a worktree did not find the plan: err=%v %.200s", res.IsError, res.Content)
	}
	doc, _ := (&ArtifactRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"kind":"plan"}`))
	if doc.IsError || !strings.Contains(doc.Content, "T-001") {
		t.Fatalf("artifact_read from a worktree did not find the plan: err=%v %.200s", doc.IsError, doc.Content)
	}
}

// With no gate configured, verify_run says so as an error the identical-call
// brake can escalate — not as a success to repeat 73 times.
func TestVerifyRunWithoutAGateIsAnErrorThatTeaches(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&VerifyRun{})
	ectx := &ExecContext{ProjectRoot: t.TempDir(), Verify: config.Verify{Mode: "none"}}
	ectx.BeginTurn()
	res, _ := reg.Execute(context.Background(), ectx, "verify_run", json.RawMessage(`{}`))
	if !res.IsError || !strings.Contains(res.Content, "Do not call it again") {
		t.Fatalf("verify_run without a gate was served as success: err=%v %.200s", res.IsError, res.Content)
	}
}

// A shell file operation is denied with the tools that do it, not with
// "ask a person".
func TestDeniedShellFileOperationsNameTheFileTools(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Shell{})
	ectx := &ExecContext{ProjectRoot: t.TempDir(), ShellPolicy: config.ShellPolicy{Mode: "guarded", AllowPrefixes: []string{"go "}}}
	res, _ := reg.Execute(context.Background(), ectx, "shell", json.RawMessage(`{"cmd":"mkdir -p src data"}`))
	if !res.IsError || !strings.Contains(res.Content, "fs_write creates a file AND its directories") {
		t.Fatalf("the denial did not teach the file tools: %.200s", res.Content)
	}
}
