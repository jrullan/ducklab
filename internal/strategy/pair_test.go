package strategy

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// recorder captures every prompt the scheduler builds, which is how the
// anonymity and findings-injection guarantees are checked: they are properties
// of what reaches the model, not of what we intended to send.
type recorder struct {
	prompts   []string
	roles     []config.Role
	ducklings []config.DucklingID
	belts     [][]string
}

func (r *recorder) runner(outcomes ...*agent.Outcome) TurnRunner {
	i := 0
	return func(ctx context.Context, t *Turn, d config.DucklingID, prompt string, belt []string, tc TurnContext) (*agent.Outcome, error) {
		r.prompts = append(r.prompts, prompt)
		r.roles = append(r.roles, t.Role)
		r.ducklings = append(r.ducklings, d)
		r.belts = append(r.belts, belt)
		var out *agent.Outcome
		if i < len(outcomes) {
			out = outcomes[i]
		} else {
			out = &agent.Outcome{Text: "done"}
		}
		i++
		return out, nil
	}
}

func editsOutcome(text string) *agent.Outcome {
	return &agent.Outcome{Text: text}
}

func verdictOutcome(verdict string, findings ...agent.Finding) *agent.Outcome {
	return &agent.Outcome{
		Text:   `{"verdict":"` + verdict + `"}`,
		Parsed: &agent.Verdict{Verdict: verdict, Findings: findings},
	}
}

func pairParams(rec *recorder, gate string, outcomes ...*agent.Outcome) *ExecuteParams {
	return &ExecuteParams{
		Prompt: "Task T-001: make TestAdd pass.",
		Runner: rec.runner(outcomes...),
		Gate: func(ctx context.Context) (string, string, error) {
			return gate, "", nil
		},
		Diff: func() (string, error) {
			return "--- a/add.go\n+++ b/add.go\n-return a - b\n+return a + b", nil
		},
		Roster: map[config.Role]config.DucklingID{
			config.RoleImplementer: "pato-local",
			config.RoleReviewer:    "pato-nube",
		},
	}
}

// AC-17: pair uses two distinct ducklings, and round 2's implementer prompt
// carries round 1's findings.
func TestPairUsesTwoDucklingsAndFeedsBackFindings(t *testing.T) {
	rec := &recorder{}
	finding := agent.Finding{
		Severity: "major", File: "auth.go", Line: 88,
		Issue: "nil deref when the token is expired", Fix: "guard before deref",
	}
	params := pairParams(rec, "red",
		editsOutcome("changed add.go"),
		verdictOutcome("request-changes", finding),
		editsOutcome("guarded the deref"),
		verdictOutcome("approve"),
	)

	res, err := ExecutePair(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}

	if len(rec.ducklings) < 2 {
		t.Fatalf("only %d turns ran", len(rec.ducklings))
	}
	if rec.ducklings[0] == rec.ducklings[1] {
		t.Errorf("both roles resolved to %q; pair needs two decorrelated ducklings", rec.ducklings[0])
	}
	if rec.roles[0] != config.RoleImplementer || rec.roles[1] != config.RoleReviewer {
		t.Errorf("turn order = %v, want implementer then reviewer", rec.roles)
	}

	if len(rec.prompts) < 3 {
		t.Fatalf("round 2 never ran (%d turns); the loop stopped early", len(rec.prompts))
	}
	round2 := rec.prompts[2]
	if !strings.Contains(round2, "nil deref when the token is expired") {
		t.Errorf("round 2's implementer prompt lost the review:\n%s", round2)
	}
	if !strings.Contains(round2, "[major] auth.go:88") {
		t.Errorf("round 2's prompt lost the finding's anchor:\n%s", round2)
	}
	if res.Rounds < 2 {
		t.Errorf("Rounds = %d, want at least 2", res.Rounds)
	}
}

// AC-18 / I7: the reviewer sees the diff and never the author's identity or
// reasoning.
func TestReviewerPromptHasDiffButNotAuthorIdentityOrReasoning(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "green",
		editsOutcome("I changed add.go because the operator was inverted"),
		verdictOutcome("approve"),
	)
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}

	if len(rec.prompts) < 2 {
		t.Fatal("reviewer turn did not run")
	}
	reviewerPrompt := rec.prompts[1]

	if !strings.Contains(reviewerPrompt, "return a + b") {
		t.Errorf("reviewer prompt is missing the diff:\n%s", reviewerPrompt)
	}
	if strings.Contains(reviewerPrompt, "pato-local") {
		t.Errorf("reviewer prompt leaked the implementer's duckling id:\n%s", reviewerPrompt)
	}
	if strings.Contains(reviewerPrompt, "because the operator was inverted") {
		t.Errorf("reviewer prompt leaked the author's reasoning; a reviewer that reads it adopts it:\n%s", reviewerPrompt)
	}
}

// The reviewer's toolbelt must stay read-only even though the script says
// "full" — the role's ceiling applies.
func TestPairReviewerBeltIsReadOnly(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "green", editsOutcome("x"), verdictOutcome("approve"))
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	for _, name := range rec.belts[1] {
		switch name {
		case "fs_write", "fs_patch", "fs_delete", "shell":
			t.Errorf("reviewer was given %q", name)
		}
	}
}

// Until must actually stop the loop; a green gate plus approval ends it.
func TestPairStopsWhenGreenAndApproved(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "green", editsOutcome("x"), verdictOutcome("approve"))
	res, err := ExecutePair(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1 {
		t.Errorf("Rounds = %d, want 1 — Until was satisfied after round 1", res.Rounds)
	}
	if len(rec.prompts) != 2 {
		t.Errorf("%d turns ran, want 2", len(rec.prompts))
	}
}

// I3: a run that never satisfies Until stops at the round cap.
func TestPairStopsAtRoundCap(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "red")
	res, err := ExecutePair(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != PairScript().MaxRounds {
		t.Errorf("Rounds = %d, want the cap %d", res.Rounds, PairScript().MaxRounds)
	}
}

// A green gate with an unapproving reviewer must keep going: the gate is
// ground truth for correctness, but pair's Until also wants the review.
func TestPairContinuesWhenGreenButNotApproved(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "green",
		editsOutcome("x"),
		verdictOutcome("request-changes", agent.Finding{Severity: "major", File: "a.go", Issue: "y", Fix: "z"}),
		editsOutcome("x2"),
		verdictOutcome("approve"),
	)
	res, err := ExecutePair(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 2 {
		t.Errorf("Rounds = %d, want 2", res.Rounds)
	}
}

func TestPairRecordsPerRoundState(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "red",
		editsOutcome("x"),
		verdictOutcome("request-changes", agent.Finding{Severity: "minor", File: "a.go", Issue: "y", Fix: "z"}),
	)
	res, err := ExecutePair(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != res.Rounds {
		t.Fatalf("got %d records for %d rounds", len(res.Records), res.Rounds)
	}
	if res.Records[0].Gate != "red" || res.Records[0].Verdict != "request-changes" {
		t.Errorf("round 1 record = %+v", res.Records[0])
	}
}

// With no gate configured the state is honest about it rather than assuming
// green (P3).
func TestNoGateMeansNoGreen(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "", editsOutcome("x"), verdictOutcome("approve"))
	params.Gate = nil
	res, err := ExecutePair(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.State.Gate == "green" {
		t.Error("a run with no gate reported green")
	}
	if res.Rounds != PairScript().MaxRounds {
		t.Errorf("Rounds = %d; without a green gate Until can never be satisfied", res.Rounds)
	}
}

func TestPairScriptValidates(t *testing.T) {
	if err := PairScript().Validate(testRegistry(t)); err != nil {
		t.Fatalf("pair script does not validate: %v", err)
	}
}
