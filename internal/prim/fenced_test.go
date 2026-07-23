package prim

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyFencedEdit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.js"), []byte("const MARGIN = 1.5;\nconst OTHER = 9;\n"), 0o644)

	out := "=== FILE: app.js ===\n```search\nconst MARGIN = 1.5;\n```\n```replace\nconst MARGIN = 1.2;\n```\n"
	res := ApplyFencedEdits(dir, out)
	if res.Applied != 1 || len(res.Rejected) != 0 {
		t.Fatalf("fenced apply: %+v", res)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "app.js"))
	if !contains(string(data), "const MARGIN = 1.2;") || !contains(string(data), "const OTHER = 9;") {
		t.Errorf("edit wrong or clobbered unrelated code: %q", data)
	}

	// a search that doesn't match is safely rejected (never a wrong edit)
	miss := "=== FILE: app.js ===\n```search\nconst NOPE = 0;\n```\n```replace\nx\n```\n"
	if res := ApplyFencedEdits(dir, miss); res.Applied != 0 || len(res.Rejected) != 1 {
		t.Errorf("mismatch should be rejected: %+v", res)
	}
}

func TestFencedCreateAndMarkerGuard(t *testing.T) {
	dir := t.TempDir()
	// empty search creates a new file
	out := "=== FILE: web/index.html ===\n```search\n```\n```replace\n<h1>hi</h1>\n```\n"
	if res := ApplyFencedEdits(dir, out); res.Applied != 1 {
		t.Fatalf("fenced create: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "web", "index.html")); err != nil {
		t.Error("new file not created")
	}
	// replace carrying a merge marker is refused
	os.WriteFile(filepath.Join(dir, "a.js"), []byte("x\n"), 0o644)
	bad := "=== FILE: a.js ===\n```search\nx\n```\n```replace\n<<<<<<< HEAD\nx\n```\n"
	if res := ApplyFencedEdits(dir, bad); res.Applied != 0 {
		t.Errorf("marker-bearing replace should be refused: %+v", res)
	}
}

func TestApplyEditsRouting(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("old\n"), 0o644)

	// fenced format routes to fenced apply
	fenced := "=== FILE: f.txt ===\n```search\nold\n```\n```replace\nnew\n```\n"
	if res := ApplyEdits(dir, fenced); res.Applied != 1 {
		t.Errorf("fenced routing: %+v", res)
	}
	// whole-file format (no fences) routes to file-block apply
	whole := "=== FILE: g.txt ===\nwhole content\n"
	if res := ApplyEdits(dir, whole); res.Applied != 1 {
		t.Errorf("whole-file routing: %+v", res)
	}
	if d, _ := os.ReadFile(filepath.Join(dir, "g.txt")); !contains(string(d), "whole content") {
		t.Error("whole-file not written via ApplyEdits")
	}
}
