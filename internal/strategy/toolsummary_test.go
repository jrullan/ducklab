package strategy

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
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
	// The first few lines after the header are kept (a build's errors open
	// the output); the hundreds of passes after them are not.
	if strings.Contains(got, "PASS case-100") || strings.Contains(got, "PASS case-299") {
		t.Errorf("the summary kept passing tests from the head:\n%s", got)
	}
	// Bounded (I3): header + failure lines + tail, never the whole output.
	bound := maxErrorHeaderLines*(maxErrorLineBytes+2) + (maxErrorLeadLines+2*maxErrorFailureLines+1)*(maxErrorLineBytes+32) + maxErrorTailBytes
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
	if n := strings.Count(got[:len(got)-maxErrorTailBytes], "FAIL case-"); n > maxErrorLeadLines+maxErrorFailureLines {
		t.Errorf("kept %d lines from the middle, want at most %d", n, maxErrorLeadLines+maxErrorFailureLines)
	}
	if len(got) > 12_000 {
		t.Errorf("a failure flood produced a %d-byte summary", len(got))
	}
}

// Review of B-544: a Go build fails before any test runs, and its errors
// open the output and name only a position. The head-only summary had kept
// them; the first error-aware one dropped them between the header and a tail
// of "ok pkg" lines.
func TestABuildFailureKeepsItsErrors(t *testing.T) {
	var b strings.Builder
	b.WriteString("gate: tests\ncmd: go test ./...\nexit: 1\n")
	b.WriteString("# example.com/app\n./foo.go:12:3: undefined: Bar\n./foo.go:20:9: cannot use x (variable of type int) as string value\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "ok  \texample.com/pkg%03d\t0.01s\n", i)
	}
	b.WriteString("FAIL\texample.com/app [build failed]\n")
	got := SummariseToolResult(b.String(), true)
	for _, want := range []string{"./foo.go:12:3: undefined: Bar", "cannot use x", "[build failed]"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary lost %q:\n%s", want, got)
		}
	}
}

// Review of B-544: cuts land on character boundaries, and a clipped line
// says how much it lost.
func TestCutsKeepCharactersWhole(t *testing.T) {
	line := "FAIL " + strings.Repeat("é", 400)
	s := "exit: 1\n" + strings.Repeat("ñ", 3000) + "\n" + line + "\n" + strings.Repeat("ü", 1000)
	got := SummariseToolResult(s, true)
	if !utf8.ValidString(got) {
		t.Fatal("the summary split a character")
	}
	if !strings.Contains(got, "bytes)") {
		t.Errorf("a clipped line does not say what it lost:\n%.300s", got)
	}
	if !utf8.ValidString(tailOf(strings.Repeat("é", 600), 513)) {
		t.Error("tailOf split a character")
	}
}

// Review of B-544: a resume note holds many calls in 16 KB. An error there
// keeps its tail, at the old size, and the last-resort shrink keeps the tail
// too — the head would bring the bug back on the resume path.
func TestACheckpointKeepsAnErrorsTail(t *testing.T) {
	out := verifyRunOutput()
	got := tailOf(strings.TrimSpace(out), maxToolResultBytes)
	if len(got) > maxToolResultBytes+64 || !strings.Contains(got, "900 passed, 2 failed") {
		t.Errorf("checkpoint error summary = %d bytes:\n%s", len(got), got)
	}
}
