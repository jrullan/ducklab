package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fs_write_lines exists because of T-058: luna failed fs_patch 28 times on a
// backtick-dense file, was afraid of fs_write's whole-file emission, and the
// policy (correctly) refused shell rewrites. B-059 named the missing middle
// ground: an edit addressed by the line numbers fs_read already displays.
// These tests pin the contract a small operator depends on.

func writeLinesFixture(t *testing.T) (*ExecContext, string) {
	t.Helper()
	root := t.TempDir()
	body := "one\ntwo\nthree\nfour\nfive\n"
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return &ExecContext{ProjectRoot: root}, root
}

func execWriteLines(t *testing.T, ectx *ExecContext, args string) *Result {
	t.Helper()
	res, err := (&FSWriteLines{}).Execute(context.Background(), ectx, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestWriteLinesReplacesTheExactRange(t *testing.T) {
	ectx, root := writeLinesFixture(t)
	res := execWriteLines(t, ectx, `{"path":"f.txt","start":2,"end":3,"first_line":"two","content":"TWO\nAND A HALF\nTHREE"}`)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	got, _ := os.ReadFile(filepath.Join(root, "f.txt"))
	want := "one\nTWO\nAND A HALF\nTHREE\nfour\nfive\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// The success message must warn that numbers below the edit shifted —
	// that is the one mistake every ranged editor makes next.
	if !strings.Contains(res.Content, "shifted") {
		t.Errorf("success message does not warn about shifted line numbers: %s", res.Content)
	}
}

func TestWriteLinesMismatchTeachesTheActualLine(t *testing.T) {
	ectx, root := writeLinesFixture(t)
	res := execWriteLines(t, ectx, `{"path":"f.txt","start":2,"end":2,"first_line":"three","content":"X"}`)
	if !res.IsError {
		t.Fatal("expected refusal on first_line mismatch")
	}
	// The error must carry the REAL content of line 2 — the actual line is
	// the whole repair for an off-by-one.
	if !strings.Contains(res.Content, `"two"`) {
		t.Errorf("error does not teach the actual line content: %s", res.Content)
	}
	got, _ := os.ReadFile(filepath.Join(root, "f.txt"))
	if !strings.HasPrefix(string(got), "one\ntwo\n") {
		t.Errorf("file was modified despite refusal: %q", got)
	}
}

// When the given first_line exists elsewhere in the file, WHERE it went is the
// repair: the numbers shifted (usually by the model's own earlier edit), and
// naming the corrected start/end closes the loop in one call instead of a
// re-read-and-recount round trip.
func TestWriteLinesShiftedRangeNamesTheNewNumbers(t *testing.T) {
	ectx, root := writeLinesFixture(t)
	res := execWriteLines(t, ectx, `{"path":"f.txt","start":2,"end":3,"first_line":"four","content":"X"}`)
	if !res.IsError {
		t.Fatal("expected refusal on first_line mismatch")
	}
	if !strings.Contains(res.Content, "start=4") || !strings.Contains(res.Content, "end=5") {
		t.Errorf("error does not name the shifted range: %s", res.Content)
	}
	got, _ := os.ReadFile(filepath.Join(root, "f.txt"))
	if string(got) != "one\ntwo\nthree\nfour\nfive\n" {
		t.Errorf("file was modified despite refusal: %q", got)
	}
}

func TestWriteLinesEmptyContentDeletes(t *testing.T) {
	ectx, root := writeLinesFixture(t)
	res := execWriteLines(t, ectx, `{"path":"f.txt","start":2,"end":4,"first_line":"two","content":""}`)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	got, _ := os.ReadFile(filepath.Join(root, "f.txt"))
	if string(got) != "one\nfive\n" {
		t.Errorf("got %q, want %q", got, "one\nfive\n")
	}
}

func TestWriteLinesRangePastEOFNamesTheRealLength(t *testing.T) {
	ectx, _ := writeLinesFixture(t)
	res := execWriteLines(t, ectx, `{"path":"f.txt","start":2,"end":99,"first_line":"two","content":"X"}`)
	if !res.IsError {
		t.Fatal("expected refusal past EOF")
	}
	if !strings.Contains(res.Content, "5 lines") {
		t.Errorf("error does not name the file's real length: %s", res.Content)
	}
}

func TestWriteLinesBackwardsRangeRefused(t *testing.T) {
	ectx, _ := writeLinesFixture(t)
	res := execWriteLines(t, ectx, `{"path":"f.txt","start":3,"end":2,"first_line":"three","content":"X"}`)
	if !res.IsError {
		t.Fatal("expected refusal on backwards range")
	}
}

func TestWriteLinesMissingFileTeachesFsWrite(t *testing.T) {
	ectx, _ := writeLinesFixture(t)
	res := execWriteLines(t, ectx, `{"path":"nope.txt","start":1,"end":1,"first_line":"x","content":"X"}`)
	if !res.IsError {
		t.Fatal("expected refusal on missing file")
	}
	if !strings.Contains(res.Content, "fs_write creates") {
		t.Errorf("error does not point at fs_write for new files: %s", res.Content)
	}
}

func TestWriteLinesPreservesNoTrailingNewline(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("one\ntwo"), 0o644); err != nil {
		t.Fatal(err)
	}
	ectx := &ExecContext{ProjectRoot: root}
	res := execWriteLines(t, ectx, `{"path":"f.txt","start":1,"end":1,"first_line":"one","content":"ONE"}`)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	got, _ := os.ReadFile(filepath.Join(root, "f.txt"))
	if string(got) != "ONE\ntwo" {
		t.Errorf("got %q, want %q (no trailing newline invented)", got, "ONE\ntwo")
	}
}

func TestWriteLinesNumbersMatchFsRead(t *testing.T) {
	// The whole point: the numbers fs_read displays ARE the addresses
	// fs_write_lines accepts. Read line 3, edit line 3, get line 3.
	ectx, root := writeLinesFixture(t)
	read, err := (&FSRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"path":"f.txt"}`))
	if err != nil || read.IsError {
		t.Fatalf("fs_read failed: %v %v", err, read)
	}
	if !strings.Contains(read.Content, "3\tthree") {
		t.Fatalf("fs_read numbering changed, fixture invalid: %q", read.Content)
	}
	res := execWriteLines(t, ectx, `{"path":"f.txt","start":3,"end":3,"first_line":"three","content":"THREE"}`)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	got, _ := os.ReadFile(filepath.Join(root, "f.txt"))
	if string(got) != "one\ntwo\nTHREE\nfour\nfive\n" {
		t.Errorf("got %q", got)
	}
}
