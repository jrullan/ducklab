package strategy

import (
	"fmt"
	"strings"
	"testing"
)

// verifyRunOutput is the shape of the failed verify_run in B-544: a header,
// hundreds of passing tests, one failure in the middle of a runner that keeps
// going, more passes, and the runner's summary at the end.
func verifyRunOutput() string {
	var b strings.Builder
	b.WriteString("gate: tests\ncmd: npm test --silent\nexit: 1\n[gate] env ok (python3, browsers, v22.22.2)\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "PASS case-%03d\n", i)
	}
	b.WriteString("FAIL second-hex: SECOND['1'] expected 'hexA' got 'hexD'\n")
	for i := 300; i < 900; i++ {
		fmt.Fprintf(&b, "PASS case-%03d\n", i)
	}
	b.WriteString("Traceback (most recent call last):\n  File \"test/e2e.py\", line 88, in <module>\nAssertionError: legend r6c1 expected 'A' got 'D'\n")
	b.WriteString("900 passed, 2 failed\n")
	return b.String()
}

// B-544. A failed verify_run showed the gate header, the first PASS lines and
// "… 29751 bytes truncated" — never the failure, which the model saw and the
// person reading the log did not.
func TestAFailedResultKeepsWhatExplainsIt(t *testing.T) {
	out := verifyRunOutput()
	got := SummariseToolResult(out, true)

	for _, want := range []string{
		"gate: tests", "cmd: npm test --silent", "exit: 1", // what ran, how it ended
		"FAIL second-hex: SECOND['1'] expected 'hexA' got 'hexD'", // the failure in the middle
		"AssertionError: legend r6c1 expected 'A' got 'D'",        // the tail
		"900 passed, 2 failed",
		"bytes omitted",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "PASS case-000") || strings.Contains(got, "PASS case-299") {
		t.Errorf("the summary kept passing tests from the head:\n%s", got)
	}
	// Bounded (I3): header + failure lines + tail, never the whole output.
	bound := maxErrorHeaderLines*(maxErrorLineBytes+2) + (2*maxErrorFailureLines+1)*(maxErrorLineBytes+32) + maxErrorTailBytes
	if len(got) > bound || len(got) >= len(out) {
		t.Errorf("summary is %d bytes of %d; bound %d", len(got), len(out), bound)
	}
	// The cuts are said out loud, in order: header, omission, failure, omission, tail.
	if i, j := strings.Index(got, "exit: 1"), strings.Index(got, "FAIL second-hex"); !(i >= 0 && j > i && strings.Contains(got[i:j], "bytes omitted")) {
		t.Errorf("the cut between the header and the failure is not marked:\n%s", got)
	}
}

// An error that fits is kept whole, and a result that is not an error keeps
// the short head-only summary the log always had.
func TestShortErrorsAndOrdinaryResultsAreUnchanged(t *testing.T) {
	short := "gate: tests\nexit: 1\nFAIL one"
	if got := SummariseToolResult(short, true); got != short {
		t.Errorf("a short error was rewritten: %q", got)
	}
	long := strings.Repeat("x", 5000)
	got := SummariseToolResult(long, false)
	if len(got) > maxToolResultBytes+64 || !strings.Contains(got, "truncated") || !strings.HasPrefix(got, "xxx") {
		t.Errorf("an ordinary result's summary changed: %d bytes", len(got))
	}
}

// Many failures do not unbound the summary: the rescued lines are capped, and
// a single enormous line is clipped.
func TestAFailureFloodStaysBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("exit: 1\n")
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "FAIL case-%d %s\n", i, strings.Repeat("y", 400))
	}
	got := SummariseToolResult(b.String(), true)
	if n := strings.Count(got[:len(got)-maxErrorTailBytes], "FAIL case-"); n > maxErrorFailureLines {
		t.Errorf("kept %d failure lines from the middle, want at most %d", n, maxErrorFailureLines)
	}
	if len(got) > 12_000 {
		t.Errorf("a failure flood produced a %d-byte summary", len(got))
	}
}
