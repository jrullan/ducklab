package strategy

import (
	"context"
	"errors"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// A resume skips every turn of the rounds before its checkpoint — their work
// is already in the tree and the gate that judged them is already in the
// record. It used to re-run that gate anyway: on every question, escalation
// and budget lift the person clicked "continue", watched a full suite run
// (36 s measured, minutes on a slow gate), and only then saw the interrupted
// seat re-enter. Measured on r-20260828-163446-i2du: checkpoint resume →
// gate_started → 36 s → round_gate → turn_start.
func TestAResumeDoesNotReRunTheGateOfReplayedRounds(t *testing.T) {
	calls := 0
	first := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			calls++
			switch {
			case turn.Role == config.RoleReviewer:
				// Round 1 asks for changes, so the script reaches round 2.
				return verdictOutcome("request-changes"), nil
			case calls == 3:
				// Round 2's implementer dies mid-turn: the checkpoint.
				return &agent.Outcome{Text: "half done"}, agent.ErrBudgetExceeded
			}
			return &agent.Outcome{Text: "implemented"}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "impl", config.RoleReviewer: "reviewer"},
		Diff:   func() (string, error) { return "diff", nil },
		Gate:   func(context.Context) (string, string, error) { return "green", "", nil },
	}
	if _, err := ExecuteScript(context.Background(), PairScript(), first); !errors.Is(err, agent.ErrBudgetExceeded) {
		t.Fatalf("first execution error = %v", err)
	}

	gateCalls := 0
	var gateRounds []interface{}
	second := &ExecuteParams{
		ResumeFrom: &ResumeTurn{Round: 2, Index: 0, Role: config.RoleImplementer, Notes: "half done"},
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				return verdictOutcome("approve"), nil
			}
			return &agent.Outcome{Text: "finished"}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "impl", config.RoleReviewer: "reviewer"},
		Diff:   func() (string, error) { return "diff", nil },
		Gate: func(context.Context) (string, string, error) {
			gateCalls++
			return "green", "", nil
		},
		OnEvent: func(kind string, data map[string]interface{}) {
			if kind == "gate_started" {
				gateRounds = append(gateRounds, data["round"])
			}
		},
	}
	if _, err := ExecuteScript(context.Background(), PairScript(), second); err != nil {
		t.Fatal(err)
	}
	if gateCalls != 1 {
		t.Fatalf("the resume ran the gate %d times; the replayed round 1 must not re-run its gate — only round 2's runs", gateCalls)
	}
	if len(gateRounds) != 1 || gateRounds[0] != 2 {
		t.Fatalf("gate_started rounds = %v, want [2]", gateRounds)
	}
}
