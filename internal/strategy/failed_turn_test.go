package strategy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

// A turn that died took its whole record with it.
//
// ExecuteScript returned on the runner's error before emitting anything, and
// agent.RunTurn returned a nil outcome from every mid-loop failure — so the work
// was discarded twice over. A real run patched index.html seventeen times and
// then hit its token ceiling: its transcript held four events, not one of them
// naming a tool call, and the only way to see what had happened was to read
// llm.jsonl by hand.
//
// The failure is exactly when that record is worth most.
func TestAFailedTurnStillRecordsWhatItDid(t *testing.T) {
	var kinds []string
	var message map[string]interface{}
	var toolCalls []map[string]interface{}
	var turnEnd map[string]interface{}

	partial := &agent.Outcome{
		Text:      "I was partway through when the budget ran out.",
		TokensIn:  420022,
		TokensOut: 16317,
		ToolCalls: []agent.ToolCallRecord{
			{Name: "fs_patch", Args: json.RawMessage(`{"path":"index.html"}`), Result: &tools.Result{Content: "ok"}},
			{Name: "fs_read", Args: json.RawMessage(`{"path":"index.html"}`), Result: &tools.Result{Content: "ok"}},
		},
	}

	params := &ExecuteParams{
		Prompt: "Integrate locked constraints.",
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "k3"},
		Runner: func(context.Context, *Turn, config.DucklingID, string, []string, TurnContext) (*agent.Outcome, error) {
			return partial, errors.New("budget exceeded: token budget exceeded: 436339 >= 400000")
		},
		OnEvent: func(kind string, data map[string]interface{}) {
			kinds = append(kinds, kind)
			switch kind {
			case "message":
				message = data
			case "tool_call":
				toolCalls = append(toolCalls, data)
			case "turn_end":
				turnEnd = data
			}
		},
	}

	if _, err := ExecuteScript(context.Background(), SoloScript(), params); err == nil {
		t.Fatal("the failure was swallowed")
	}

	if message == nil {
		t.Error("what the model said before it died was never recorded")
	}
	if len(toolCalls) != 2 {
		t.Errorf("got %d tool_call events, want the 2 the turn made: %v", len(toolCalls), kinds)
	}
	// And the turn must be marked as not having finished, or a reader would take
	// a partial record for a complete one.
	if turnEnd == nil {
		t.Fatal("no turn_end for the failed turn")
	}
	if turnEnd["incomplete"] != true {
		t.Errorf("the turn is not marked incomplete: %+v", turnEnd)
	}
}

// A turn that finished is not marked incomplete, or the flag would mean nothing.
func TestACompletedTurnIsNotMarkedIncomplete(t *testing.T) {
	var turnEnd map[string]interface{}
	params := &ExecuteParams{
		Prompt: "Do the thing.",
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "k3"},
		Runner: func(context.Context, *Turn, config.DucklingID, string, []string, TurnContext) (*agent.Outcome, error) {
			return &agent.Outcome{Text: "Done."}, nil
		},
		OnEvent: func(kind string, data map[string]interface{}) {
			if kind == "turn_end" {
				turnEnd = data
			}
		},
	}
	if _, err := ExecuteScript(context.Background(), SoloScript(), params); err != nil {
		t.Fatal(err)
	}
	if _, marked := turnEnd["incomplete"]; marked {
		t.Errorf("a completed turn carries the incomplete flag: %+v", turnEnd)
	}
}
