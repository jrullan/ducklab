package service

import (
	"strings"
	"testing"
)

// The prompt told the model the spec section was its job.
//
// It said "This task delivers SPEC-003" and then handed over the whole
// section. Every section in a real plan is delivered by several tasks — one
// project had five on SPEC-002 — so the sentence was false, and the model
// reasonably read the section as its scope.
//
// It happened twice in one session. T-003 implemented T-004's geometry
// calculations as well, T-005 implemented T-006's labels, and both times the
// gate went green because the code was correct. Nothing was red; the next run
// simply found nothing left to do.
//
// This tests the wording, which is the whole mechanism: the fix is that the
// model is told what is not its part, by name.
func TestScopeWordingNamesTheSiblings(t *testing.T) {
	siblings := []TaskView{
		{ID: "T-004", Title: "Implement geometry calculations"},
		{ID: "T-006", Title: "Render labels and measurements"},
	}
	got := scopeNote([]string{"SPEC-003"}, siblings)

	for _, want := range []string{
		"part of", // not "delivers"
		"T-004 — Implement geometry calculations",
		"T-006 — Render labels and measurements",
		"Do not implement those",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the note is missing %q:\n%s", want, got)
		}
	}
	// A model that genuinely needs a piece of a sibling's work must have an
	// answer other than doing all of it, or it will do all of it.
	if !strings.Contains(got, "smallest thing") {
		t.Errorf("the note leaves a real dependency with no way out:\n%s", got)
	}
}

// A task that owns its section outright is told so plainly. Warning about
// siblings that do not exist would be noise, and noise is what gets skimmed.
func TestATaskThatOwnsItsSectionIsToldPlainly(t *testing.T) {
	got := scopeNote([]string{"SPEC-001"}, nil)
	if !strings.Contains(got, "This task delivers SPEC-001") {
		t.Errorf("got %q", got)
	}
	if strings.Contains(got, "Do not implement") {
		t.Errorf("a sole owner was warned about siblings:\n%s", got)
	}
}
