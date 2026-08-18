package stage

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/strategy"
)

// An amendment revision must be able to edit the unapproved fragment it just
// proposed. The approved plan has no T-060/T-061/T-062, so the small operator
// note is actionable only when that fragment is supplied to the architect.
func TestRevisingAnAmendmentPromptCarriesTheNoteAndPriorFragment(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan, "## M-001 — Core\n\n### T-001 — Existing task\n\nDone.\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}

	fragment := "## T-060 — Build the flow\n\nDo it.\n\n## T-061 — Wire the flow\n\nDo it.\n\n## T-062 — Test the flow\n\nDo it.\n"
	note := "add Depends on: T-060 to T-061 and T-062"
	var prompt string
	_, err = runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-revision", Mode: "solo",
		Extend: "add the small flow", Revision: note,
		Drafts: func() []string { return []string{fragment} },
		Execute: func(_ context.Context, _ *strategy.Script, got string) (string, error) {
			prompt = got
			return fragment, nil
		},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{note, "T-060 — Build the flow", "T-061 — Wire the flow", "T-062 — Test the flow"} {
		if !strings.Contains(prompt, must) {
			t.Errorf("revision prompt lost %q:\n%s", must, prompt)
		}
	}
}
