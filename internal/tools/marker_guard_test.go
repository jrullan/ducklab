package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Editing a file that legitimately contains the protocol's own fences — agent.go
// defines them, docs quote them — was impossible: the marker guard refused any
// result still holding a ```ducklab / ```payload: line, i.e. content the model
// never authored. The guard now refuses only markers a write INTRODUCES.
func TestMarkerGuardAllowsEditingAFileThatAlreadyHoldsProtocolFences(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agent.go")
	// A file that documents the protocol, like the real agent.go.
	body := "package agent\n" +
		"// docs: the model ends with a ```ducklab block, and a ```payload:1 block for content\n" +
		"const version = 1\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Change an unrelated line; the marker lines stay untouched in the result.
	args := `{"path":"agent.go","edits":[{"search":"const version = 1","replace":"const version = 2"}]}`
	res, err := (&FSPatch{}).Execute(context.Background(),
		&ExecContext{ProjectRoot: root}, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("guard refused a patch that introduced no new marker: %q", res.Content)
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "const version = 2") {
		t.Errorf("edit not applied: %q", after)
	}
}

// The guard still catches a model leaking a NEW protocol fence into source.
func TestMarkerGuardRefusesANewlyIntroducedProtocolFence(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "clean.go")
	if err := os.WriteFile(path, []byte("package p\nconst x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The replacement injects a ```ducklab line that was not in the file.
	args := `{"path":"clean.go","edits":[{"search":"const x = 1","replace":"const x = 1\n// ` +
		"```ducklab" + ` leaked into source"}]}`
	res, err := (&FSPatch{}).Execute(context.Background(),
		&ExecContext{ProjectRoot: root}, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("guard let a newly introduced ```ducklab marker through")
	}
	if !strings.Contains(res.Content, "marker guard") {
		t.Errorf("refusal did not name the marker guard: %q", res.Content)
	}
}
