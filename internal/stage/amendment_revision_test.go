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
	firstNote := "do not touch the existing task table"
	secondNote := "add Depends on: T-060 to T-061 and T-062"
	revisions := firstNote + "\n\n" + secondNote
	var architectPrompt, reviewerPrompt string
	var deltaDigest string
	_, err = runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-revision", Mode: "solo",
		Extend: "add the small flow", Revision: revisions,
		Drafts: func() []string { return []string{fragment} },
		OnEvent: func(kind string, data map[string]interface{}) {
			if kind == "composition_review_started" {
				deltaDigest, _ = data["delta_digest"].(string)
			}
		},
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
	for _, must := range []string{firstNote, secondNote, "T-060 — Build the flow", "T-061 — Wire the flow", "T-062 — Test the flow"} {
		if !strings.Contains(architectPrompt, must) {
			t.Errorf("revision prompt lost %q:\n%s", must, architectPrompt)
		}
	}
	// B-376: the architect received the revision above, but composition review
	// used only the original Extend string. A finding against superseded scope
	// could therefore never converge no matter how accurately it was revised.
	for _, must := range []string{"add the small flow", firstNote, secondNote, "Operator revisions, in order", "authoritative where it changes or narrows"} {
		if !strings.Contains(reviewerPrompt, must) {
			t.Errorf("composition reviewer lost effective amendment %q:\n%s", must, reviewerPrompt)
		}
	}
	singleRevisionDigest := fullContentHash(effectiveExtendChange(Params{Extend: "add the small flow", Revision: secondNote}))
	if deltaDigest == "" || deltaDigest == singleRevisionDigest {
		t.Fatalf("composition delta digest did not preserve the earlier revision: got %q single-note %q", deltaDigest, singleRevisionDigest)
	}
}
