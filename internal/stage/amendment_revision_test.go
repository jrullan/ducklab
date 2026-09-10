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
func TestRevisingAnAmendmentUsesOneEffectiveRequestForArchitectAndReviewer(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan, "## M-001 — Core\n\n### T-001 — Existing task\n\nDone.\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}

	fragment := "## T-060 — Build the flow\n\nDo it.\n\n## T-061 — Wire the flow\n\nDo it.\n\n## T-062 — Test the flow\n\nDo it.\n"
	note := "add Depends on: T-060 to T-061 and T-062"
	var architectPrompt, reviewerPrompt string
	_, err = runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-revision", Mode: "solo",
		Extend: "add the small flow", Revision: note,
		Drafts: func() []string { return []string{fragment} },
		Execute: func(_ context.Context, script *strategy.Script, got string) (string, error) {
			if script.Name == "composition-review" {
				reviewerPrompt = got
				return `{"verdict":"approve","findings":[]}`, nil
			}
			architectPrompt = got
			return fragment, nil
		},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{note, "T-060 — Build the flow", "T-061 — Wire the flow", "T-062 — Test the flow"} {
		if !strings.Contains(architectPrompt, must) {
			t.Errorf("revision prompt lost %q:\n%s", must, architectPrompt)
		}
	}
	// B-376: the architect received the revision above, but composition review
	// used only the original Extend string. A finding against superseded scope
	// could therefore never converge no matter how accurately it was revised.
	for _, must := range []string{"add the small flow", note, "authoritative where it changes or narrows"} {
		if !strings.Contains(reviewerPrompt, must) {
			t.Errorf("composition reviewer lost effective amendment %q:\n%s", must, reviewerPrompt)
		}
	}
}
