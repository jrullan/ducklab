package strategy

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
)

// A fragment prompt deliberately repeats SPEC-900 for every new section.
// Canonicalize that wire format before the reviewer sees it so the critic
// cannot mistake a placeholder convention for a document defect.
func TestFragmentMaterializationCanonicalizesRepeatedPlaceholders(t *testing.T) {
	first := &agent.Outcome{Text: "## SPEC-900 — Capture\n\none\n\n" +
		"## SPEC-900 — Save\n\ntwo\n"}
	got := materializeFragment(nil, first, "SPEC")
	for _, want := range []string{"## SPEC-900 — Capture", "## SPEC-901 — Save"} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("materialized fragment lacks %q:\n%s", want, got.Text)
		}
	}
	if strings.Count(got.Text, "## SPEC-") != 2 {
		t.Fatalf("sections = %d, want 2:\n%s", strings.Count(got.Text, "## SPEC-"), got.Text)
	}
}

// Corrida 13's critic contradicted the placeholder protocol and asked for
// SPEC-900..908. A later repair then repeated SPEC-900. Both spellings must
// address the same accumulated sections rather than append another copy.
func TestFragmentMaterializationKeepsStableIdentityAcrossPlaceholderSpellings(t *testing.T) {
	base := materializeFragment(nil, &agent.Outcome{Text: "## SPEC-900 — Capture\n\nold capture\n\n" +
		"## SPEC-900 — Save\n\nold save\n"}, "SPEC")
	sequential := materializeFragment(base, &agent.Outcome{Text: "## SPEC-900 — Capture\n\nnew capture\n\n" +
		"## SPEC-901 — Save\n\nnew save\n"}, "SPEC")
	repeated := materializeFragment(sequential, &agent.Outcome{Text: "## SPEC-900 — Capture\n\nfinal capture\n\n" +
		"## SPEC-900 — Save\n\nfinal save\n"}, "SPEC")

	if strings.Count(repeated.Text, "## SPEC-") != 2 {
		t.Fatalf("repair appended duplicates:\n%s", repeated.Text)
	}
	for _, want := range []string{"## SPEC-900 — Capture", "final capture", "## SPEC-901 — Save", "final save"} {
		if !strings.Contains(repeated.Text, want) {
			t.Errorf("stable candidate lacks %q:\n%s", want, repeated.Text)
		}
	}
}
