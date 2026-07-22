package prim

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello, World!":       "hello-world",
		"  多 spaces  here  ":  "spaces-here",
		"already-a-slug":      "already-a-slug",
		"CamelCase_and-1.2.3": "camelcase-and-1-2-3",
		"---trim---":          "trim",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateMiddle(t *testing.T) {
	if got := TruncateMiddle("short", 20); got != "short" {
		t.Errorf("no-op truncation changed text: %q", got)
	}
	got := TruncateMiddle("abcdefghij", 5)
	if []rune(got)[2] != '…' {
		t.Errorf("expected ellipsis in middle, got %q", got)
	}
	if len([]rune(got)) != 5 {
		t.Errorf("truncated length = %d, want 5 (%q)", len([]rune(got)), got)
	}
	if TruncateMiddle("abc", 0) != "" {
		t.Errorf("maxLen 0 should yield empty")
	}
}

func TestFilePathsAndApply(t *testing.T) {
	out := "=== FILE: a/b.txt ===\nline1\nline2\n=== FILE: c.txt ===\nhi\n=== FILE: a/b.txt ===\ndup\n"
	paths := FilePaths(out)
	if len(paths) != 2 || paths[0] != "a/b.txt" || paths[1] != "c.txt" {
		t.Fatalf("FilePaths = %v", paths)
	}
	dir := t.TempDir()
	n, err := ApplyFileBlocks(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("wrote %d blocks, want 3", n)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "c.txt"))
	if string(data) != "hi\n" {
		t.Errorf("c.txt = %q", data)
	}
}

func TestApplyFileBlocksEmpty(t *testing.T) {
	if _, err := ApplyFileBlocks(t.TempDir(), "no blocks here"); err == nil {
		t.Error("expected error on output with no FILE blocks")
	}
}

func TestApplySearchReplace(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f.go"), []byte("package x\n\nfunc a() int { return 1 }\n"), 0o644)

	// success
	out := "=== FILE: f.go ===\n<<< SEARCH\nreturn 1\n===\nreturn 2\n>>> REPLACE\n"
	res := ApplySearchReplace(dir, out)
	if res.Applied != 1 || len(res.Rejected) != 0 {
		t.Fatalf("applied=%d rejected=%v", res.Applied, res.Rejected)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "f.go"))
	if want := "func a() int { return 2 }"; !contains(string(data), want) {
		t.Errorf("file not edited: %q", data)
	}

	// search miss -> rejected, file untouched
	miss := "=== FILE: f.go ===\n<<< SEARCH\nreturn 99\n===\nreturn 3\n>>> REPLACE\n"
	res = ApplySearchReplace(dir, miss)
	if res.Applied != 0 || len(res.Rejected) != 1 {
		t.Errorf("expected rejection, got applied=%d rejected=%v", res.Applied, res.Rejected)
	}

	// unrelated code preserved
	if !contains(string(data), "package x") {
		t.Errorf("unrelated code lost")
	}
}

func TestDecisionAndJudge(t *testing.T) {
	if d := Decision("blah\n### DECISION: A\nmore"); d != "A" {
		t.Errorf("Decision=%q", d)
	}
	if d := Decision("**DECISION:** hybrid"); d != "HYBRID" {
		t.Errorf("Decision=%q", d)
	}
	if d := Decision("no verdict here"); d != "HYBRID" {
		t.Errorf("default should be HYBRID, got %q", d)
	}
	if d := Decision("DECISION NONE — neither works"); d != "NONE" {
		t.Errorf("Decision=%q", d)
	}

	rep := ParseJudge("## SOLUTION A\nlooks ok\nBLOCKING FINDING: deletes tests\n## SOLUTION B\nfine\nDECISION: A")
	if rep.Decision != "A" {
		t.Errorf("decision=%q", rep.Decision)
	}
	if !rep.Blocking["A"] {
		t.Errorf("expected A flagged blocking, got %v", rep.Blocking)
	}
	if rep.Blocking["B"] {
		t.Errorf("B should not be blocking")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
