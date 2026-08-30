package stage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// Every seat of a spec or plan council on an empty tree is told there is no
// code: the spec reviewer of a greenfield project searched the tree 21 times.
func TestSpecAndPlanOnAnEmptyTreeSayThereIsNoCode(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(root, artifact.KindRequirements), []byte("## REQ-001 — Capture\n\nCapture the screen.\n\n**Priority:** must\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(root, artifact.KindSpec), []byte("## SPEC-001 — Shell\n\n**Implements:** REQ-001\n\nGTK4.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []Name{Spec, Plan} {
		prompt, err := BuildPrompt(root, name, "", &artifact.Document{}, "", false, false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(prompt, "## The project has no code yet") {
			t.Errorf("%s brief on an empty tree does not say so:\n%.400s", name, prompt)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.rs"), []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildPrompt(root, Spec, "", &artifact.Document{}, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "no code yet") {
		t.Fatal("a tree with source was called empty")
	}
}
