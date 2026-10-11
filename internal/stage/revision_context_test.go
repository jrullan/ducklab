package stage

import (
	"strings"
	"testing"
)

// B-521: a revision's prompt carries the request it continues — brief and
// references — as context under the assignment; a fresh brief's prompt is
// untouched (its ask already is the seed).
func TestRevisionPromptCarriesTheRequestItContinues(t *testing.T) {
	seed := "Fill the gap.\n\n## Reference: guidebook.md — role: SCOPE (describes the intended product)\n\nPolar form."
	got := withRevisionContext("## The request\n\nKeep the wording.\n", Params{Revision: "Keep the wording.", Seed: seed})
	at := strings.Index(got, revisionContextHeading)
	if at < 0 || !strings.Contains(got[at:], "role: SCOPE") || strings.Index(got, "Keep the wording.") > at {
		t.Fatalf("revision prompt lacks the continued request after its assignment:\n%s", got)
	}
	if fresh := withRevisionContext("prompt", Params{Seed: seed}); fresh != "prompt" {
		t.Fatalf("a fresh brief's prompt changed: %q", fresh)
	}
	if bare := withRevisionContext("prompt", Params{Revision: "x"}); bare != "prompt" {
		t.Fatalf("a revision with no seed changed: %q", bare)
	}
}
