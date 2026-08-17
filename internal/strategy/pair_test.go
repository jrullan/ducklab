package strategy

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
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
// Distress is a request for operational help, not an excuse the reviewer may
// read. A tool-failure brake and an implementer's final admission must summon
// the advisor to draft a corrective note, while the reviewer receives only the
// measured facts needed to judge a partial diff.
func TestPairRoutesImplementerDistressToAdvisorWithoutLeakingAdmissionToReviewer(t *testing.T) {
	rec := &recorder{}
	const admission = "I am fighting fs_patch; 28 failures, brake tripped, diff partial."
	calls := make([]agent.ToolCallRecord, 28)
	for i := range calls {
		calls[i] = agent.ToolCallRecord{
			Name: "fs_patch",
			Args: []byte(`{"path":"widget.go"}`),
			Result: &tools.Result{IsError: true, Content: "patch did not apply"},
		}
	}
	// The final refused call is the tool's brake, rather than merely another
	// failed patch attempt.
	calls[len(calls)-1].Result.Content = "REFUSED: fs_patch has failed 28 times on this file; stop patching"

	params := pairParams(rec, "green",
		&agent.Outcome{Text: admission, ToolCalls: calls},
		// The advisor's response is deliberately unstructured prose: it belongs
		// to the next implementer attempt, never to the reviewer.
		editsOutcome("Use fs_write for widget.go after reading the current file."),
		verdictOutcome("approve"),
	)
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}

	advisorAt, reviewerAt := -1, -1
	for i, role := range rec.roles {
		switch role {
		case config.RoleAdvisor:
			advisorAt = i
		case config.RoleReviewer:
			if reviewerAt < 0 {
				reviewerAt = i
			}
		}
	}
	if advisorAt < 0 {
		t.Fatal("tool-failure distress did not request a corrective note from the advisor")
	}
	if reviewerAt < 0 {
		t.Fatal("reviewer turn did not run")
	}
	advisorPrompt := rec.prompts[advisorAt]
	for _, want := range []string{"fs_patch", "28", "brake"} {
		if !strings.Contains(advisorPrompt, want) {
			t.Errorf("advisor corrective-note request lacks operational fact %q:\n%s", want, advisorPrompt)
		}
	}

	reviewerPrompt := rec.prompts[reviewerAt]
	if strings.Contains(reviewerPrompt, admission) || strings.Contains(reviewerPrompt, "fighting fs_patch") {
		t.Errorf("reviewer received the implementer's rationalization instead of blind operational data:\n%s", reviewerPrompt)
	}
	// The exact wire representation is intentionally not prescribed, but the
	// reviewer must receive machine-readable facts, including the brake, count,
	// and partial-diff state — not a narrative summary.
	for _, want := range []string{`"tool":"fs_patch"`, `"failures":28`, `"brake_tripped":true`, `"diff_partial":true`} {
		if !strings.Contains(reviewerPrompt, want) {
			t.Errorf("reviewer operational summary lacks %s:\n%s", want, reviewerPrompt)
		}
	}
}

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

// T-007 burned three rounds of two models for nothing, and it cost real money.
//
// The task's work was already in the tree, so every implementer turn wrote
// nothing and every reviewer turn read the same empty diff. The reviewer said
// in prose that the implementation was complete and correct, then returned
// "request-changes" anyway — its verdict contract offers no way to say "the
// code is right, the plan is wrong", so its planning observation came back
// dressed as a critical code finding with a file and a line number.
//
// Pair stops on `gate == "green" and verdict == "approve"`. A reviewer that
// cannot approve makes that unreachable, and the loop runs to MaxRounds every
// time. The implementer cannot act on the objection: there is no code to write.
//
// So the loop is cut where nothing can move it — an untouched tree behind a
// green gate.
func TestRoundsStopWhenNothingCanChange(t *testing.T) {
	rec := &recorder{}
	var settled map[string]interface{}
	params := &ExecuteParams{
		Prompt: "Add lock indicators.",
		Runner: rec.runner(
			&agent.Outcome{Text: "The lock indicators are already implemented. No changes needed."},
			verdictOutcome("request-changes", agent.Finding{
				Severity: "critical", File: "index.html", Line: 147,
				Issue: "already fully implemented before this task attempt",
				Fix:   "Verify task assignment",
			}),
			&agent.Outcome{Text: "Still nothing to do."},
			verdictOutcome("request-changes"),
			&agent.Outcome{Text: "Still nothing to do."},
			verdictOutcome("request-changes"),
		),
		Roster: map[config.Role]config.DucklingID{
			config.RoleImplementer: "pato-atom",
			config.RoleReviewer:    "pato-sonnet",
		},
		Gate: func(context.Context) (string, string, error) { return "green", "", nil },
		Diff: func() (string, error) { return "", nil },
		OnEvent: func(kind string, data map[string]interface{}) {
			if kind == "settled" {
				settled = data
			}
		},
	}
	res, err := ExecuteScript(context.Background(), PairScript(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1 {
		t.Errorf("ran %d rounds against an empty diff; one was all that could be learned", res.Rounds)
	}
	if settled == nil {
		t.Error("the run stopped without saying why — a human reading this sees a truncated loop")
	}
}

// The cut must not fire while work is still landing: a reviewer asking for
// changes on a real diff is the loop doing its job.
func TestRoundsContinueWhileTheTreeIsMoving(t *testing.T) {
	rec := &recorder{}
	params := &ExecuteParams{
		Prompt: "Add lock indicators.",
		Runner: rec.runner(
			&agent.Outcome{Text: "Wrote it."},
			verdictOutcome("request-changes", agent.Finding{Severity: "major", File: "a.go", Issue: "off by one", Fix: "n-1"}),
			&agent.Outcome{Text: "Fixed."},
			verdictOutcome("approve"),
		),
		Roster: map[config.Role]config.DucklingID{
			config.RoleImplementer: "pato-atom",
			config.RoleReviewer:    "pato-sonnet",
		},
		Gate: func(context.Context) (string, string, error) { return "green", "", nil },
		Diff: func() (string, error) { return "--- a/a.go\n+++ b/a.go\n+x\n", nil },
	}
	res, err := ExecuteScript(context.Background(), PairScript(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 2 {
		t.Errorf("ran %d rounds; the second round's fix and approval were cut off", res.Rounds)
	}
}
