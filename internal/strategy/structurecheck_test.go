package strategy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

func sectioned(text string, secs ...agent.Section) *agent.Outcome {
	return &agent.Outcome{Text: text, Parsed: secs}
}

// The last architect turn of a council is the one nobody reviews. A
// revision that loses the Implements: lines its draft carried is caught by
// the harness and sent back once, before a person ever sees it.
func TestARevisionThatLosesStructureIsSentBackOnce(t *testing.T) {
	var events []string
	var prompts []string
	architectTurns := 0
	withImpl := agent.Section{ID: "SPEC-001", Title: "Shell", Body: "**Implements:** REQ-001\n\nGTK4."}
	without := agent.Section{ID: "SPEC-001", Title: "Shell", Body: "GTK4, revised."}
	params := &ExecuteParams{
		OnEvent: func(kind string, data map[string]interface{}) { events = append(events, kind) },
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				if architectTurns >= 3 {
					return verdictOutcome("approve"), nil
				}
				return verdictOutcome("request-changes"), nil
			}
			architectTurns++
			prompts = append(prompts, prompt)
			switch architectTurns {
			case 1:
				return sectioned("draft", withImpl), nil
			case 2:
				return sectioned("revised without implements", without), nil
			default:
				return sectioned("revised properly", withImpl), nil
			}
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	if _, err := ExecuteScript(context.Background(), CouncilScript("SPEC", nil), params); err != nil {
		t.Fatal(err)
	}
	if architectTurns < 3 {
		t.Fatalf("architect turns = %d, want the revision retried once", architectTurns)
	}
	found := false
	for _, e := range events {
		if e == "structure_check" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no structure_check event; events = %v", events)
	}
	retry := prompts[2]
	if !strings.Contains(retry, "Structure check") || !strings.Contains(retry, "SPEC-001 has no **Implements:** line") {
		t.Fatalf("the retry prompt does not name the defect:\n%s", retry)
	}
}

func TestStructureThatDoesNotConvergeFailsClosed(t *testing.T) {
	var events []string
	bad := agent.Section{ID: "SPEC-001", Title: "Shell", Body: "missing implements"}
	params := &ExecuteParams{
		OnEvent: func(kind string, _ map[string]interface{}) { events = append(events, kind) },
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				return verdictOutcome("request-changes"), nil
			}
			return sectioned("bad", bad), nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	_, err := ExecuteScript(context.Background(), CouncilScript("SPEC", nil), params)
	if !errors.Is(err, ErrStructureFailed) {
		t.Fatalf("non-converging structure error = %v, want ErrStructureFailed", err)
	}
	if !slices.Contains(events, "structure_failed") {
		t.Fatalf("structure_failed event missing: %v", events)
	}
}

func TestStructureRepairTargetsOneMilestoneAndMergesItsSection(t *testing.T) {
	baseText := "# Plan\n\n## M-001 — Setup\n\n### T-001 — Build\n\nold setup\n\n## M-002 — UI\n\n### T-002 — Window\n\nkeep this exactly"
	base := sectioned(baseText,
		agent.Section{ID: "M-001", Title: "Setup", Body: "### T-001 — Build\n\nold setup"},
		agent.Section{ID: "M-002", Title: "UI", Body: "### T-002 — Window\n\nkeep this exactly"},
	)
	findings := []string{
		"T-001 has no **Produces:** artifacts",
		"T-001 has no **Exercises:** artifacts",
		"T-002 has no **Produces:** artifacts",
	}
	note := structureRepairNote(findings, sectionsOf(base))
	if !strings.Contains(note, "Return ONLY") || !strings.Contains(note, "M-001") || strings.Contains(note, "T-002 has no") {
		t.Fatalf("repair note is not bounded to the first milestone:\n%s", note)
	}
	patch := sectioned("## M-001 — Setup\n\n### T-001 — Build\n\nnew bounded setup",
		agent.Section{ID: "M-001", Title: "Setup", Body: "### T-001 — Build\n\nnew bounded setup"})
	merged := mergeStructureRepair(base, patch, "markdown_sections:M")
	if !strings.Contains(merged.Text, "new bounded setup") || !strings.Contains(merged.Text, "keep this exactly") || strings.Contains(merged.Text, "old setup") {
		t.Fatalf("bounded repair was not merged into the complete checkpoint:\n%s", merged.Text)
	}
	if len(sectionsOf(merged)) != 2 {
		t.Fatalf("merged sections = %d, want complete two-section plan", len(sectionsOf(merged)))
	}
}

// A revision byte-identical to the previous draft ends the council: another
// round would spend minutes and tokens to change nothing.
func TestAnIdenticalRevisionEndsTheCouncil(t *testing.T) {
	var events []string
	draft := agent.Section{ID: "REQ-001", Title: "Capture", Body: "The app captures the screen.\n\n**Priority:** must"}
	params := &ExecuteParams{
		OnEvent: func(kind string, data map[string]interface{}) { events = append(events, kind) },
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				return verdictOutcome("request-changes"), nil
			}
			return sectioned("## REQ-001 — Capture\n\nThe app captures the screen.", draft), nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	res, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1 {
		t.Fatalf("rounds = %d, want 1: an identical revision must not buy a second round", res.Rounds)
	}
	found := false
	for _, e := range events {
		if e == "revision_identical" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no revision_identical event; events = %v", events)
	}
}
