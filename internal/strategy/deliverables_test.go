package strategy

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

// The task's bullets are the contract; out-of-scope bullets are not; a task
// with no bullets is one deliverable — itself. T-119's exact body.
func TestExtractDeliverablesFromATaskBody(t *testing.T) {
	body := `Add nullable image_url columns.

- Add nullable ` + "`image_url`" + ` VARCHAR columns to exercises and user_exercises through a migration.
- Existing rows remain valid with null values.
  - sub-bullet elaborating the parent
- Update seed data with catalog image references for a representative subset.
- Ensure read-only catalog API serialization can return the catalog image_url value.

**Out of scope:** User-uploaded files, server-side image storage.
- CDN configuration
`
	got := ExtractDeliverables("Add image_url columns", body)
	if len(got) != 4 {
		t.Fatalf("got %d deliverables, want 4: %q", len(got), got)
	}
	if !strings.HasPrefix(got[0], "Add nullable") || !strings.HasPrefix(got[3], "Ensure read-only") {
		t.Errorf("wrong deliverables: %q", got)
	}
	for _, d := range got {
		if strings.Contains(d, "CDN") || strings.Contains(d, "sub-bullet") {
			t.Errorf("out-of-scope or sub-bullet leaked into deliverables: %q", d)
		}
	}
	if got := ExtractDeliverables("Fix the login redirect", "Just fix it."); len(got) != 1 || got[0] != "Fix the login redirect" {
		t.Errorf("a bullet-less task must be one deliverable, its title: %q", got)
	}
}

// The report is parsed tolerantly: fenced, trailing, odd ids and statuses.
func TestParseDeliverablesReportIsTolerant(t *testing.T) {
	text := "Changed two files.\n\n```json\n{\"deliverables\":[{\"id\":1,\"status\":\"done\"},{\"id\":\"2\",\"status\":\"Not Done\",\"note\":\"blocked on migration\"},{\"id\":9,\"status\":\"done\"},{\"id\":3,\"status\":\"maybe\"}]}\n```"
	rep := ParseDeliverablesReport(text, 4)
	if rep.Unreported {
		t.Fatal("report not found")
	}
	if len(rep.Items) != 2 || rep.Items[1].ID != 2 || rep.Items[1].Status != "not_done" {
		t.Errorf("parsed %+v", rep.Items)
	}
	if got := rep.Undelivered(); len(got) != 1 || got[0] != 2 {
		t.Errorf("undelivered = %v", got)
	}
	if got := rep.Missing(4); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Errorf("missing = %v", got)
	}
	if !ParseDeliverablesReport("I did the work.", 4).Unreported {
		t.Error("a reply without a report must read as unreported")
	}
}

// An implementer that reports #4 undelivered summons the duck — even with a
// clean tool trace; the reviewer gets ids and statuses, never the note.
func TestUndeliveredReportSummonsTheDuckAndInformsTheReviewerAsData(t *testing.T) {
	rec := &recorder{}
	report := `Done most of it.
{"deliverables":[{"id":1,"status":"done"},{"id":2,"status":"done"},{"id":3,"status":"done"},{"id":4,"status":"blocked","note":"the catalog serializer is generated and I could not find the template"}]}`
	params := pairParams(rec, "green",
		&agent.Outcome{Text: report},
		&agent.Outcome{Parsed: map[string]interface{}{"action": "none"}},
		verdictOutcome("approve"),
	)
	params.Deliverables = []string{"Add columns", "Keep rows valid", "Update seed data", "Serialize image_url in the catalog API"}
	params.Roster[config.RoleAdvisor] = "pato-duck"
	var reportEvents, gapEvents []map[string]interface{}
	params.OnEvent = func(kind string, data map[string]interface{}) {
		switch kind {
		case "deliverables_report":
			reportEvents = append(reportEvents, data)
		case "deliverables_gap":
			gapEvents = append(gapEvents, data)
		}
	}
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	advisorAt, reviewerAt, implAt := -1, -1, -1
	for i, role := range rec.roles {
		switch role {
		case config.RoleAdvisor:
			advisorAt = i
		case config.RoleReviewer:
			reviewerAt = i
		case config.RoleImplementer:
			if implAt < 0 {
				implAt = i
			}
		}
	}
	if advisorAt < 0 {
		t.Fatal("an undelivered deliverable did not summon the duck")
	}
	if !strings.Contains(rec.prompts[implAt], "Deliverables — your work contract") || !strings.Contains(rec.prompts[implAt], "4. Serialize image_url") {
		t.Errorf("implementer prompt lacks the numbered contract:\n%s", rec.prompts[implAt])
	}
	if !strings.Contains(rec.prompts[advisorAt], "could not find the template") || !strings.Contains(rec.prompts[advisorAt], "[4] undelivered") {
		t.Errorf("duck prompt lacks the report and its note:\n%s", rec.prompts[advisorAt])
	}
	rp := rec.prompts[reviewerAt]
	if !strings.Contains(rp, `"id":4,"status":"blocked"`) {
		t.Errorf("reviewer prompt lacks the statuses as data:\n%s", rp)
	}
	if strings.Contains(rp, "could not find the template") {
		t.Errorf("reviewer prompt leaked the implementer's note:\n%s", rp)
	}
	if len(reportEvents) == 0 || reportEvents[0]["unreported"] != false {
		t.Errorf("deliverables_report not recorded: %v", reportEvents)
	}
	if len(gapEvents) != 1 {
		t.Errorf("approve over an undelivered item must record deliverables_gap: %v", gapEvents)
	}
}

// No report is data for the reviewer, not distress: a seat learning the
// contract must not summon the duck every turn.
// Three reports that leave the same item blocked are evidence that progress,
// rather than merely effort, has stalled.  Escalation is a suggestion at the
// decision point, not an extra turn: it records the stalled item, the other
// measured thresholds, and both the reseat and brief-repair alternatives.
func TestStalledDeliverableEmitsEscalationSuggestionOnlyAfterTheThirdReport(t *testing.T) {
	rec := &recorder{}
	var events []struct {
		kind string
		data map[string]interface{}
	}
	report := func(status string) *agent.Outcome {
		return &agent.Outcome{Text: `{"deliverables":[{"id":1,"status":"done"},{"id":2,"status":"` + status + `"}]}`}
	}
	params := &ExecuteParams{
		Prompt:       "Task T-107",
		Deliverables: []string{"first", "stalled item"},
		Rounds:       3,
		Runner:       rec.runner(report("blocked"), report("blocked"), report("blocked")),
		Gate: func(context.Context) (string, string, error) {
			return "red", "", nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "current"},
		OnEvent: func(kind string, data map[string]interface{}) {
			events = append(events, struct {
				kind string
				data map[string]interface{}
			}{kind, data})
		},
	}
	script := &Script{
		Name: "escalation-fixture", MaxRounds: 3, Until: "round == 99",
		Turns: []Turn{{Role: config.RoleImplementer, Toolbelt: "full", MaxTurns: 4}},
	}
	if _, err := ExecuteScript(context.Background(), script, params); err != nil {
		t.Fatal(err)
	}

	suggestionAt, thirdReportAt := -1, -1
	for i, event := range events {
		if event.kind == "deliverables_report" {
			thirdReportAt = i
		}
		if event.kind == "escalation_suggestion" {
			if suggestionAt >= 0 {
				t.Fatalf("got more than one escalation suggestion: %+v", events)
			}
			suggestionAt = i
			shown := strings.ToLower(strings.TrimSpace(fmt.Sprint(event.data)))
			for _, want := range []string{"stuck", "2", "turn", "red", "relaunch", "improve", "continue"} {
				if !strings.Contains(shown, want) {
					t.Errorf("suggestion does not narrate %q: %+v", want, event.data)
				}
			}
		}
	}
	if suggestionAt < 0 {
		t.Fatalf("three reports with deliverable #2 blocked emitted no escalation_suggestion: %+v", events)
	}
	if suggestionAt <= thirdReportAt {
		t.Fatalf("suggestion fired before the third consecutive stalled report (suggestion=%d third report=%d)", suggestionAt, thirdReportAt)
	}
}

// Equal effort is not a capacity signal by itself: when the checklist moves,
// the run must not propose a costly reseat just because it took three turns.
func TestConvergingChecklistAtEqualTurnCountDoesNotEmitEscalationSuggestion(t *testing.T) {
	rec := &recorder{}
	var suggested bool
	report := func(items string) *agent.Outcome {
		return &agent.Outcome{Text: `{"deliverables":` + items + `}`}
	}
	params := &ExecuteParams{
		Prompt:       "Task T-107",
		Deliverables: []string{"first", "second", "third"},
		Rounds:       3,
		Runner: rec.runner(
			report(`[{"id":1,"status":"done"},{"id":2,"status":"blocked"}]`),
			report(`[{"id":1,"status":"done"},{"id":2,"status":"done"},{"id":3,"status":"blocked"}]`),
			report(`[{"id":1,"status":"done"},{"id":2,"status":"done"},{"id":3,"status":"done"}]`),
		),
		Gate:   func(context.Context) (string, string, error) { return "green", "", nil },
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "current"},
		OnEvent: func(kind string, _ map[string]interface{}) {
			suggested = suggested || kind == "escalation_suggestion"
		},
	}
	script := &Script{
		Name: "converging-fixture", MaxRounds: 3, Until: "round == 99",
		Turns: []Turn{{Role: config.RoleImplementer, Toolbelt: "full", MaxTurns: 4}},
	}
	if _, err := ExecuteScript(context.Background(), script, params); err != nil {
		t.Fatal(err)
	}
	if suggested {
		t.Fatal("a converging checklist at the same turn count suggested escalation")
	}
}

func TestNoStrongerCandidateEmitsNoEscalationSuggestion(t *testing.T) {
	var suggested bool
	params := &ExecuteParams{
		EscalationCandidates: []EscalationCandidate{{ID: "peer", WilsonFloor: 61}},
		CurrentLowerBound:    61,
		OnEvent:              func(kind string, _ map[string]interface{}) { suggested = kind == "escalation_suggestion" },
	}
	emitEscalationSuggestion(params, escalationEvidence{StuckItem: 2, StuckReports: 3}, "failed_run")
	if suggested {
		t.Fatal("equal Wilson floor must not produce an escalation suggestion")
	}
}

func TestUnreportedDeliverablesDoNotSummonTheDuck(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "green",
		&agent.Outcome{Text: "Did it all.", ToolCalls: []agent.ToolCallRecord{{Name: "fs_write", Result: &tools.Result{Content: "ok"}}}},
		verdictOutcome("approve"),
	)
	params.Deliverables = []string{"A", "B"}
	params.Roster[config.RoleAdvisor] = "pato-duck"
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	for i, role := range rec.roles {
		if role == config.RoleAdvisor {
			t.Fatal("the duck was summoned for a missing report")
		}
		if role == config.RoleReviewer && !strings.Contains(rec.prompts[i], "filed no report") {
			t.Errorf("reviewer not told the report is missing:\n%s", rec.prompts[i])
		}
	}
}
