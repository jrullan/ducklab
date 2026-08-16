package service

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/verify"
)

const testDiff = "diff --git a/add_test.go b/add_test.go\n--- /dev/null\n+++ b/add_test.go\n@@ -0,0 +1,3 @@\n+func TestAdd(t *testing.T) {\n"
const codeDiff = "diff --git a/add.go b/add.go\n--- a/add.go\n+++ b/add.go\n@@ -1 +1 @@\n-a\n+b\n"

func green() *verify.Result { return &verify.Result{ExitCode: 0} }
func red() *verify.Result   { return &verify.Result{ExitCode: 1} }

// The case the flow exists for: the suite was green, a test was written, and
// now it is red. That is a test that specifies work which does not exist.
func TestAGreenGateTurnedRedIsWhatWeWant(t *testing.T) {
	verdict, detail := judgeTestFirst(green(), red(), testDiff, nil)
	if verdict != "PASSED" {
		t.Errorf("verdict = %s (%s)", verdict, detail)
	}
	if !strings.Contains(detail, "add_test.go") {
		t.Errorf("the detail does not name the test: %q", detail)
	}
}

// The trap. A test that passes against code that does not exist has asserted
// nothing, and accepting it would install a permanent false green — the gate
// would stay green forever regardless of the implementation.
func TestAStillGreenGateFailsTheRun(t *testing.T) {
	verdict, detail := judgeTestFirst(green(), green(), testDiff, nil)
	if verdict != "FAILED" {
		t.Errorf("a test that changed nothing was accepted: %s", verdict)
	}
	if !strings.Contains(detail, "asserts nothing") {
		t.Errorf("the detail does not say what is wrong: %q", detail)
	}
}

// A passing frontend test is not a vacuous specification when the configured
// gate never executes frontend/. Diagnose the coverage boundary explicitly so
// a redo note does not falsely blame the assertion.
func TestGreenFrontendTestOutsideGateIsCoverageError(t *testing.T) {
	diff := "diff --git a/frontend/src/recovery.test.tsx b/frontend/src/recovery.test.tsx\n--- /dev/null\n+++ b/frontend/src/recovery.test.tsx\n@@ -0,0 +1 @@\n+test('recovery control', () => {})\n"
	verdict, detail := judgeTestFirst(green(), green(), diff, nil)
	if verdict != "FAILED" {
		t.Fatalf("verdict = %s, want FAILED while diagnosing uncovered test", verdict)
	}
	for _, want := range []string{"frontend/", "gate never runs", "widen the gate or move the test"} {
		if !strings.Contains(detail, want) {
			t.Errorf("coverage diagnosis lacks %q: %q", want, detail)
		}
	}
}

// "It is red now" proves nothing when it was red before (05 §5.2). Saying so
// beats claiming a result the run cannot support.
func TestAnAlreadyRedGateIsUnverifiedNotPassed(t *testing.T) {
	verdict, detail := judgeTestFirst(red(), red(), testDiff, nil)
	if verdict != "UNVERIFIED" {
		t.Errorf("verdict = %s, want UNVERIFIED", verdict)
	}
	if !strings.Contains(detail, "already red") {
		t.Errorf("the detail does not say why it is unverified: %q", detail)
	}
}

// A run that wrote no test specified nothing, whatever the gate says.
func TestNoTestWrittenIsAFailure(t *testing.T) {
	for _, diff := range []string{"", codeDiff} {
		if verdict, _ := judgeTestFirst(green(), red(), diff, nil); verdict != "FAILED" {
			t.Errorf("verdict = %s for a diff with no test in it", verdict)
		}
	}
}

// A project that says where its tests live must be obeyed here too, or a
// correct test is judged as no test at all.
func TestTheProjectsOwnTestGlobsDecide(t *testing.T) {
	diff := "diff --git a/checks/thing.go b/checks/thing.go\n--- /dev/null\n+++ b/checks/thing.go\n@@ -0,0 +1 @@\n+x\n"
	if verdict, detail := judgeTestFirst(green(), red(), diff, []string{"checks/**"}); verdict != "PASSED" {
		t.Errorf("verdict = %s (%s)", verdict, detail)
	}
}

// The prompt must say the two things that are enforced rather than requested,
// so a refusal reads as a rule and not as a broken tool.
func TestThePromptSaysWhatIsEnforced(t *testing.T) {
	got := testFirstPrompt("## Your task\n\nT-001 — Add a thing", "go test ./...")
	for _, want := range []string{
		"Do not implement",
		"must **fail**",
		"go test ./...",
		"refuse any path that is not a test file",
		// A decision the task leaves open gets baked into the assertions and
		// becomes the de-facto spec — the prompt must send it to the person
		// instead. A model burned 2M tokens deliberating where a "week"
		// starts, with ask_human sitting unused in its toolbelt.
		"ask_human",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt does not say %q:\n%s", want, got)
		}
	}
	// And it carries the task, or the model is writing a test for nothing.
	if !strings.Contains(got, "T-001") {
		t.Error("the prompt lost the task")
	}
}

// Test-first needs a gate that runs a test suite. Nothing else gives a test
// anything to hook into.
//
// Found by clicking it on a project whose gate is a custom script: the model
// explored, understood the task, and then tried to patch the gate script
// itself to add assertions — because in that project the gate script is the
// only place an assertion could live. The write guard refused it, correctly,
// and the run had nowhere left to go.
func TestTestFirstNeedsATestGate(t *testing.T) {
	for _, mode := range []string{"none", "build", "lint", "custom", ""} {
		if err := checkTestGate(mode); err == nil {
			t.Errorf("mode %q was accepted; a test written under it changes nothing", mode)
		} else if !strings.Contains(err.Error(), "verify.mode tests") {
			t.Errorf("mode %q: the error does not say how to fix it: %v", mode, err)
		}
	}
	if err := checkTestGate("tests"); err != nil {
		t.Errorf("a tests gate was refused: %v", err)
	}
}
