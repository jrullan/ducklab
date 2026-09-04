package strategy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/conv"
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
	maxTurns  []int
}

func (r *recorder) runner(outcomes ...*agent.Outcome) TurnRunner {
	i := 0
	return func(ctx context.Context, t *Turn, d config.DucklingID, prompt string, belt []string, tc TurnContext) (*agent.Outcome, error) {
		r.prompts = append(r.prompts, prompt)
		r.roles = append(r.roles, t.Role)
		r.ducklings = append(r.ducklings, d)
		r.belts = append(r.belts, belt)
		r.maxTurns = append(r.maxTurns, t.MaxTurns)
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
	if !strings.Contains(round2, "Blocking review ledger") || !strings.Contains(round2, "Merely re-reading files or re-running the gate is not a disposition") {
		t.Errorf("round 2's findings were not presented as blocking work:\n%s", round2)
	}
	if strings.Index(round2, "Blocking review ledger") < strings.Index(round2, "Deliverables — your work contract") {
		t.Errorf("blocking review ledger is not the final, salient contract:\n%s", round2)
	}
	if res.Rounds < 2 {
		t.Errorf("Rounds = %d, want at least 2", res.Rounds)
	}
}

// A process-level pause reconstructs ExecuteScript with a fresh transcript.
// The checkpoint must therefore carry the open review ledger explicitly;
// skipping round 1 cannot magically rebuild it.
func TestResumedImplementerReceivesCheckpointedFindings(t *testing.T) {
	rec := &recorder{}
	finding := conv.Finding{
		Severity: "critical", File: "worker.c", Line: 42,
		Issue: "completion is never signalled", Fix: "complete the caller-visible task",
	}
	params := pairParams(rec, "green",
		editsOutcome("completed the task"),
		verdictOutcome("approve"),
	)
	params.ResumeFrom = &ResumeTurn{
		Round: 2, Index: 0, Role: config.RoleImplementer,
		Findings: []conv.Finding{finding},
	}

	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if len(rec.prompts) == 0 {
		t.Fatal("resumed implementer did not run")
	}
	for _, want := range []string{"[critical] worker.c:42", "completion is never signalled", "complete the caller-visible task"} {
		if !strings.Contains(rec.prompts[0], want) {
			t.Errorf("resumed implementer prompt lost %q:\n%s", want, rec.prompts[0])
		}
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
// read. A tool-failure brake must summon the advisor — the rubber duck — AFTER
// the implementer's turn closes and BEFORE the reviewer speaks; the duck reads
// the implementer's story, the reviewer receives only measured facts.
func TestPairRoutesImplementerDistressToAdvisorWithoutLeakingAdmissionToReviewer(t *testing.T) {
	rec := &recorder{}
	const admission = "I am fighting fs_patch; 28 failures, brake tripped, diff partial."
	calls := make([]agent.ToolCallRecord, 28)
	for i := range calls {
		calls[i] = agent.ToolCallRecord{
			Name:   "fs_patch",
			Args:   []byte(`{"path":"widget.go"}`),
			Result: &tools.Result{IsError: true, Content: "patch did not apply"},
		}
	}
	// The final refused call is the tool's brake, rather than merely another
	// failed patch attempt.
	calls[len(calls)-1].Result.Content = "REFUSED: fs_patch has failed 28 times on this file; stop patching"

	params := pairParams(rec, "green",
		&agent.Outcome{Text: admission, Reasoning: "maybe I should try fs_write but it might truncate", ToolCalls: calls},
		// The duck answers with the advice contract: a note — which sends the
		// implementer straight back to work, note in hand, before any reviewer.
		&agent.Outcome{Parsed: map[string]interface{}{"action": "note", "note": "Use fs_write_lines on widget.go after reading the current lines."}},
		editsOutcome("Rewrote widget.go lines 12-40 with fs_write_lines."),
		verdictOutcome("approve"),
	)
	params.Roster[config.RoleAdvisor] = "pato-duck"
	var kinds []string
	params.OnEvent = func(kind string, data map[string]interface{}) {
		if kind == "turn_start" || kind == "turn_end" {
			kinds = append(kinds, kind+":"+data["role"].(string))
		}
	}
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
		t.Fatal("tool-failure distress did not summon the advisor")
	}
	if reviewerAt < 0 {
		t.Fatal("reviewer turn did not run")
	}
	if advisorAt > reviewerAt {
		t.Errorf("the duck spoke after the reviewer; it must speak before: %v", rec.roles)
	}
	// The events must close the implementer's turn BEFORE the duck's opens —
	// T-059 nested them and the desktop showed the two running in parallel.
	// And the note loops back to the implementer BEFORE the reviewer: advice
	// applied warm costs one implementer turn; wounded work sent to review
	// costs a reviewer turn and the next round.
	want := []string{"turn_start:implementer", "turn_end:implementer", "turn_start:advisor", "turn_end:advisor",
		"turn_start:implementer", "turn_end:implementer", "turn_start:reviewer", "turn_end:reviewer"}
	if strings.Join(kinds, " ") != strings.Join(want, " ") {
		t.Errorf("turn events out of order:\n got %v\nwant %v", kinds, want)
	}
	// The retried implementer turn carries the note in its prompt.
	retryAt := -1
	for i, role := range rec.roles {
		if role == config.RoleImplementer && i > advisorAt {
			retryAt = i
		}
	}
	if retryAt < 0 || !strings.Contains(rec.prompts[retryAt], "fs_write_lines on widget.go") {
		t.Errorf("the retried implementer turn did not receive the duck's note")
	}

	// The duck hears what the reviewer must not: the story, the reasoning, the trace.
	advisorPrompt := rec.prompts[advisorAt]
	for _, want := range []string{"fs_patch", "REFUSED", admission, "might truncate", `"failure_streak":28`} {
		if !strings.Contains(advisorPrompt, want) {
			t.Errorf("rubber-duck prompt lacks %q:\n%s", want, advisorPrompt)
		}
	}

	reviewerPrompt := rec.prompts[reviewerAt]
	if strings.Contains(reviewerPrompt, admission) || strings.Contains(reviewerPrompt, "fighting fs_patch") || strings.Contains(reviewerPrompt, "might truncate") {
		t.Errorf("reviewer received the implementer's rationalization instead of blind operational data:\n%s", reviewerPrompt)
	}
	// Machine-readable facts only: what the harness counted.
	for _, want := range []string{`"failure_streak_tool":"fs_patch"`, `"failure_streak":28`, `"refusals":1`} {
		if !strings.Contains(reviewerPrompt, want) {
			t.Errorf("reviewer operational summary lacks %s:\n%s", want, reviewerPrompt)
		}
	}
}

// A working turn costs no duck: four fs_read misses are not distress, and no
// advisor seat is consulted for them (T-119 burned k3's turn on exactly this).
func TestPairDoesNotSummonTheDuckForAMerelyRoughTurn(t *testing.T) {
	rec := &recorder{}
	calls := []agent.ToolCallRecord{
		{Name: "fs_read", Args: []byte(`{"path":"a.go"}`), Result: &tools.Result{IsError: true, Content: "no such file"}},
		{Name: "fs_read", Args: []byte(`{"path":"b.go"}`), Result: &tools.Result{IsError: true, Content: "no such file"}},
		{Name: "fs_read", Args: []byte(`{"path":"c.go"}`), Result: &tools.Result{IsError: true, Content: "no such file"}},
		{Name: "fs_read", Args: []byte(`{"path":"d.go"}`), Result: &tools.Result{IsError: true, Content: "no such file"}},
		{Name: "fs_write", Args: []byte(`{"path":"d.go"}`), Result: &tools.Result{Content: "wrote"}},
	}
	params := pairParams(rec, "green",
		&agent.Outcome{Text: "done", ToolCalls: calls},
		verdictOutcome("approve"),
	)
	params.Roster[config.RoleAdvisor] = "pato-duck"
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	for _, role := range rec.roles {
		if role == config.RoleAdvisor {
			t.Fatalf("the duck was summoned for a turn that was merely rough: %v", rec.roles)
		}
	}
}

// The duck's third answer: stop. The run ends with the reason and the
// reshuffle suggestion on the record, before the reviewer spends a turn.
// The advisor turn is fabricated after the script has been walked, so it must
// resolve its cap directly from TurnCaps. The service fills these from
// defaults.role_turns and from the per-run AgentTurns override.
func TestPairAdvisorConsultHonorsTurnCaps(t *testing.T) {
	for _, tc := range []struct {
		name string
		caps map[config.Role]int
		want int
	}{
		{name: "configured advisor cap", caps: map[config.Role]int{config.RoleAdvisor: 20}, want: 20},
		{name: "consult default", want: consultAdvisorDefaultTurns},
		{name: "per-run no-cap lift", caps: map[config.Role]int{config.RoleAdvisor: agent.UncappedTurns}, want: agent.UncappedTurns},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			params := pairParams(rec, "green",
				&agent.Outcome{Text: "stuck", ToolCalls: []agent.ToolCallRecord{{Name: "fs_patch", Result: &tools.Result{IsError: true, Content: "REFUSED: brake"}}}},
				&agent.Outcome{Parsed: map[string]interface{}{"action": "none"}},
				verdictOutcome("approve"),
			)
			params.Roster[config.RoleAdvisor] = "pato-duck"
			params.TurnCaps = tc.caps
			if _, err := ExecutePair(context.Background(), params); err != nil {
				t.Fatal(err)
			}
			for i, role := range rec.roles {
				if role == config.RoleAdvisor {
					if rec.maxTurns[i] != tc.want {
						t.Errorf("advisor MaxTurns = %d, want %d", rec.maxTurns[i], tc.want)
					}
					return
				}
			}
			t.Errorf("advisor consult did not run; roles = %v", rec.roles)
		})
	}
}

func TestPairAdvisorStopEndsTheRunBeforeTheReviewer(t *testing.T) {
	rec := &recorder{}
	calls := make([]agent.ToolCallRecord, 6)
	for i := range calls {
		calls[i] = agent.ToolCallRecord{Name: "verify_run", Result: &tools.Result{IsError: true, Content: "FAIL"}}
	}
	params := pairParams(rec, "red",
		&agent.Outcome{Text: "still red", ToolCalls: calls},
		&agent.Outcome{Parsed: map[string]interface{}{"action": "stop", "reason": "six red gates, no new approach", "reshuffle": "reseat the implementer to a stronger duckling"}},
		verdictOutcome("approve"),
	)
	params.Roster[config.RoleAdvisor] = "pato-duck"
	var consult map[string]interface{}
	params.OnEvent = func(kind string, data map[string]interface{}) {
		if kind == "advisor_consult" {
			consult = data
		}
	}
	_, err := ExecutePair(context.Background(), params)
	stop, ok := StoppedByAdvisor(err)
	if !ok {
		t.Fatalf("expected an advisor stop, got %v", err)
	}
	if stop.Advisor != "pato-duck" || !strings.Contains(stop.Reason, "six red gates") || !strings.Contains(stop.Reshuffle, "stronger") {
		t.Errorf("stop lost its content: %+v", stop)
	}
	for _, role := range rec.roles {
		if role == config.RoleReviewer {
			t.Error("the reviewer ran after the duck said stop")
		}
	}
	if consult == nil || consult["outcome"] != "stop" {
		t.Errorf("advisor_consult event does not record the stop: %v", consult)
	}
}

// The inner loop is bounded: after maxConsultRetries the round proceeds to
// the reviewer even if the duck keeps handing out notes — the reviewer and
// the gate are the independent check, the duck is not.
func TestPairInnerLoopIsBounded(t *testing.T) {
	rec := &recorder{}
	distressed := func() *agent.Outcome {
		return &agent.Outcome{Text: "still stuck", ToolCalls: []agent.ToolCallRecord{
			{Name: "fs_patch", Result: &tools.Result{IsError: true, Content: "REFUSED: brake"}},
		}}
	}
	note := func() *agent.Outcome {
		return &agent.Outcome{Parsed: map[string]interface{}{"action": "note", "note": "try again differently"}}
	}
	params := pairParams(rec, "green",
		distressed(), note(), // consult 1 → retry 1
		distressed(), note(), // consult 2 → retry 2
		distressed(), note(), // consult 3 → cap reached, on to the reviewer
		verdictOutcome("approve"),
	)
	params.Roster[config.RoleAdvisor] = "pato-duck"
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	impl, adv, rev := 0, 0, 0
	for _, role := range rec.roles {
		switch role {
		case config.RoleImplementer:
			impl++
		case config.RoleAdvisor:
			adv++
		case config.RoleReviewer:
			rev++
		}
	}
	if impl != 1+maxConsultRetries || adv != 1+maxConsultRetries || rev != 1 {
		t.Errorf("got implementer=%d advisor=%d reviewer=%d, want %d/%d/1: %v", impl, adv, rev, 1+maxConsultRetries, 1+maxConsultRetries, rec.roles)
	}
}

func TestSmallSeatGetsOneAdvisorRetryBeforeIndependentReview(t *testing.T) {
	rec := &recorder{}
	distressed := func() *agent.Outcome {
		return &agent.Outcome{Text: "still stuck", ToolCalls: []agent.ToolCallRecord{
			{Name: "fs_patch", Result: &tools.Result{IsError: true, Content: "REFUSED: brake"}},
		}}
	}
	note := func() *agent.Outcome {
		return &agent.Outcome{Parsed: map[string]interface{}{"action": "note", "note": "try one targeted repair"}}
	}
	params := pairParams(rec, "green",
		distressed(), note(), // consult 1 → one retry
		distressed(), note(), // consult 2 → reviewer, not another retry
		verdictOutcome("approve"),
	)
	params.SmallSeat = true
	params.Roster[config.RoleAdvisor] = "pato-duck"
	originalRunner := params.Runner
	var implementerCaps []int
	params.Runner = func(ctx context.Context, turn *Turn, duckling config.DucklingID, prompt string, toolbelt []string, tc TurnContext) (*agent.Outcome, error) {
		if turn.Role == config.RoleImplementer {
			implementerCaps = append(implementerCaps, turn.MaxTurns)
		}
		return originalRunner(ctx, turn, duckling, prompt, toolbelt, tc)
	}
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	impl, adv, rev := 0, 0, 0
	for _, role := range rec.roles {
		switch role {
		case config.RoleImplementer:
			impl++
		case config.RoleAdvisor:
			adv++
		case config.RoleReviewer:
			rev++
		}
	}
	if impl != 2 || adv != 2 || rev != 1 {
		t.Errorf("small-seat routing = implementer:%d advisor:%d reviewer:%d, want 2/2/1: %v", impl, adv, rev, rec.roles)
	}
	if len(implementerCaps) != 2 || implementerCaps[0] != 24 || implementerCaps[1] != 24 {
		t.Errorf("advisor repair caps = %v, want [24 24]: work without a current green verify is not a narrow continuation", implementerCaps)
	}
}

func TestPairSendsCompletedGreenWorkStraightToReviewer(t *testing.T) {
	rec := &recorder{}
	implemented := &agent.Outcome{
		Text: `Finished despite an earlier tool brake. {"deliverables":[{"id":1,"status":"done"}]}`,
		ToolCalls: []agent.ToolCallRecord{
			{Name: "fs_patch", Result: &tools.Result{IsError: true, Content: "REFUSED: patch brake"}},
			{Name: "fs_write", Result: &tools.Result{Content: "wrote file"}},
			{Name: "verify_run", Result: &tools.Result{Content: "gate: green"}},
		},
	}
	params := pairParams(rec, "green", implemented, verdictOutcome("approve"))
	params.Deliverables = []string{"A"}
	params.Roster[config.RoleAdvisor] = "pato-duck"
	var skipped bool
	params.OnEvent = func(kind string, _ map[string]interface{}) { skipped = skipped || kind == "advisor_skipped" }
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	for _, role := range rec.roles {
		if role == config.RoleAdvisor {
			t.Fatalf("advisor ran before review of completed green work: %v", rec.roles)
		}
	}
	if !skipped {
		t.Fatal("completed green distress did not record why advisor was skipped")
	}
}

// No advisor seated: the consult is skipped on the record and the run goes on
// to the reviewer — a missing seat must never fail a run.
func TestPairWithoutAnAdvisorSeatSkipsTheConsult(t *testing.T) {
	rec := &recorder{}
	calls := []agent.ToolCallRecord{{Name: "fs_patch", Result: &tools.Result{IsError: true, Content: "REFUSED: brake"}}}
	params := pairParams(rec, "green",
		&agent.Outcome{Text: "hmm", ToolCalls: calls},
		verdictOutcome("approve"),
	)
	var consult map[string]interface{}
	params.OnEvent = func(kind string, data map[string]interface{}) {
		if kind == "advisor_consult" {
			consult = data
		}
	}
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if consult == nil || consult["outcome"] != "skipped" {
		t.Errorf("skipped consult not recorded: %v", consult)
	}
	if len(rec.roles) != 2 {
		t.Errorf("expected implementer+reviewer only, got %v", rec.roles)
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
func TestResumeRepeatsInterruptedRoleWithPartialNotes(t *testing.T) {
	var events []map[string]interface{}
	var firstRoles []config.Role
	calls := 0
	first := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			firstRoles = append(firstRoles, turn.Role)
			calls++
			if calls == 2 {
				return &agent.Outcome{Text: "partial review notes"}, agent.ErrBudgetExceeded
			}
			return &agent.Outcome{Text: "implemented"}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "impl", config.RoleReviewer: "reviewer"},
		Diff:   func() (string, error) { return "diff", nil },
		OnEvent: func(kind string, data map[string]interface{}) {
			if kind == "turn_interrupted" {
				events = append(events, data)
			}
		},
	}
	if _, err := ExecuteScript(context.Background(), PairScript(), first); !errors.Is(err, agent.ErrBudgetExceeded) {
		t.Fatalf("first execution error = %v", err)
	}
	if len(events) != 1 || events[0]["role"] != string(config.RoleReviewer) {
		t.Fatalf("checkpoint = %+v, want reviewer interruption", events)
	}

	var prompt string
	var resumedRoles []config.Role
	second := &ExecuteParams{
		ResumeFrom: &ResumeTurn{Round: 1, Index: 1, Role: config.RoleReviewer, Notes: "partial review notes"},
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, p string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			resumedRoles = append(resumedRoles, turn.Role)
			prompt = p
			return verdictOutcome("approve"), nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "impl", config.RoleReviewer: "reviewer"},
		Diff:   func() (string, error) { return "diff", nil },
		Gate:   func(context.Context) (string, string, error) { return "green", "", nil },
	}
	if _, err := ExecuteScript(context.Background(), PairScript(), second); err != nil {
		t.Fatal(err)
	}
	if len(firstRoles) != 2 || firstRoles[0] != config.RoleImplementer || firstRoles[1] != config.RoleReviewer {
		t.Fatalf("first roles = %v", firstRoles)
	}
	if len(resumedRoles) != 1 || resumedRoles[0] != config.RoleReviewer {
		t.Fatalf("resumed roles = %v, want reviewer only", resumedRoles)
	}
	if !strings.Contains(prompt, "Resume checkpoint") || !strings.Contains(prompt, "continue, do not restart") || !strings.Contains(prompt, "partial review notes") {
		t.Fatalf("resume prompt lost partial notes: %q", prompt)
	}
}

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
