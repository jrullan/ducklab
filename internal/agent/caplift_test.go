package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

// A reviewer died on exactly its hundredth call — the cap — and the only
// remedy was resuming into the same ceiling. The lift must land on a reply
// already in flight: checked before every call, not read once at the top.
func TestALiftedCapReachesTheReplyInFlight(t *testing.T) {
	var lifted atomic.Bool

	fake := provider.NewFake("f")
	fake.ScriptFunc = func(_ provider.ChatRequest, callCount int) *provider.ChatResponse {
		// Two tool calls, then — beyond the original cap of 2 — the answer.
		content := "Looking.\n```ducklab\n{\"tool\":\"fs_list\",\"args\":{\"path\":\".\"}}\n```"
		if callCount == 2 {
			// The person saw it circling and lifted the cap mid-reply.
			lifted.Store(true)
		}
		if callCount == 3 {
			content = "Done looking. All fine."
		}
		return &provider.ChatResponse{
			Choices: []provider.Choice{{
				Message:      provider.Message{Role: "assistant", Content: content},
				FinishReason: provider.FinishStop,
			}},
			Usage: provider.Usage{PromptTokens: 100, CompletionTokens: 50},
		}
	}

	loop := &Loop{
		Provider: fake,
		Duckling: &DucklingConfig{ID: "rev", Provider: "openrouter", Model: "m"},
		Registry: tools.NewRegistry(),
		Budget: budget.NewTracker(&budget.Budget{
			MaxUSD: 10, MaxTokens: 1e6, MaxTurns: 50, MaxWallclockS: 600,
		}),
		CapLift: lifted.Load,
	}
	turn := &Turn{Role: config.RoleReviewer, Prompt: "Review it.", Contract: "freeform",
		Toolbelt: []string{"fs_list"}, MaxTurns: 2}

	outcome, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("the lift did not reach the loop: %v", err)
	}
	if outcome.Text != "Done looking. All fine." {
		t.Errorf("text = %q, want the answer the third call gave", outcome.Text)
	}
}

// The control: without the lift, the same script hits the cap — the loop
// stops looking and falls to the tools-withheld "answer NOW" call. Proven by
// that call's own instruction appearing in what the provider was sent, which
// the lifted twin never triggers.
func TestAnUnliftedCapStillBinds(t *testing.T) {
	var sawConclude atomic.Bool
	fake := provider.NewFake("f")
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		content := "Looking.\n```ducklab\n{\"tool\":\"fs_list\",\"args\":{\"path\":\".\"}}\n```"
		if last := req.Messages[len(req.Messages)-1]; last.Role == "user" &&
			strings.Contains(last.Content, "out of tool calls") {
			sawConclude.Store(true)
			content = "Could not finish verifying."
		}
		return &provider.ChatResponse{
			Choices: []provider.Choice{{
				Message:      provider.Message{Role: "assistant", Content: content},
				FinishReason: provider.FinishStop,
			}},
			Usage: provider.Usage{PromptTokens: 100, CompletionTokens: 50},
		}
	}
	loop := &Loop{
		Provider: fake,
		Duckling: &DucklingConfig{ID: "rev", Provider: "openrouter", Model: "m"},
		Registry: tools.NewRegistry(),
		Budget: budget.NewTracker(&budget.Budget{
			MaxUSD: 10, MaxTokens: 1e6, MaxTurns: 50, MaxWallclockS: 600,
		}),
	}
	turn := &Turn{Role: config.RoleReviewer, Prompt: "Review it.", Contract: "freeform",
		Toolbelt: []string{"fs_list"}, MaxTurns: 2}

	if _, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()}); err != nil {
		t.Fatalf("exhaustion with a conclusion is not an error: %v", err)
	}
	if !sawConclude.Load() {
		t.Error("the cap never bound: the tools-withheld conclude call was not made")
	}
}
