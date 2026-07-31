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

type recordingWriter struct{ calls []*LLMCallRecord }

func (w *recordingWriter) AppendLLM(c *LLMCallRecord) error {
	w.calls = append(w.calls, c)
	return nil
}

// Only successful calls were written down. A run that died on its third attempt
// left two entries in llm.jsonl and no trace of the one that killed it — and
// that file is the one place that could have said what was sent.
//
// Measured on a real triage: two calls recorded, the third returned a response
// with no choices, and the run reported an error naming our parser with nothing
// behind it to look at.
func TestACallThatFailedIsStillRecorded(t *testing.T) {
	fake := provider.NewFake("f")
	fake.ScriptFunc = func(_ provider.ChatRequest, callCount int) *provider.ChatResponse {
		if callCount == 1 {
			return &provider.ChatResponse{
				Choices: []provider.Choice{{
					Message: provider.Message{
						Role:    "assistant",
						Content: "Looking.\n```ducklab\n{\"tool\":\"fs_list\",\"args\":{\"path\":\".\"}}\n```",
					},
					FinishReason: provider.FinishStop,
				}},
				Usage: provider.Usage{PromptTokens: 700, CompletionTokens: 100},
			}
		}
		return nil // the fake answers an error for an unscripted call
	}

	w := &recordingWriter{}
	loop := &Loop{
		Provider: fake,
		Duckling: &DucklingConfig{ID: "k3", Provider: "openrouter", Model: "moonshotai/kimi-k3"},
		Registry: tools.NewRegistry(),
		Budget: budget.NewTracker(&budget.Budget{
			MaxUSD: 10, MaxTokens: 1e6, MaxTurns: 50, MaxWallclockS: 600,
		}),
		MaxTurns:  4,
		RunWriter: w,
	}
	turn := &Turn{Role: config.RoleTriager, Prompt: "Classify it.", Contract: "freeform",
		Toolbelt: []string{"fs_list"}, MaxTurns: 4}

	_, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err == nil {
		t.Fatal("the call did not fail, so this proves nothing")
	}

	var failed *LLMCallRecord
	for _, c := range w.calls {
		if c.FinishReason == "error" {
			failed = c
		}
	}
	if failed == nil {
		t.Fatalf("the failing call left no record; %d call(s) written", len(w.calls))
	}
	// The same request shape as a call that worked, or the one entry anybody
	// would want to read would be the one written differently.
	if failed.Request["messages"] == nil {
		t.Errorf("the record does not carry what was sent: %+v", failed.Request)
	}
	if failed.Duckling != "k3" || failed.Model != "moonshotai/kimi-k3" {
		t.Errorf("the record does not say who was asked: %+v", failed)
	}
	if msg, _ := failed.Response["error"].(string); msg == "" {
		t.Errorf("the record does not say what went wrong: %+v", failed.Response)
	}
	_ = errors.Is(err, err)
}
