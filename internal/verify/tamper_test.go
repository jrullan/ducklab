package verify

import (
	"strings"
	"testing"
)

const mixedDiff = `diff --git a/mathutil.go b/mathutil.go
--- a/mathutil.go
+++ b/mathutil.go
@@ -1,3 +1,3 @@
-func Double(n int) int { return n * 2 }
+func Double(n int) int { return n * 3 }
diff --git a/mathutil_test.go b/mathutil_test.go
--- a/mathutil_test.go
+++ b/mathutil_test.go
@@ -1,3 +1,3 @@
-	if Double(2) != 4 {
+	if Double(2) != 6 {
`

// The case the guard exists for: a change that edits the code and the test
// that would have caught it, in one diff, with a task that never mentioned
// tests. The gate goes green either way.
func TestATestEditedByATaskThatNeverAskedIsFlagged(t *testing.T) {
	got := CheckTampering(mixedDiff, "Make Double correct for negative numbers.", nil)
	if !got.Flagged() {
		t.Fatal("a silent test edit was not flagged")
	}
	if len(got.Files) != 1 || got.Files[0] != "mathutil_test.go" {
		t.Errorf("Files = %v, want just the test file", got.Files)
	}
	if strings.Contains(got.Hunks, "mathutil.go b/mathutil.go") {
		t.Error("Hunks carried the production file; the point is to show the test hunks alone")
	}
	if !strings.Contains(got.Hunks, "Double(2) != 6") {
		t.Error("Hunks lost the change a reader needs to see")
	}
}

// A warning that is always on is a warning nobody reads.
func TestATaskThatAsksForTestsIsNotFlagged(t *testing.T) {
	got := CheckTampering(mixedDiff, "Add tests for Double.", nil)
	if got.Flagged() {
		t.Error("flagged a test edit the task asked for")
	}
	if len(got.Files) != 1 {
		t.Errorf("Files = %v — the files are still reported, only the flag is off", got.Files)
	}
}

func TestADiffThatTouchesNoTestsIsNotFlagged(t *testing.T) {
	diff := "diff --git a/mathutil.go b/mathutil.go\n--- a/mathutil.go\n+++ b/mathutil.go\n@@ -1 +1 @@\n-a\n+b\n"
	if got := CheckTampering(diff, "Change Double.", nil); got.Flagged() || got.Hunks != "" {
		t.Errorf("flagged a diff with no test in it: %+v", got)
	}
}

func TestEmptyDiff(t *testing.T) {
	if got := CheckTampering("", "anything", nil); got.Flagged() || len(got.Files) != 0 {
		t.Errorf("empty diff produced %+v", got)
	}
}

// Tests do not all look like Go's. A project whose suite lives under tests/ is
// exactly as vulnerable.
func TestRecognisesTestPathsBeyondGo(t *testing.T) {
	for _, f := range []string{
		"foo_test.go", "test_thing.py", "thing_test.py",
		"src/app.test.ts", "src/app.test.tsx", "lib/x.spec.ts",
		"tests/conftest.py", "test/helper.rb", "src/__tests__/a.js",
		"backend/tests/deep/nested/case.py",
	} {
		if !matchesAny(f, DefaultTestGlobs) {
			t.Errorf("%s was not recognised as a test", f)
		}
	}
	for _, f := range []string{
		"mathutil.go", "testing_helpers.go", "src/contest.ts", "latest.py", "tests.go",
	} {
		if matchesAny(f, DefaultTestGlobs) {
			t.Errorf("%s was treated as a test", f)
		}
	}
}

// A project may say where its tests live; when it does, that wins.
func TestProjectGlobsReplaceTheDefaults(t *testing.T) {
	diff := "diff --git a/checks/thing.go b/checks/thing.go\n--- a/checks/thing.go\n+++ b/checks/thing.go\n@@ -1 +1 @@\n-a\n+b\n"
	if got := CheckTampering(diff, "Change it.", []string{"checks/**"}); !got.Flagged() {
		t.Error("a project's own test glob was ignored")
	}
	if got := CheckTampering(mixedDiff, "Change it.", []string{"checks/**"}); got.Flagged() {
		t.Error("the defaults still applied after the project named its own")
	}
}

// "latest" is not a test, and neither is "contest". A flag that fires on those
// is a flag that gets ignored on the ones that matter.
func TestMentionsTests(t *testing.T) {
	for _, s := range []string{
		"Add tests for Double", "write a test", "improve coverage",
		"update the spec", "fix the assertion", "add a fixture",
	} {
		if !MentionsTests(s) {
			t.Errorf("MentionsTests(%q) = false", s)
		}
	}
	for _, s := range []string{
		"Use the latest version", "run the contest", "attest to the change", "",
	} {
		if MentionsTests(s) {
			t.Errorf("MentionsTests(%q) = true", s)
		}
	}
}

// A rename is how a test stops running without a line of it changing.
func TestARenamedTestIsReported(t *testing.T) {
	diff := "diff --git a/mathutil_test.go b/mathutil_old.go\nsimilarity index 100%\nrename from mathutil_test.go\nrename to mathutil_old.go\n"
	got := CheckTampering(diff, "Tidy up the package.", nil)
	if !got.Flagged() {
		t.Error("a test renamed out of the suite was not flagged")
	}
	if len(got.Files) != 1 || got.Files[0] != "mathutil_test.go" {
		t.Errorf("Files = %v, want the test's own name", got.Files)
	}
}

// A task that names what it delivers is not a task about tests.
//
// The spine writes `**Implements:** SPEC-001` into the body of every properly
// traced task, and `spec` is one of the words that says "the human asked for
// test work". So the guard switched itself off on exactly the tasks that are
// best documented. Found by running a real task whose model edited the test
// that would have caught it, and getting no flag.
func TestATraceabilityIDIsNotARequestForTests(t *testing.T) {
	for _, s := range []string{
		"Make Add multiply instead\n**Implements:** SPEC-001\nChange Add in add.go.",
		"Fix the parser\n\n**Implements:** REQ-014, SPEC-003",
		"**Implements:** TEST-002",
	} {
		if MentionsTests(s) {
			t.Errorf("MentionsTests(%q) = true — an ID reference is not an ask", s)
		}
	}
	// The words still count when they are words.
	for _, s := range []string{
		"Update the spec for SPEC-001",
		"**Implements:** SPEC-001. Also add tests.",
	} {
		if !MentionsTests(s) {
			t.Errorf("MentionsTests(%q) = false — a real ask was swallowed", s)
		}
	}
}
