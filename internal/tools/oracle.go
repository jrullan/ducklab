package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jrullan/ducklab/internal/config"
)

// B-490. A test-first run writes the test that decides whether the task is
// done; the build is judged by it. When that oracle is wrong — TI-36X T-014's
// test expected 2^-3^2 = 0.015625, which contradicts the right associativity
// the task required — the implementer had no sanctioned move: it noticed
// ("So maybe the test is wrong?"), was told nothing about where the test came
// from, and the run spent its turns until the reviewer said so. The oracle is
// now guarded, and a contradiction goes to the person with its reason.

// The two answers an oracle dispute offers. The first unlocks the oracle for
// the rest of the build.
const (
	OracleCorrectAnswer = "The test is wrong — let the implementer correct it"
	OracleKeepAnswer    = "The test is right — implement to it"
)

// OracleQuestionPrefix marks an oracle dispute's question id. Its answer is a
// person's call, never an advisor's auto-answer.
const OracleQuestionPrefix = "oracle-"

// OracleCorrectionAllowed reports whether the person allowed correcting the
// oracle in this run.
func OracleCorrectionAllowed(ectx *ExecContext) bool {
	for id, answer := range ectx.Answers {
		if strings.HasPrefix(id, OracleQuestionPrefix) && strings.TrimSpace(answer) == OracleCorrectAnswer {
			return true
		}
	}
	return false
}

func isOracleTest(ectx *ExecContext, path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	for _, oracle := range ectx.OracleTests {
		if clean == filepath.ToSlash(filepath.Clean(oracle)) {
			return true
		}
	}
	return false
}

// OracleDispute pauses the build for the person when the implementer finds
// the test-first oracle wrong.
type OracleDispute struct{}

func (t *OracleDispute) Name() string   { return "oracle_dispute" }
func (t *OracleDispute) Mutating() bool { return false }

func (t *OracleDispute) Description() string {
	return "Report that a test written by this task's test-first run is wrong: its assertion contradicts the " +
		"task. You may not edit those tests yourself. Name the test, the assertion, and why — the acceptance " +
		"slice it contradicts and the arithmetic (expected X, but the task implies Y because …). The run " +
		"pauses for the person, who either lets you correct the test or keeps it."
}

func (t *OracleDispute) Schema() interface{} {
	return NewSchema().
		AddString("test", "The test file", true).
		AddString("assertion", "The assertion you dispute, as written", true).
		AddString("why", "The slice it contradicts and the arithmetic", true)
}

type oracleDisputeArgs struct {
	Test      string `json:"test"`
	Assertion string `json:"assertion"`
	Why       string `json:"why"`
}

func (t *OracleDispute) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	if len(ectx.OracleTests) == 0 {
		return ErrorResult("this task has no test-first oracle; if a test is wrong, fix it in your change and say so in your report"), nil
	}
	var a oracleDisputeArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	if strings.TrimSpace(a.Assertion) == "" || strings.TrimSpace(a.Why) == "" {
		return ErrorResult("name the assertion and why it is wrong: the slice it contradicts and the arithmetic"), nil
	}
	test := strings.TrimSpace(a.Test)
	if !isOracleTest(ectx, test) {
		return ErrorResult("%s is not one of this task's test-first tests (%s); a test you wrote yourself is yours to fix", test, strings.Join(ectx.OracleTests, ", ")), nil
	}
	if OracleCorrectionAllowed(ectx) {
		return SuccessResult("The person already allowed correcting the oracle: edit %s, and say in your report what you changed and why.", test), nil
	}
	id := OracleQuestionPrefix + QuestionID(test+"\n"+a.Assertion)
	if ans, ok := ectx.Answers[id]; ok {
		if strings.TrimSpace(ans) == OracleKeepAnswer {
			return SuccessResult("The person keeps the test: implement to it as written."), nil
		}
		return SuccessResult("%s", ans), nil
	}
	if ectx.NoHuman || ectx.Autonomy == config.AutonomyAuto {
		return ErrorResult("no human available to decide; keep the test as written, implement to it, and state the disagreement in your report"), nil
	}
	question := fmt.Sprintf("The implementer says a test written by this task's test-first run is wrong.\n\nTest: %s\nAssertion: %s\nWhy: %s\n\n"+
		"Correcting it lets the implementer edit the test; keeping it means the implementation must satisfy it as written.",
		test, strings.TrimSpace(a.Assertion), strings.TrimSpace(a.Why))
	ectx.Pending = &PendingQuestion{ID: id, Question: question, Options: []string{OracleCorrectAnswer, OracleKeepAnswer}}
	return nil, ErrHumanNeeded
}
