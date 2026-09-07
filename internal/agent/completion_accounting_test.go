package agent

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

func TestCompletionBreakdownUsesProviderReasoningTokens(t *testing.T) {
	got := completionBreakdown(provider.Usage{CompletionTokens: 100, ReasoningTokens: 73}, provider.Message{
		Content: "answer", Reasoning: "private work",
	})
	if got["source"] != "provider" || got["reasoning_tokens"] != 73 || got["content_tokens"] != 27 {
		t.Fatalf("breakdown = %#v", got)
	}
	if got["unattributed_tokens"] != 0 {
		t.Errorf("provider split left tokens unattributed: %#v", got)
	}
}

func TestCompletionBreakdownEstimatesSeparatedLocalReasoning(t *testing.T) {
	got := completionBreakdown(provider.Usage{CompletionTokens: 120}, provider.Message{
		Content: "short answer", Reasoning: "a much longer private chain of reasoning that the local endpoint separated",
	})
	reasoning := got["reasoning_tokens"].(int)
	content := got["content_tokens"].(int)
	if got["source"] != "proportional_text_estimate" {
		t.Fatalf("source = %v, breakdown = %#v", got["source"], got)
	}
	if reasoning <= content || reasoning+content != 120 {
		t.Errorf("estimated split reasoning=%d content=%d, want reasoning dominant and total 120", reasoning, content)
	}
	if got["reasoning_bytes"].(int) == 0 || got["content_bytes"].(int) == 0 {
		t.Errorf("raw observations missing: %#v", got)
	}
}

func TestUsageMapRecordsContentOnlyAndUnattributedCompletions(t *testing.T) {
	content := usageMap(provider.Usage{CompletionTokens: 40}, provider.Message{Content: "visible"})
	contentSplit := content["completion_breakdown"].(map[string]interface{})
	if contentSplit["source"] != "observed_content_only" || contentSplit["content_tokens"] != 40 {
		t.Errorf("content split = %#v", contentSplit)
	}

	empty := usageMap(provider.Usage{CompletionTokens: 9}, provider.Message{})
	emptySplit := empty["completion_breakdown"].(map[string]interface{})
	if emptySplit["source"] != "unattributed" || emptySplit["unattributed_tokens"] != 9 {
		t.Errorf("empty split = %#v", emptySplit)
	}
}

func TestRunTurnPersistsReasoningAndItsPerCallSplit(t *testing.T) {
	fake := provider.NewFake("local")
	fake.ScriptFunc = func(_ provider.ChatRequest, _ int) *provider.ChatResponse {
		return &provider.ChatResponse{
			Choices: []provider.Choice{{
				Message:      provider.Message{Role: "assistant", Content: "final answer", Reasoning: "private deliberation that took most of the reply"},
				FinishReason: provider.FinishStop,
			}},
			Usage: provider.Usage{PromptTokens: 20, CompletionTokens: 60},
		}
	}
	w := &recordingWriter{}
	loop := &Loop{
		Provider: fake,
		Duckling: &DucklingConfig{ID: "small-local", Provider: "openai-compat", Model: "small"},
		Budget: budget.NewTracker(&budget.Budget{
			MaxUSD: 1, MaxTokens: 1000, MaxTurns: 2, MaxWallclockS: 60,
		}),
		MaxTurns:  1,
		RunWriter: w,
	}
	turn := &Turn{Role: config.RoleArchitect, Prompt: "answer", Contract: "freeform", MaxTurns: 1}
	if _, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(w.calls))
	}
	call := w.calls[0]
	if call.Response["reasoning"] != "private deliberation that took most of the reply" {
		t.Errorf("reasoning was not persisted: %#v", call.Response)
	}
	split := call.Usage["completion_breakdown"].(map[string]interface{})
	if split["source"] != "proportional_text_estimate" {
		t.Errorf("breakdown = %#v", split)
	}
	if split["reasoning_tokens"].(int)+split["content_tokens"].(int) != 60 {
		t.Errorf("breakdown does not reconcile to completion usage: %#v", split)
	}
}
