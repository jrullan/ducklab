package service

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestTaskWritableLanePreservesExactFilesAndOwnsTrees(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, ".ducklab", "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := `---
kind: plan
grammar: 2
---
# Plan

## M-001 — Milestone

### T-001 — Exact and tree lane
**Owns:** src/core
**Produces:** file:README.md, dir:fixtures
`
	if err := os.WriteFile(filepath.Join(docs, "plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	files, dirs := taskWritableLane(root, "T-001")
	if !slices.Equal(files, []string{"README.md"}) {
		t.Fatalf("files = %v", files)
	}
	for _, want := range []string{"src/core", "fixtures"} {
		if !slices.Contains(dirs, want) {
			t.Errorf("dirs = %v; missing %q", dirs, want)
		}
	}
}
