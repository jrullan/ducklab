package strategy

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

func TestCancellationAtTurnEndDoesNotStartAGate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	gateStarted := false
	params := &ExecuteParams{
		Runner: func(context.Context, *Turn, config.DucklingID, string, []string, TurnContext) (*agent.Outcome, error) {
			return &agent.Outcome{Text: "work is durable"}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "impl"},
		Gate: func(context.Context) (string, string, error) {
			gateStarted = true
			return "green", "", nil
		},
		OnEvent: func(kind string, data map[string]interface{}) {
			if kind == "turn_end" && data["incomplete"] != true {
				cancel()
			}
		},
	}
	_, err := ExecuteScript(ctx, SoloScript(), params)
	if err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if gateStarted {
		t.Fatal("gate started after a safe-point pause cancelled the run")
	}
}

// A run recorded turn_start and turn_end around a turn whose content was never
// written down: eleven events and not one carrying what a model said. The text
// fed the internal transcript the whole time; it just never left the process,
// so the desktop's conversation lanes had nothing to render.
func TestATurnRecordsWhatWasSaidAndDone(t *testing.T) {
	var kinds []string
	var message map[string]interface{}
	var toolCall map[string]interface{}

	params := &ExecuteParams{
		OnEvent: func(kind string, data map[string]interface{}) {
			kinds = append(kinds, kind)
			switch kind {
			case "message":
				message = data
			case "tool_call":
				toolCall = data
			}
		},
	}
	outcome := &agent.Outcome{
		Text:      "  I fixed add.go.  ",
		TokensIn:  120,
		TokensOut: 34,
		ToolCalls: []agent.ToolCallRecord{{
			Name:   "fs_patch",
			Args:   json.RawMessage(`{"path":"add.go"}`),
			Result: &tools.Result{Content: "1 edit applied"},
		}},
	}
	emitMessage(params, 1, 0, config.RoleImplementer, "pato-uno", outcome)

	if !slices.Contains(kinds, "message") {
		t.Fatal("no message event: the turn's content was not recorded")
	}
	if got := message["content"]; got != "I fixed add.go." {
		t.Errorf("content = %q, want the trimmed text", got)
	}
	if message["role"] != "implementer" || message["duckling"] != "pato-uno" {
		t.Errorf("message lost its attribution: %+v", message)
	}

	if toolCall == nil {
		t.Fatal("no tool_call event: the timeline has nothing to draw")
	}
	if toolCall["tool"] != "fs_patch" || toolCall["ok"] != true {
		t.Errorf("tool_call = %+v", toolCall)
	}
}

// A turn that said nothing must not leave an empty bubble in the lane.
func TestAnEmptyTurnEmitsNoMessage(t *testing.T) {
	var kinds []string
	params := &ExecuteParams{
		OnEvent: func(kind string, _ map[string]interface{}) { kinds = append(kinds, kind) },
	}
	emitMessage(params, 1, 0, config.RoleReviewer, "pato-dos", &agent.Outcome{Text: "   "})
	if slices.Contains(kinds, "message") {
		t.Error("an empty turn produced a message event")
	}
}

// I3: nothing unbounded. One fs_read can return a whole file, and forty of
// them would make the run log larger than the repository it describes.
func TestToolResultsAreBounded(t *testing.T) {
	var toolCall map[string]interface{}
	params := &ExecuteParams{
		OnEvent: func(kind string, data map[string]interface{}) {
			if kind == "tool_call" {
				toolCall = data
			}
		},
	}
	huge := strings.Repeat("x", 50_000)
	emitMessage(params, 1, 0, config.RoleImplementer, "pato-uno", &agent.Outcome{
		Text: "read it",
		ToolCalls: []agent.ToolCallRecord{{
			Name: "fs_read", Result: &tools.Result{Content: huge},
		}},
	})
	got, _ := toolCall["result"].(string)
	if len(got) > maxToolResultBytes+64 {
		t.Errorf("result kept %d bytes; it must be summarised", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Error("the summary does not say it dropped anything")
	}
}
