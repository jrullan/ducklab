package prim

import (
	"strings"
	"testing"
)

func TestIsPlanRejection(t *testing.T) {
	// a stand-pat message with no plan/list/fence is a rejection
	if !IsPlanRejection("B, I appreciate the notes but I'm keeping my plan because it already covers the edge case.") {
		t.Error("stand-pat prose should be a rejection")
	}
	// a revised plan (has a numbered list) is NOT a rejection even with a stray phrase
	revised := "B, thanks. Here is my revised plan:\n1. Edit calc.py\n2. Add a guard"
	if IsPlanRejection(revised) {
		t.Error("a revised plan with a list must not read as rejection")
	}
	// unrelated prose is not a rejection
	if IsPlanRejection("Here are some thoughts about the approach.") {
		t.Error("no stand-pat phrase should not be a rejection")
	}
}

func TestExtractPlan(t *testing.T) {
	handoff := "B, here is my plan for the requirement.\n\n1. Edit calc.py fib to return a\n2. Run tests\n\nLet me know."
	got := ExtractPlan(handoff)
	if !strings.HasPrefix(got, "1. Edit calc.py") {
		t.Errorf("plan extraction lost the framing boundary: %q", got)
	}
	// no structure -> whole text
	if ExtractPlan("just prose") != "just prose" {
		t.Error("unstructured handoff should return whole text")
	}
}
