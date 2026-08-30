package strategy

import (
	"context"
	"strings"
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

// A fragment revision returns only the sections it changes. The next critic
// must judge the accumulated amendment, not mistake that latest patch for the
// whole candidate (Neocapture corrida 9: REQ-006 and REQ-009 looked deleted).
func TestAFragmentCouncilCarriesTheMaterializedCandidate(t *testing.T) {
	script := CouncilScript("REQ", nil)
	script.FragmentPrefix = "REQ"
	for i := range script.Turns {
		if script.Turns[i].Role == config.RoleArchitect {
			script.Turns[i].Contract = ""
		}
	}
	architectTurns, reviewerTurns := 0, 0
	params := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				reviewerTurns++
				if reviewerTurns == 2 {
					for _, want := range []string{"REQ-001", "REQ-006", "REQ-009"} {
						if !strings.Contains(prompt, want) {
							t.Errorf("round-2 critic lost %s:\n%s", want, prompt)
						}
					}
					return verdictOutcome("approve"), nil
				}
				return verdictOutcome("request-changes"), nil
			}
			architectTurns++
			switch architectTurns {
			case 1:
				return &agent.Outcome{Text: "## REQ-001 — Trigger\n\nOld.\n\n## REQ-006 — Keyboard\n\nOut.\n\n## REQ-009 — Saving\n\nRequired."}, nil
			default:
				return &agent.Outcome{Text: "## REQ-001 — User action\n\nRevised."}, nil
			}
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REQ-001 — User action", "REQ-006 — Keyboard", "REQ-009 — Saving"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("final materialized candidate lost %q:\n%s", want, res.Text)
		}
	}
}
