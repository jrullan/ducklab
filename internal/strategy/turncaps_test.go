package strategy

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// The person's configured role cap must beat the script's baked-in number on
// the MAIN execution path, as TurnCaps has always documented. It did not:
// ExecuteScript read t.MaxTurns raw, so a test-first implementer ran at
// TestFirstScript's hardcoded 24 while role_turns said 100 and the Settings
// fallback said 40 — builds only survived because applyRoleTurns patches the
// script by another route. Terra died reading a 30-file project at 24 calls
// with every configured number decorative.
func TestTurnCapsOverrideTheScriptsBakedInCap(t *testing.T) {
	var got int
	params := &ExecuteParams{
		Prompt:   "write the failing test",
		Roster:   map[config.Role]config.DucklingID{config.RoleImplementer: "pato-uno"},
		TurnCaps: map[config.Role]int{config.RoleImplementer: 100},
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			got = turn.MaxTurns
			return &agent.Outcome{Text: "done"}, nil
		},
	}
	if _, err := ExecuteScript(context.Background(), TestFirstScript(), params); err != nil {
		t.Fatal(err)
	}
	if got != 100 {
		t.Fatalf("turn ran with MaxTurns=%d; the configured role cap (100) must override the script's 24", got)
	}
}

// Absent a configured cap, the script's own number still stands.
func TestAScriptCapStandsWhenNoRoleCapIsConfigured(t *testing.T) {
	var got int
	params := &ExecuteParams{
		Prompt: "write the failing test",
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "pato-uno"},
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			got = turn.MaxTurns
			return &agent.Outcome{Text: "done"}, nil
		},
	}
	if _, err := ExecuteScript(context.Background(), TestFirstScript(), params); err != nil {
		t.Fatal(err)
	}
	if got != 24 {
		t.Fatalf("turn ran with MaxTurns=%d; with no configured cap the script's 24 stands", got)
	}
}
