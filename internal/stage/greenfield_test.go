package stage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// A new project has nothing to read, and the intake brief says so — the
// alternative was a small model spending its first eight tool calls
// surveying an empty tree and re-reading absent artifacts.
func TestAGreenfieldIntakeTellsTheArchitectNotToExplore(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{".ducklab/docs", ".ducklab/runs", ".git"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".ducklab/runs/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildPrompt(root, Intake, "a screen capture tool", &artifact.Document{}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "## The project is empty") || !strings.Contains(prompt, "Do not explore the tree") {
		t.Fatalf("greenfield intake does not say the tree is empty:\n%s", prompt)
	}

	// One source file and the notice is gone: there is something to read.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt, err = BuildPrompt(root, Intake, "a screen capture tool", &artifact.Document{}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "## The project is empty") {
		t.Fatal("a tree with source was called empty")
	}
}
