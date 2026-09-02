package strategy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

func readCall(name, args string) agent.ToolCallRecord {
	return agent.ToolCallRecord{Name: name, Args: json.RawMessage(args), Result: &tools.Result{Content: "ok"}}
}

// An architect that comes back — for its revision, or after a pause — is a
// fresh conversation. It is handed what its earlier turns already read, so
// it does not survey the project again (17 tool calls and 22 minutes of
// re-reading across two resumptions, Neocapture 2026-08-29).
func TestARevisingArchitectIsToldWhatItAlreadyRead(t *testing.T) {
	var prompts []string
	architectTurns := 0
	params := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			switch turn.Role {
			case config.RoleReviewer:
				if architectTurns >= 2 {
					return verdictOutcome("approve"), nil
				}
				return verdictOutcome("request-changes"), nil
			default:
				architectTurns++
				prompts = append(prompts, prompt)
				if architectTurns == 1 {
					return &agent.Outcome{Text: "## REQ-001 — Draft\n\nBody.", ToolCalls: []agent.ToolCallRecord{
						readCall("artifact_read", `{"kind":"requirements"}`),
						readCall("fs_list", `{"depth":2}`),
						readCall("fs_read", `{"path":".ducklab/project.toml"}`),
					}}, nil
				}
				return &agent.Outcome{Text: "## REQ-001 — Draft\n\nRevised."}, nil
			}
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	if len(prompts) < 2 {
		t.Fatalf("architect turns = %d, want a draft and a revision", len(prompts))
	}
	if strings.Contains(prompts[0], "What you already read") {
		t.Fatal("the first draft was told it had read something")
	}
	revision := prompts[1]
	for _, want := range []string{"## What you already read in this run", "artifact_read", "fs_list", ".ducklab/project.toml", "do not read them again"} {
		if !strings.Contains(revision, want) {
			t.Errorf("the revision prompt lacks %q:\n%s", want, revision)
		}
	}
}

func TestAResumedArchitectIsHandedTheInterruptedTurnsReads(t *testing.T) {
	var first string
	params := &ExecuteParams{
		ResumeFrom: &ResumeTurn{Round: 1, Index: 0, Role: config.RoleArchitect, Notes: "half a draft",
			Looked: []string{"artifact_read kind=spec", "fs_read .ducklab/docs/requirements.md"}},
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				return verdictOutcome("approve"), nil
			}
			if first == "" {
				first = prompt
			}
			return &agent.Outcome{Text: "## REQ-001 — Draft\n\nBody."}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"What you already read", "artifact_read kind=spec", "requirements.md", "Resume checkpoint", "continue, do not restart"} {
		if !strings.Contains(first, want) {
			t.Errorf("the resumed architect prompt lacks %q:\n%s", want, first)
		}
	}
}

func TestAResumedTurnReceivesThePartialDraftNotTheInternalEnvelope(t *testing.T) {
	raw := `{"draft":"review so far: alpha is wrong","reasoning":"private scratchpad","tool_calls":[{"name":"fs_read"}]}`
	got := resumeCheckpointNotes(raw)
	if got != "review so far: alpha is wrong" {
		t.Fatalf("checkpoint notes = %q, want only the saved draft", got)
	}
	for _, leaked := range []string{"private scratchpad", "tool_calls", "fs_read"} {
		if strings.Contains(got, leaked) {
			t.Errorf("checkpoint leaked %q: %s", leaked, got)
		}
	}
}
