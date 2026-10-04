package strategy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

// The shape of TI-36X T-014's first attempt: the right patch, a red gate on
// a wrong test-first assertion, and the right diagnosis in the final message,
// but no deliverables report.
func t014FirstAttempt() *agent.Outcome {
	return &agent.Outcome{
		Text: "Patched parsePow. Only `2^-3^2` still fails: the test expects 0.015625, but 2^-9 is 0.001953125. So maybe the test is wrong?",
		ToolCalls: []agent.ToolCallRecord{
			{Name: "fs_read", Args: json.RawMessage(`{"path":"logic.mjs","start":660,"end":700}`), Result: &tools.Result{Content: "…"}},
			{Name: "fs_read", Args: json.RawMessage(`{"path":"tests/parser.test.mjs"}`), Result: &tools.Result{Content: "…"}},
			{Name: "fs_patch", Args: json.RawMessage(`{"path":"logic.mjs","edits":[]}`), Result: &tools.Result{Content: "patched"}},
			{Name: "fs_write", Args: json.RawMessage(`{"path":"trace.mjs"}`), Result: &tools.Result{IsError: true, Content: "lane: outside"}},
			{Name: "verify_run", Result: &tools.Result{IsError: true, Content: "gate: tests\ncmd: npm test --silent\nexit: 1\nok 1 - package.json test script\nok 2 - delivery\nnot ok 12 - power: negated exponent right-hand side\n  expected: 0.015625\n  actual: 0.001953125\n# pass 84\n# fail 1"}},
		},
	}
}

// B-496: a report retry is a new conversation. It must start from what the
// attempt did and concluded: its message verbatim, the file it changed, the
// failing assertion, what it read. A failed write is not a change, and the
// passing log is not evidence worth the prompt.
func TestAReportRetryCarriesThePreviousAttempt(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "green",
		t014FirstAttempt(),
		&agent.Outcome{Text: `Fixed the test. {"deliverables":[{"id":1,"status":"done"}]}`},
		verdictOutcome("approve"),
	)
	params.Deliverables = []string{"A"}
	params.SmallSeat = true
	params.ExecContext = &tools.ExecContext{}
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if len(rec.prompts) < 3 || rec.roles[1] != config.RoleImplementer {
		t.Fatalf("expected implementer, implementer retry, reviewer: %v", rec.roles)
	}
	retry := rec.prompts[1]
	for _, want := range []string{
		"## Your previous attempt at this turn",
		"So maybe the test is wrong?",
		"- logic.mjs (fs_patch)",
		"not ok 12 - power: negated exponent right-hand side",
		"expected: 0.015625",
		"fs_read logic.mjs:660-700",
		"fs_read tests/parser.test.mjs",
	} {
		if !strings.Contains(retry, want) {
			t.Errorf("retry prompt lacks %q:\n%s", want, retry)
		}
	}
	for _, unwanted := range []string{"trace.mjs (fs_write)", "ok 2 - delivery"} {
		if strings.Contains(retry, unwanted) {
			t.Errorf("retry prompt carries %q, which is not evidence:\n%s", unwanted, retry)
		}
	}
	if strings.Contains(rec.prompts[2], "Your previous attempt") {
		t.Errorf("the reviewer received the implementer's retry context:\n%s", rec.prompts[2])
	}
}

// The advisor-retry path carries it too, and only into the retry it caused.
func TestAnAdvisorRetryCarriesThePreviousAttempt(t *testing.T) {
	rec := &recorder{}
	distressed := &agent.Outcome{Text: "Still stuck: the guard at logic.mjs:672 looks right to me.", ToolCalls: []agent.ToolCallRecord{
		{Name: "fs_patch", Args: json.RawMessage(`{"path":"logic.mjs"}`), Result: &tools.Result{IsError: true, Content: "REFUSED: brake"}},
	}}
	note := &agent.Outcome{Parsed: map[string]interface{}{"action": "note", "note": "try one targeted repair"}}
	params := pairParams(rec, "green",
		distressed, note,
		&agent.Outcome{Text: "done"},
		verdictOutcome("approve"),
	)
	params.SmallSeat = true
	params.Roster[config.RoleAdvisor] = "pato-duck"
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	retryAt := -1
	for i, role := range rec.roles {
		if role == config.RoleImplementer && i > 0 {
			retryAt = i
			break
		}
	}
	if retryAt < 0 {
		t.Fatalf("no implementer retry: %v", rec.roles)
	}
	retry := rec.prompts[retryAt]
	if !strings.Contains(retry, "## Your previous attempt at this turn") || !strings.Contains(retry, "the guard at logic.mjs:672 looks right to me") {
		t.Errorf("advisor retry lacks the previous attempt:\n%s", retry)
	}
	if !strings.Contains(retry, "- none") {
		t.Errorf("a refused patch was reported as a change:\n%s", retry)
	}
	if strings.Index(retry, "## Your previous attempt") > strings.Index(retry, "## Advisor corrective note") {
		t.Errorf("the attempt should come before the advisor's note that responds to it:\n%s", retry)
	}
}

func TestPreviousAttemptIsEmptyWhenTheTurnDidNothing(t *testing.T) {
	if got := previousAttempt(nil); got != "" {
		t.Errorf("nil outcome rendered %q", got)
	}
	if got := previousAttempt(&agent.Outcome{Text: "  "}); got != "" {
		t.Errorf("empty outcome rendered %q", got)
	}
	long := strings.Repeat("x", maxPriorMessageRunes+50) + " the conclusion"
	if got := previousAttempt(&agent.Outcome{Text: long}); !strings.Contains(got, "the conclusion") || strings.Count(got, "x") > maxPriorMessageRunes {
		t.Errorf("a long message should keep its end, bounded")
	}
}

// TI-36X T-005: a test-first writer read until the boundary, wrote its plan as
// text and changed nothing; the reviewer asked for the test; the gate stayed
// green. The run stopped as "settled" — but in test-first green means the
// failing test is still missing. Round 2 must run, and its writer starts from
// round 1's plan instead of from zero.
func TestATestFirstRoundThatWroteNothingIsNotSettled(t *testing.T) {
	rec := &recorder{}
	var settled bool
	params := &ExecuteParams{
		Prompt: "Task T-005: specify the LCD workflow.",
		Runner: rec.runner(
			&agent.Outcome{Text: "I have enough to write the test: tests/lcd-flow.test.mjs will assert backspace, delete and the clear hierarchy."},
			verdictOutcome("request-changes", agent.Finding{Severity: "critical", File: "*", Issue: "The diff is empty: no test was written", Fix: "Write tests/lcd-flow.test.mjs"}),
			&agent.Outcome{Text: "Wrote the test.", ToolCalls: []agent.ToolCallRecord{
				{Name: "fs_write", Args: json.RawMessage(`{"path":"tests/lcd-flow.test.mjs"}`), Result: &tools.Result{Content: "wrote"}},
			}},
			verdictOutcome("approve"),
		),
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "pato-atom", config.RoleReviewer: "pato-sonnet"},
		Gate:   func(context.Context) (string, string, error) { return "green", "", nil },
		Diff:   func() (string, error) { return "", nil },
		OnEvent: func(kind string, _ map[string]interface{}) {
			settled = settled || kind == "settled"
		},
	}
	res, err := ExecuteTestFirstMode(context.Background(), "pair", params)
	if err != nil {
		t.Fatal(err)
	}
	if settled || res.Rounds != 2 {
		t.Fatalf("test-first stopped as settled after %d round(s); green means the test is missing", res.Rounds)
	}
	round2 := rec.prompts[2]
	if rec.roles[2] != config.RoleImplementer || !strings.Contains(round2, "## Your previous attempt at this turn") ||
		!strings.Contains(round2, "tests/lcd-flow.test.mjs will assert backspace") {
		t.Errorf("round 2's writer did not start from round 1's plan:\n%s", round2)
	}
	if strings.Contains(rec.prompts[0], "Your previous attempt") {
		t.Error("round 1 carried an attempt that did not exist")
	}
}

// A round whose implementer did edit carries nothing forward: the reviewer's
// findings and the tree are its continuation.
func TestARoundThatEditedCarriesNoAttemptForward(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "red",
		&agent.Outcome{Text: "Patched.", ToolCalls: []agent.ToolCallRecord{
			{Name: "fs_patch", Args: json.RawMessage(`{"path":"add.go"}`), Result: &tools.Result{Content: "patched"}},
		}},
		verdictOutcome("request-changes", agent.Finding{Severity: "major", File: "add.go", Issue: "wrong sign", Fix: "use +"}),
		&agent.Outcome{Text: "Fixed."},
		verdictOutcome("approve"),
	)
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if rec.roles[2] != config.RoleImplementer || strings.Contains(rec.prompts[2], "Your previous attempt") {
		t.Errorf("round 2's implementer carried an attempt although round 1 edited:\n%s", rec.prompts[2])
	}
}
