package cli

import (
	"bytes"
	"strings"
	"testing"
)

// B-548: a bare intake is an interview; it starts only when a person is there
// to answer and says yes.
func TestBriefLessIntakeGo(t *testing.T) {
	cases := []struct {
		name      string
		yes, tty  bool
		input     string
		wantOK    bool
		wantCode  int
		wantInErr string
		wantInOut string
	}{
		{name: "yes skips the interview", yes: true, tty: true, wantCode: 2, wantInErr: "--yes skips the interview"},
		{name: "piped caller", tty: false, wantCode: 2, wantInErr: "needs a person at a terminal"},
		{name: "person confirms", tty: true, input: "y\n", wantOK: true, wantInOut: "Start it?"},
		{name: "person confirms in full", tty: true, input: "YES\n", wantOK: true},
		{name: "person declines", tty: true, input: "n\n", wantInOut: "not started"},
		{name: "enter declines", tty: true, input: "\n", wantInOut: "not started"},
		{name: "eof declines", tty: true, input: "", wantInOut: "not started"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errw bytes.Buffer
			code, ok := briefLessIntakeGo(c.yes, c.tty, strings.NewReader(c.input), &out, &errw)
			if ok != c.wantOK || code != c.wantCode {
				t.Fatalf("got (%d, %v), want (%d, %v)", code, ok, c.wantCode, c.wantOK)
			}
			if c.wantInErr != "" {
				if !strings.Contains(errw.String(), c.wantInErr) || !strings.Contains(errw.String(), "usage: ducklab intake") {
					t.Errorf("stderr = %q, want %q and the usage", errw.String(), c.wantInErr)
				}
				if out.Len() != 0 {
					t.Errorf("a refusal prompted: %q", out.String())
				}
			}
			if c.wantInOut != "" && !strings.Contains(out.String(), c.wantInOut) {
				t.Errorf("stdout = %q, want %q", out.String(), c.wantInOut)
			}
		})
	}
}

// The refusal happens before any engine contact: no project is needed for a
// piped bare intake to print its usage and stop.
func TestBareIntakeOverAPipeStartsNothing(t *testing.T) {
	saved := interactiveTerminal
	interactiveTerminal = func() bool { return false }
	t.Cleanup(func() { interactiveTerminal = saved })
	if code := stageCmd("intake", nil, t.TempDir()); code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
}

// A background --ref does not say what to build; --yes with only one is
// refused before the engine is contacted.
func TestYesIntakeWithOnlyABackgroundRefStartsNothing(t *testing.T) {
	if code := stageCmd("intake", []string{"--yes", "--ref", "notes.md"}, t.TempDir()); code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
}
