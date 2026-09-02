package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project document is read as an artifact; fs_read of the same .md put a
// second copy of the text in the context (a plan architect read requirements
// and spec twice each, once per tool).
func TestFSReadOfAProjectDocumentRedirectsToArtifactRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ducklab", "docs", "spec.md"), []byte("## SPEC-001 — X\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register(&FSRead{})
	ectx := &ExecContext{ProjectRoot: dir}
	res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":".ducklab/docs/spec.md"}`))
	if !res.IsError || !strings.Contains(res.Content, `artifact_read {"kind":"spec"}`) {
		t.Fatalf("fs_read of spec.md was not redirected: err=%v %.200s", res.IsError, res.Content)
	}
	res, _ = reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":".ducklab/docs/plan.md.proposed"}`))
	if !res.IsError || !strings.Contains(res.Content, `"kind":"plan"`) {
		t.Fatalf("fs_read of a proposal was not redirected: %.200s", res.Content)
	}
}

// The same read twice in one turn is refused with directions; a new turn
// makes it legitimate again.
func TestARepeatedReadInOneTurnIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register(&FSRead{})
	ectx := &ExecContext{ProjectRoot: dir}
	ectx.BeginTurn()
	args := json.RawMessage(`{"path":"main.go"}`)
	first, _ := reg.Execute(context.Background(), ectx, "fs_read", args)
	if first.IsError {
		t.Fatalf("first read failed: %s", first.Content)
	}
	second, _ := reg.Execute(context.Background(), ectx, "fs_read", args)
	if !second.IsError || !strings.Contains(second.Content, "REPEATED READ") {
		t.Fatalf("the repeated read was served again: err=%v %.150s", second.IsError, second.Content)
	}
	// Refused once; a seat that asks a third time is served with a reminder
	// rather than stranded (thirteen refusals at 37 s each, once).
	again, _ := reg.Execute(context.Background(), ectx, "fs_read", args)
	if again.IsError || !strings.Contains(again.Content, "served again") || !strings.Contains(again.Content, "package main") {
		t.Fatalf("the third read was not served with a reminder: err=%v %.200s", again.IsError, again.Content)
	}
	// A fourth identical read is reading as a way of thinking: exploration
	// closes, but a build turn must still be able to deliver and verify work
	// it has already constructed.
	fourth, _ := reg.Execute(context.Background(), ectx, "fs_read", args)
	if !fourth.IsError || fourth.EndTurn || !ectx.ReadToolsClosed || ectx.ToolsClosed {
		t.Fatalf("the fourth identical read did not close only exploration: %+v", fourth)
	}
	if ectx.ToolAvailable("fs_read") || !ectx.ToolAvailable("fs_write") || !ectx.ToolAvailable("verify_run") {
		t.Fatal("the read brake did not preserve delivery and verification tools")
	}
	ectx.BeginTurn()
	if ectx.ReadToolsClosed {
		t.Fatal("BeginTurn did not reopen read-only tools")
	}
	third, _ := reg.Execute(context.Background(), ectx, "fs_read", args)
	if third.IsError {
		t.Fatalf("a new turn's read was refused: %s", third.Content)
	}
}

func TestResearchBudgetForcesActionAndReopensAfterWrite(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	reg.Register(&FSRead{})
	reg.Register(&FSWrite{})
	ectx := &ExecContext{ProjectRoot: dir}
	ectx.BeginTurn()
	for i := 0; i < ExplorationCallLimit; i++ {
		name := fmt.Sprintf("read-%02d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(fmt.Sprintf(`{"path":%q}`, name)))
		if res.IsError {
			t.Fatalf("research call %d was refused early: %s", i+1, res.Content)
		}
	}
	if ectx.ToolAvailable("fs_read") || ectx.ToolAvailable("shell") || !ectx.ToolAvailable("fs_write") || !ectx.ToolAvailable("verify_run") {
		t.Fatal("research boundary did not close exploration while preserving action tools")
	}
	blocked, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"read-00.txt"}`))
	if !blocked.IsError || !strings.Contains(blocked.Content, "RESEARCH BUDGET EXHAUSTED") {
		t.Fatalf("post-budget research was not refused with an action boundary: %+v", blocked)
	}
	written, _ := reg.Execute(context.Background(), ectx, "fs_write", json.RawMessage(`{"path":"new.txt","content":"progress"}`))
	if written.IsError || !ectx.ToolAvailable("fs_read") || !ectx.ToolAvailable("shell") {
		t.Fatalf("successful file progress did not reopen bounded research: %+v", written)
	}
}

func TestResearchBudgetHonorsTurnOverride(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	reg.Register(&FSRead{})
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ectx := &ExecContext{ProjectRoot: dir, ExplorationCallLimit: 2}
	ectx.BeginTurn()
	for _, name := range []string{"one.txt", "two.txt"} {
		res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(fmt.Sprintf(`{"path":%q}`, name)))
		if res.IsError {
			t.Fatalf("override refused %s early: %s", name, res.Content)
		}
	}
	blocked, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"three.txt"}`))
	if !blocked.IsError || !strings.Contains(blocked.Content, "2 observational calls") {
		t.Fatalf("turn override was not enforced: %+v", blocked)
	}
}
