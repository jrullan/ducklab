package strategy

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// A council's second round opens on the revision the first round closed
// with: draft → critique → revision → critique → revision, not draft →
// critique → revision → DRAFT AGAIN → critique → revision.
func TestACouncilsSecondRoundOpensOnTheRevision(t *testing.T) {
	architectTurns := 0
	var events []string
	params := &ExecuteParams{
		OnEvent: func(kind string, data map[string]interface{}) { events = append(events, kind) },
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				return verdictOutcome("request-changes"), nil
			}
			architectTurns++
			return &agent.Outcome{Text: "## REQ-001 — Draft\n\nBody v" + string(rune('0'+architectTurns)), Parsed: []agent.Section{{ID: "REQ-001", Title: "Draft", Body: "Body"}}}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	if architectTurns != 3 {
		t.Fatalf("architect turns = %d, want 3 (draft, revision, revision) — not a re-draft at the top of round 2", architectTurns)
	}
	carried := false
	for _, e := range events {
		if e == "draft_carried" {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("no draft_carried event; events = %v", events)
	}
}
