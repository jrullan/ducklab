package stage

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// The regression that motivated the fold: 15 sections drafted, 2 revised
// after critique, 13 silently gone from the proposal (B-089).
func TestFoldPassesKeepsWhatTheRevisionDidNotMention(t *testing.T) {
	round1 := "## SPEC-001 — Architecture\n\nMonolith on App Runner.\n\n" +
		"## SPEC-002 — RBAC\n\nModule-first permissions.\n\n" +
		"## SPEC-003 — Audit\n\nEvery mutation lands in the trail.\n"
	round2 := "## SPEC-002 — RBAC\n\nModule-first permissions, 192 codes.\n"

	folded, kept := FoldPasses([]string{round1, round2}, artifact.KindSpec)
	for _, want := range []string{"SPEC-001", "Monolith on App Runner", "192 codes", "SPEC-003", "Every mutation"} {
		if !strings.Contains(folded, want) {
			t.Errorf("fold lacks %q:\n%s", want, folded)
		}
	}
	if strings.Contains(folded, "Module-first permissions.\n") {
		t.Error("the revised section kept its old body")
	}
	if len(kept) != 2 || !strings.HasPrefix(kept[0], "SPEC-001") || !strings.HasPrefix(kept[1], "SPEC-003") {
		t.Errorf("kept = %v, want SPEC-001 and SPEC-003", kept)
	}
}

// A revision that returns the whole document folds to itself: nothing kept,
// nothing to announce.
func TestFoldPassesWholeDocumentIsAQuietFold(t *testing.T) {
	round1 := "## SPEC-001 — A\n\none\n\n## SPEC-002 — B\n\ntwo\n"
	round2 := "## SPEC-001 — A\n\none better\n\n## SPEC-002 — B\n\ntwo better\n\n## SPEC-003 — C\n\nthree\n"
	folded, kept := FoldPasses([]string{round1, round2}, artifact.KindSpec)
	if len(kept) != 0 {
		t.Errorf("whole-document revision reported kept sections: %v", kept)
	}
	for _, want := range []string{"one better", "two better", "SPEC-003"} {
		if !strings.Contains(folded, want) {
			t.Errorf("fold lacks %q", want)
		}
	}
}

// One usable pass — or none — degrades to the old behaviour.
func TestFoldPassesDegradesGracefully(t *testing.T) {
	solo := "## SPEC-001 — A\n\nbody\n"
	if folded, kept := FoldPasses([]string{solo}, artifact.KindSpec); folded != solo || kept != nil {
		t.Errorf("single pass changed: %q %v", folded, kept)
	}
	if folded, _ := FoldPasses([]string{"no sections here"}, artifact.KindSpec); folded != "no sections here" {
		t.Errorf("unparseable passes should return the last text, got %q", folded)
	}
}

// The B-090 regression, in the shape it actually happened: every NEW
// fragment section wears the placeholder id until AssignIDs renumbers, so
// an id-only fold saw "SPEC-900 is in the final pass" and dropped four
// verified additions. Placeholder ids fold by id+title.
func TestFoldPassesDistinguishesPlaceholderSectionsByTitle(t *testing.T) {
	round1 := "## SPEC-900 — GanttPRO sync\n\nverified as-built\n\n" +
		"## SPEC-900 — Email senders\n\ntwo-level fallback\n\n" +
		"## SPEC-900 — Status overrides\n\nnot found in tree\n"
	round2 := "## SPEC-900 — Email senders\n\nmodule then default; no deployment fallback\n"

	folded, kept := FoldPasses([]string{round1, round2}, artifact.KindSpec)
	for _, want := range []string{"GanttPRO sync", "verified as-built", "no deployment fallback", "Status overrides"} {
		if !strings.Contains(folded, want) {
			t.Errorf("fold lacks %q:\n%s", want, folded)
		}
	}
	if strings.Contains(folded, "two-level fallback") {
		t.Error("the revised placeholder section kept its old body")
	}
	if len(kept) != 2 {
		t.Errorf("kept = %v, want the two un-re-emitted placeholder sections", kept)
	}
}
