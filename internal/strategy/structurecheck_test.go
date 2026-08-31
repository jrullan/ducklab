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
	var architectToolbelts [][]string
	architectTurns := 0
	withImpl := agent.Section{ID: "SPEC-001", Title: "Shell", Body: "**Implements:** REQ-001\n\nGTK4."}
	without := agent.Section{ID: "SPEC-001", Title: "Shell", Body: "GTK4, revised."}
	params := &ExecuteParams{
		OnEvent: func(kind string, data map[string]interface{}) { events = append(events, kind) },
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, toolbelt []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				if architectTurns >= 3 {
					return verdictOutcome("approve"), nil
				}
				return verdictOutcome("request-changes"), nil
			}
			architectTurns++
			prompts = append(prompts, prompt)
			architectToolbelts = append(architectToolbelts, append([]string{}, toolbelt...))
			switch architectTurns {
			case 1:
				return sectioned("## SPEC-001 — Shell\n\n**Implements:** REQ-001\n\nGTK4.", withImpl), nil
			case 2:
				return sectioned("## SPEC-001 — Shell\n\nGTK4, revised.", without), nil
			default:
				return sectioned("## SPEC-001 — Shell\n\n**Implements:** REQ-001\n\nGTK4.", withImpl), nil
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
	if len(architectToolbelts[2]) != 0 {
		t.Fatalf("bounded repair tools = %v, want none", architectToolbelts[2])
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

func TestStructureRepairTargetsBothSidesOfACrossMilestoneFinding(t *testing.T) {
	base := sectioned("",
		agent.Section{ID: "M-001", Body: "### T-001 — Core"},
		agent.Section{ID: "M-002", Body: "### T-002 — UI"},
	)
	findings := []string{"T-001 and T-002 both **Produce:** src/shared.h — one artifact needs one owner"}
	_, ids := structureRepairInstruction(findings, sectionsOf(base))
	if !slices.Equal(ids, []string{"M-001", "M-002"}) {
		t.Fatalf("repair sections = %v, want both sides of the collision", ids)
	}
}

func TestStructureRepairDoesNotAssignAThreeSectionChainToTwoSections(t *testing.T) {
	base := sectioned("",
		agent.Section{ID: "M-001", Body: "### T-001 — A"},
		agent.Section{ID: "M-002", Body: "### T-002 — B"},
		agent.Section{ID: "M-003", Body: "### T-003 — C"},
	)
	first := "T-001 and T-002 both **Produce:** src/ab.h"
	outside := "T-002 and T-003 both **Produce:** src/bc.h"
	batch, ids := structureRepairBatch([]string{first, outside}, sectionsOf(base))
	if !slices.Equal(ids, []string{"M-001", "M-002"}) || !slices.Equal(batch, []string{first}) {
		t.Fatalf("repair batch = %v for %v; want only the finding wholly owned by M-001/M-002", batch, ids)
	}
}

func TestStructureRepairFindsEveryParentOfADuplicatedTaskID(t *testing.T) {
	base := sectioned("",
		agent.Section{ID: "M-001", Body: "### T-004 — Portal"},
		agent.Section{ID: "M-002", Body: "### T-004 — Portal duplicate"},
	)
	_, ids := structureRepairInstruction([]string{"T-004 is declared more than once"}, sectionsOf(base))
	if !slices.Equal(ids, []string{"M-001", "M-002"}) {
		t.Fatalf("duplicate task parents = %v, want both milestones", ids)
	}
}

func TestBoundedRepairRejectsUnexpectedOrMissingSectionsAtomically(t *testing.T) {
	baseText := "## M-001 — Core\n\nold core\n\n## M-002 — UI\n\nold ui"
	base := sectioned(baseText,
		agent.Section{ID: "M-001", Title: "Core", Body: "old core"},
		agent.Section{ID: "M-002", Title: "UI", Body: "old ui"},
	)
	patch := sectioned("## M-001 — Core\n\nnew core\n\n## M-002 — UI\n\nmodel also rewrote ui",
		agent.Section{ID: "M-001", Title: "Core", Body: "new core"},
		agent.Section{ID: "M-002", Title: "UI", Body: "model also rewrote ui"},
	)
	merged, err := mergeStructureRepairScoped(base, patch, "markdown_sections:M", []string{"M-001"})
	if !errors.Is(err, ErrStructureRepairScope) {
		t.Fatalf("scope error = %v, want ErrStructureRepairScope", err)
	}
	if merged.Text != baseText {
		t.Fatalf("rejected transaction changed the checkpoint:\n%s", merged.Text)
	}

	missing := sectioned("## M-001 — Core\n\nnew core", agent.Section{ID: "M-001", Title: "Core", Body: "new core"})
	if _, err := mergeStructureRepairScoped(base, missing, "markdown_sections:M", []string{"M-001", "M-002"}); !errors.Is(err, ErrStructureRepairScope) {
		t.Fatalf("missing-section error = %v, want ErrStructureRepairScope", err)
	}
	if _, err := mergeStructureRepairScoped(base, missing, "markdown_sections:M", []string{}); !errors.Is(err, ErrStructureRepairScope) {
		t.Fatalf("empty assignment error = %v, want ErrStructureRepairScope", err)
	}
}

func TestPartialCouncilRevisionIsMaterializedOverCompleteDraft(t *testing.T) {
	baseText := "## SPEC-001 — Core\n\n**Implements:** REQ-001\n\nkeep core\n\n## SPEC-002 — UI\n\n**Implements:** REQ-002\n\nold ui"
	base := sectioned(baseText,
		agent.Section{ID: "SPEC-001", Title: "Core", Body: "**Implements:** REQ-001\n\nkeep core"},
		agent.Section{ID: "SPEC-002", Title: "UI", Body: "**Implements:** REQ-002\n\nold ui"},
	)
	revision := sectioned("## SPEC-002 — UI\n\n**Implements:** REQ-002\n\nnew ui",
		agent.Section{ID: "SPEC-002", Title: "UI", Body: "**Implements:** REQ-002\n\nnew ui"})
	merged, ids := materializePartialRevision(base, revision, "markdown_sections:SPEC")
	if !slices.Equal(ids, []string{"SPEC-002"}) {
		t.Fatalf("materialized sections = %v, want SPEC-002", ids)
	}
	if !strings.Contains(merged.Text, "keep core") || !strings.Contains(merged.Text, "new ui") || strings.Contains(merged.Text, "old ui") {
		t.Fatalf("partial revision did not preserve the complete checkpoint:\n%s", merged.Text)
	}
	if findings := structureFindings(sectionsOf(base), sectionsOf(merged), "markdown_sections:SPEC", nil, false, merged.Text); len(findings) != 0 {
		t.Fatalf("materialized revision has findings: %v", findings)
	}
}

func TestCouncilKeepsCompleteCheckpointWhenArchitectReturnsOneChangedSection(t *testing.T) {
	architectTurns, reviewerTurns := 0, 0
	var events []string
	params := &ExecuteParams{
		OnEvent: func(kind string, _ map[string]interface{}) { events = append(events, kind) },
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				reviewerTurns++
				if reviewerTurns == 1 {
					return verdictOutcome("request-changes"), nil
				}
				return verdictOutcome("approve"), nil
			}
			architectTurns++
			if architectTurns == 1 {
				return sectioned("## SPEC-001 — Core\n\n**Implements:** REQ-001\n\nkeep core\n\n## SPEC-002 — UI\n\n**Implements:** REQ-002\n\nold ui",
					agent.Section{ID: "SPEC-001", Title: "Core", Body: "**Implements:** REQ-001\n\nkeep core"},
					agent.Section{ID: "SPEC-002", Title: "UI", Body: "**Implements:** REQ-002\n\nold ui"}), nil
			}
			return sectioned("## SPEC-002 — UI\n\n**Implements:** REQ-002\n\nnew ui",
				agent.Section{ID: "SPEC-002", Title: "UI", Body: "**Implements:** REQ-002\n\nnew ui"}), nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	res, err := ExecuteScript(context.Background(), CouncilScript("SPEC", nil), params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "keep core") || !strings.Contains(res.Text, "new ui") {
		t.Fatalf("proposal lost part of its checkpoint:\n%s", res.Text)
	}
	if !slices.Contains(events, "revision_materialized") || slices.Contains(events, "structure_failed") {
		t.Fatalf("events = %v, want materialization without structure failure", events)
	}
}

func TestStructureRepairStopsAfterThreeNonImprovingPatches(t *testing.T) {
	architectTurns := 0
	params := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				return verdictOutcome("request-changes"), nil
			}
			architectTurns++
			body := "**Implements:** REQ-001\n\nrevision " + string(rune('a'+architectTurns))
			return sectioned("## SPEC-001 — Shell\n\n"+body, agent.Section{ID: "SPEC-001", Title: "Shell", Body: body}), nil
		},
		StructureCheck: func(text string) []string { return []string{"still invalid: " + text[len(text)-1:]} },
		Roster:         map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	_, err := ExecuteScript(context.Background(), CouncilScript("SPEC", nil), params)
	if !errors.Is(err, ErrStructureFailed) {
		t.Fatalf("error = %v, want ErrStructureFailed", err)
	}
	if architectTurns != 4 {
		t.Fatalf("architect turns = %d, want initial draft plus three non-improving patches", architectTurns)
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
