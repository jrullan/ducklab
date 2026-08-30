package strategy

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// A configured reviewer cap serves code reviews of large diffs; it must not
// raise a document critic's six calls — the draft is in its prompt.
func TestAConfiguredReviewerCapDoesNotRaiseADocumentCritic(t *testing.T) {
	var criticCap, architectCap int
	params := &ExecuteParams{
		TurnCaps: map[config.Role]int{config.RoleReviewer: 100, config.RoleArchitect: 40},
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				criticCap = turn.MaxTurns
				return verdictOutcome("approve"), nil
			}
			architectCap = turn.MaxTurns
			return &agent.Outcome{Text: "## REQ-001 — Draft\n\nBody."}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	if criticCap != 6 {
		t.Fatalf("critic cap = %d, want the script's 6 (configured 100 must not raise it)", criticCap)
	}
	if architectCap != 40 {
		t.Fatalf("architect cap = %d, want the configured 40", architectCap)
	}
}
