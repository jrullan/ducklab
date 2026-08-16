package service

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/verify"
)

// The wound from B-028: T-018's test-first run wrote a test referencing an
// undefined type (RunView). `go test` exited nonzero — a COMPILE error, not an
// assertion failure — and judgeTestFirst read that as "the gate is red, the
// spec is valid" and returned PASSED. The chained build then inherited a tree
// that would not compile, every other test in the package stopped running, and
// the build's job silently changed from "turn one red assertion green" into
// "make the tree compile again" — 43 minutes and 55 failed patches later the
// run died FAILED.
//
// Compile-red is not assertion-red. The judge must distinguish the two: a
// compile error is a malformed specification that bounces back to the
// test-writer with the compiler's message, exactly like a contract repair. A
// test that compiles and fails on an assertion is the valid red specification
// the flow exists to accept.

// compileRed is a gate result whose output shows `go test` failed at BUILD
// time — the package did not compile. This is what `go test ./...` prints when
// a test file references an undefined type, exactly as T-018 did.
func compileRed() *verify.Result {
	return &verify.Result{
		ExitCode: 1,
		Output: "# github.com/jrullan/ducklab/internal/service\n" +
			"./redo_cleanup_cap_test.go:10:5: undefined: RunView\n" +
			"FAIL\tgithub.com/jrullan/ducklab/internal/service [build failed]\n",
	}
}

// assertionRed is a gate result whose output shows `go test` compiled the
// package and a test FAILED on an assertion. This is the valid red: the test
// specifies work that does not exist yet.
func assertionRed() *verify.Result {
	return &verify.Result{
		ExitCode: 1,
		Output: "--- FAIL: TestRedoCleanupCap (0.00s)\n" +
			"    redo_cleanup_cap_test.go:15: expected 3, got 0\n" +
			"FAIL\n" +
			"FAIL\tgithub.com/jrullan/ducklab/internal/service\n",
	}
}

// A compile-red gate is not a valid specification. The package did not build,
// so no test actually ran — the failure is in the test writer's Go, not in the
// code under test. Accepting it would install a broken tree and derail the
// chained build into "make it compile" instead of "make the assertion pass."
func TestACompileRedGateIsNotAValidSpecification(t *testing.T) {
	verdict, detail := judgeTestFirst(green(), compileRed(), testDiff, nil)
	if verdict == "PASSED" {
		t.Fatalf("a compile-red gate was accepted as a valid spec: %s", detail)
	}
}

// The compiler's message must reach the test writer, so they can fix the
// malformed test without reading the raw gate log — exactly like a contract
// repair returns the parse error to the model. "undefined: RunView" is the
// exact error that sank T-018.
func TestACompileRedBouncesBackWithTheCompilersMessage(t *testing.T) {
	_, detail := judgeTestFirst(green(), compileRed(), testDiff, nil)
	if !strings.Contains(detail, "undefined: RunView") {
		t.Errorf("the detail does not carry the compiler's message:\n%s", detail)
	}
}

// An assertion-red gate — the package compiled, a test ran and failed on an
// assertion — IS the valid specification the flow exists to accept. This is
// the case that must still pass after the compile-red guard is added.
func TestAnAssertionRedGateIsAValidSpecification(t *testing.T) {
	verdict, detail := judgeTestFirst(green(), assertionRed(), testDiff, nil)
	if verdict != "PASSED" {
		t.Errorf("an assertion-red gate was rejected: %s", detail)
	}
}

// A red gate with no compile-error markers in its output is assertion-red by
// default. The existing red() helper has empty Output, and projects whose gate
// output lacks Go's `[build failed]` marker must not be silently rejected.
// This guards backward compatibility: the compile-red check narrows the
// verdict only when the output actually says the build failed.
func TestARedGateWithNoCompileMarkersIsAssertionRed(t *testing.T) {
	verdict, _ := judgeTestFirst(green(), red(), testDiff, nil)
	if verdict != "PASSED" {
		t.Error("a red gate with no output was rejected as compile-red")
	}
}

// A compile error is malformed regardless of whether the suite was already
// red. "It was red before" does not make a test that does not compile into a
// valid specification — the compile error still breaks the package and still
// must bounce back with the compiler's message. The compile-red check must
// take precedence over the already-red guard, not hide behind it.
func TestACompileRedIsMalformedEvenIfTheSuiteWasAlreadyRed(t *testing.T) {
	verdict, detail := judgeTestFirst(red(), compileRed(), testDiff, nil)
	if verdict == "PASSED" {
		t.Fatalf("a compile-red gate was accepted despite the suite being already red: %s", detail)
	}
	if !strings.Contains(detail, "undefined: RunView") {
		t.Errorf("the detail does not carry the compiler's message:\n%s", detail)
	}
}
