package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

// Every mid-loop failure returned a nil outcome, so the tool calls, tokens and
// cost the turn had already accumulated were thrown away before its caller could
// record them. A run that patched a file seventeen times and then hit its token
// ceiling left a transcript of four events.
func TestABudgetFailureReturnsWhatTheTurnAlreadyDid(t *testing.T) {
	fake := provider.NewFake("f")
	fake.ScriptFunc = func(_ provider.ChatRequest, callCount int) *provider.ChatResponse {
		// One tool call, then keep going until the budget stops the loop.
		if callCount == 1 {
			return &provider.ChatResponse{
				Choices: []provider.Choice{{
					Message: provider.Message{
						Role: "assistant",
						Content: "Reading it.\n```ducklab\n" +
							`{"tool":"fs_list","args":{"path":"."}}` + "\n```",
					},
					FinishReason: provider.FinishStop,
				}},
				Usage: provider.Usage{PromptTokens: 900, CompletionTokens: 40},
			}
		}
		return &provider.ChatResponse{
			Choices: []provider.Choice{{
				Message:      provider.Message{Role: "assistant", Content: "Still working."},
				FinishReason: provider.FinishStop,
			}},
			Usage: provider.Usage{PromptTokens: 900, CompletionTokens: 40},
		}
	}

	loop := &Loop{
		Provider: fake,
		Duckling: &DucklingConfig{ID: "k3", Model: "m"},
		Registry: tools.NewRegistry(),
		// Small enough that the second iteration's check trips it.
		Budget:   budget.NewTracker(&budget.Budget{MaxUSD: 10, MaxTokens: 500, MaxTurns: 50, MaxWallclockS: 600}),
		MaxTurns: 8,
	}
	turn := &Turn{Role: config.RoleImplementer, Prompt: "Do it.", Contract: "freeform",
		Toolbelt: []string{"fs_list"}, MaxTurns: 8}

	outcome, err := RunTurn(context.Background(), loop, turn,
		&tools.ExecContext{ProjectRoot: t.TempDir()})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want a budget failure", err)
	}
	if outcome == nil {
		t.Fatal("the partial outcome was discarded, so the caller has nothing to record")
	}
	if len(outcome.ToolCalls) == 0 {
		t.Error("the tool call the turn made before it died was lost")
	}
	if outcome.TokensIn == 0 {
		t.Error("the tokens it spent were lost, so the run cannot report them")
	}
}
