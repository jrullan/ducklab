package agent

import (
	"strings"
	"testing"
)

// A defect that lives in three files is one finding, not three rounds. The
// schema admits a class-level finding — file "*" plus the invariant it
// violates — and the reviewer's brief tells it to use one (B-286).
func TestVerdictContractAdmitsAClassLevelFinding(t *testing.T) {
	text := `{"verdict":"request-changes","findings":[
		{"severity":"major","file":"*","invariant":"the published ref is the ref acceptance advances",
		 "issue":"three call sites each pick their own branch to push","fix":"define the ref once and pass it explicitly"},
		{"severity":"minor","invariant":"no current-branch fallbacks","issue":"a fallback remains in Push","fix":"delete it"},
		{"severity":"minor","file":"remote.go","line":12,"issue":"unused import","fix":"remove it"}]}`
	got, err := ParseContract("verdict", text)
	if err != nil {
		t.Fatal(err)
	}
	v := got.(*Verdict)
	if len(v.Findings) != 3 {
		t.Fatalf("got %d findings, want 3", len(v.Findings))
	}
	if !v.Findings[0].ClassLevel() || !v.Findings[1].ClassLevel() || v.Findings[2].ClassLevel() {
		t.Fatalf("class-level detection: %+v", v.Findings)
	}
	if v.Findings[0].Invariant == "" {
		t.Fatal("the invariant was dropped")
	}
	if len(v.Blocking()) != 1 {
		t.Fatalf("blocking = %d, want 1", len(v.Blocking()))
	}
}

func TestAClassLevelFindingMustNameItsInvariant(t *testing.T) {
	text := `{"verdict":"request-changes","findings":[{"severity":"major","file":"*","issue":"it is broken everywhere","fix":"fix it"}]}`
	if _, err := ParseContract("verdict", text); err == nil || !strings.Contains(err.Error(), "invariant") {
		t.Fatalf("err = %v, want a refusal naming the missing invariant", err)
	}
}

// The three parts of a role must agree (getRolePrompt): the schema admits a
// class finding, so the brief must teach it, and the round discipline that
// makes reviews converge must be in the brief, not in a memo nobody reads.
func TestReviewerBriefTeachesRoundDisciplineAndClassFindings(t *testing.T) {
	for _, want := range []string{"INVARIANTS", `"file":"*"`, "class-level", "re-verify", "contradicts a fix"} {
		if !strings.Contains(reviewerPrompt, want) {
			t.Errorf("reviewer brief lacks %q", want)
		}
	}
}
