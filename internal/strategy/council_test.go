package strategy

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

func councilParams(rec *recorder, outcomes ...*agent.Outcome) *ExecuteParams {
	return &ExecuteParams{
		Prompt: "Write the requirements for a timesheet app.",
		Runner: rec.runner(outcomes...),
		Roster: map[config.Role]config.DucklingID{
			config.RoleArchitect: "pato-atom",
			config.RoleReviewer:  "pato-local",
		},
	}
}

func TestCouncilScriptValidates(t *testing.T) {
	if err := CouncilScript("REQ").Validate(testRegistry(t)); err != nil {
		t.Fatalf("council does not validate: %v", err)
	}
}

// The reviewer must see the draft. Anonymize controls WHO is shown, not
// whether the transcript appears — conflating them left council's reviewer
// reviewing nothing at all.
func TestCouncilReviewerSeesTheDraft(t *testing.T) {
	rec := &recorder{}
	draft := "## REQ-001 — Users can log time\n\nBody of the draft."
	params := councilParams(rec,
		&agent.Outcome{Text: draft},
		verdictOutcome("approve"),
		&agent.Outcome{Text: draft},
	)
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ"), params); err != nil {
		t.Fatal(err)
	}
	if len(rec.prompts) < 2 {
		t.Fatalf("only %d turns ran", len(rec.prompts))
	}
	reviewerPrompt := rec.prompts[1]
	if !strings.Contains(reviewerPrompt, "Body of the draft.") {
		t.Errorf("the reviewer was not shown the draft:\n%s", reviewerPrompt)
	}
}

// A revision that cannot see the critique is just a second draft.
func TestCouncilArchitectSeesTheCritique(t *testing.T) {
	rec := &recorder{}
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("request-changes", agent.Finding{
			Severity: "major", File: "requirements.md",
			Issue: "REQ-001 does not say what is out of scope", Fix: "add a scope line",
		}),
		&agent.Outcome{Text: "## REQ-001 — Revised\n"},
	)
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ"), params); err != nil {
		t.Fatal(err)
	}
	if len(rec.prompts) < 3 {
		t.Fatalf("the revision turn never ran (%d turns)", len(rec.prompts))
	}
	revision := rec.prompts[2]
	if !strings.Contains(revision, "out of scope") {
		t.Errorf("the architect's revision could not see the critique:\n%s", revision)
	}
}

// pair keeps the opposite rule: its reviewer must NOT read the author's
// reasoning, or the second model stops being decorrelated.
func TestPairReviewerStillCannotSeeTheAuthorsReasoning(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "green",
		editsOutcome("I changed it because the operator was inverted"),
		verdictOutcome("approve"),
	)
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.prompts[1], "because the operator was inverted") {
		t.Error("pair's reviewer was shown the author's reasoning")
	}
}

func TestCouncilSkipsTheHumanTurnWhenUnattended(t *testing.T) {
	rec := &recorder{}
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("approve"),
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
	)
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ"), params); err != nil {
		t.Fatal(err)
	}
	for _, role := range rec.roles {
		if role == config.RoleHuman {
			t.Error("a human turn ran with no human present")
		}
	}
}

// Two rounds at most: an artifact that has not converged after a draft, a
// critique and a revision needs a person, not another lap.
func TestCouncilStopsAtTwoRounds(t *testing.T) {
	rec := &recorder{}
	params := councilParams(rec)
	res, err := ExecuteScript(context.Background(), CouncilScript("REQ"), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds > 2 {
		t.Errorf("ran %d rounds", res.Rounds)
	}
}

func TestCouncilContractFollowsThePrefix(t *testing.T) {
	for prefix, want := range map[string]string{"REQ": "markdown_sections:REQ", "SPEC": "markdown_sections:SPEC", "M": "markdown_sections:M"} {
		if got := CouncilScript(prefix).Turns[0].Contract; got != want {
			t.Errorf("prefix %q: contract = %q, want %q", prefix, got, want)
		}
	}
}
