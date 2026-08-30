package strategy

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// With no gate configured, "green" cannot come. An approved change ends the
// run UNVERIFIED after one round instead of buying two more (T-001,
// benchmark run 5: 3 rounds, 2.15M tokens).
func TestAnApprovedChangeWithNoGateEndsAfterOneRound(t *testing.T) {
	var events []string
	params := &ExecuteParams{
		OnEvent: func(kind string, data map[string]interface{}) { events = append(events, kind) },
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				return verdictOutcome("approve"), nil
			}
			return &agent.Outcome{Text: "implemented"}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "impl", config.RoleReviewer: "rev"},
		Diff:   func() (string, error) { return "diff", nil },
		Gate:   func(context.Context) (string, string, error) { return "none", "", nil },
	}
	res, err := ExecuteScript(context.Background(), PairScript(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1 {
		t.Fatalf("rounds = %d, want 1: no gate means no second round", res.Rounds)
	}
	found := false
	for _, e := range events {
		if e == "no_gate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no no_gate event; events = %v", events)
	}
}
