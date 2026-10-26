package strategy

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// B-522: an escalation card fits the seat whose evidence it carries. A
// task-less run (a document stage, a release) whose seat died here was told to
// improve a task body it did not have.
func TestATasklessRunsEscalationOffersNoTaskActions(t *testing.T) {
	var suggestion map[string]interface{}
	params := &ExecuteParams{
		Prompt: "Draft the release notes.",
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "k3"},
		Runner: func(context.Context, *Turn, config.DucklingID, string, []string, TurnContext) (*agent.Outcome, error) {
			return nil, agent.ErrNoAnswer
		},
		OnEvent: func(kind string, data map[string]interface{}) {
			if kind == "escalation_suggestion" {
				suggestion = data
			}
		},
	}
	if _, err := ExecuteScript(context.Background(), SoloScript(), params); !errors.Is(err, agent.ErrNoAnswer) {
		t.Fatalf("err = %v, want ErrNoAnswer", err)
	}
	if suggestion == nil {
		t.Fatal("the evidence card itself must survive; only the task-shaped advice goes")
	}
	actions, _ := suggestion["actions"].([]string)
	if slices.Contains(actions, "improve_task_body") || slices.Contains(actions, "relaunch_with_stronger_seat") {
		t.Fatalf("task-less run offered task actions: %v", actions)
	}
	diagnoses, _ := suggestion["diagnoses"].(map[string]interface{})
	if _, ok := diagnoses["task_brief_quality"]; ok {
		t.Fatalf("task-less run diagnosed a task brief: %v", diagnoses)
	}
	if suggestion["role"] != "implementer" || suggestion["seat"] != "k3" {
		t.Fatalf("card does not name the seat it is about: role=%v seat=%v", suggestion["role"], suggestion["seat"])
	}
}

// The service's candidates are implementer seats. Evidence from any other
// seat has no same-role candidate, so no card offers to replace a reviewer
// with an implementer.
func TestImplementerCandidatesNeverRideAnotherSeatsCard(t *testing.T) {
	var suggested map[string]interface{}
	params := &ExecuteParams{
		TaskID:               "T-001",
		EscalationCandidates: []EscalationCandidate{{ID: "strong-implementer", WilsonFloor: 90}},
		CurrentLowerBound:    10,
		OnEvent: func(kind string, data map[string]interface{}) {
			if kind == "escalation_suggestion" {
				suggested = data
			}
		},
	}
	emitEscalationSuggestion(params, escalationEvidence{UnansweredDeath: true}, "failed_run", config.RoleReviewer)
	if suggested != nil {
		t.Fatalf("a reviewer's card carried an implementer candidate: %v", suggested)
	}
	emitEscalationSuggestion(params, escalationEvidence{UnansweredDeath: true}, "failed_run", config.RoleImplementer)
	if suggested == nil || suggested["candidate"].(EscalationCandidate).ID != "strong-implementer" {
		t.Fatalf("the implementer's card lost its candidate: %v", suggested)
	}
	if actions, _ := suggested["actions"].([]string); !slices.Contains(actions, "improve_task_body") {
		t.Fatalf("a task run's implementer card lost the brief action: %v", actions)
	}
}
