package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A model asked fs_read for lines 93 to 78 — a range that reads backwards — and
// `lines[92:78]` panicked. The panic killed the goroutine running the turn and
// took the whole run down with it, reported as ABORTED with a message that
// named no location.
//
// Each bound was clamped on its own and nothing checked that the range ran
// forwards.
func TestABackwardsLineRangeDoesNotPanic(t *testing.T) {
	body := strings.Repeat("a line\n", 200)
	// The exact numbers from the run that crashed.
	if got := TruncateLines(body, 93, 78); got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}

func TestALineRangeStillWorksForwards(t *testing.T) {
	body := "one\ntwo\nthree\nfour\n"
	if got := TruncateLines(body, 2, 3); got != "two\nthree" {
		t.Errorf("got %q", got)
	}
}

// Returning nothing would tell the model those lines are blank and send it
// looking at the wrong thing. The call is what is wrong, so say so.
func TestFSReadNamesABackwardsRange(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"),
		[]byte(strings.Repeat("x\n", 200)), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := (&FSRead{}).Execute(context.Background(),
		&ExecContext{ProjectRoot: root},
		json.RawMessage(`{"path":"index.html","start":93,"end":78}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("a backwards range was accepted: %q", res.Content)
	}
	for _, want := range []string{"93", "78"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("the error does not name the bounds: %q", res.Content)
		}
	}
}
