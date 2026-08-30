package tools

import (
	"context"
	"encoding/json"
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
	ectx.BeginTurn()
	third, _ := reg.Execute(context.Background(), ectx, "fs_read", args)
	if third.IsError {
		t.Fatalf("a new turn's read was refused: %s", third.Content)
	}
}
