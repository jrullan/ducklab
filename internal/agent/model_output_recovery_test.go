package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

func TestReasoningChannelToolCallIsSalvaged(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fact.txt"), []byte("the fact\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := provider.NewFake("local")
	fake.ScriptFunc = func(_ provider.ChatRequest, call int) *provider.ChatResponse {
		if call == 1 {
			return &provider.ChatResponse{
				Choices: []provider.Choice{{
					Message:      provider.Message{Role: "assistant", Reasoning: "```ducklab\n{\"tool\":\"fs_read\",\"args\":{\"path\":\"fact.txt\"}}\n```"},
					FinishReason: provider.FinishStop,
				}},
				Usage: provider.Usage{PromptTokens: 20, CompletionTokens: 24},
			}
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message: provider.Message{Role: "assistant", Content: "Read the fact."}, FinishReason: provider.FinishStop,
		}}}
	}
	var events []string
	loop := truncationLoop(fake)
	loop.Duckling.Caps.NativeTools = false
	loop.OnRecovery = func(_ *Turn, kind string, _ map[string]interface{}) { events = append(events, kind) }
	turn := &Turn{Role: config.RoleImplementer, Prompt: "Read it.", Contract: "freeform", Toolbelt: []string{"fs_read"}, MaxTurns: 4}
	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: root})
	if err != nil {
		t.Fatalf("salvage failed: %v", err)
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Name != "fs_read" || !strings.Contains(out.ToolCalls[0].Result.Content, "the fact") {
		t.Fatalf("salvaged calls = %+v", out.ToolCalls)
	}
	if len(events) != 1 || events[0] != "tool_call_salvaged_from_reasoning" {
		t.Fatalf("recovery events = %v", events)
	}
	requests := fake.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider calls = %d, want salvage plus continuation", len(requests))
	}
	for _, message := range requests[1].Messages {
		if message.Role == "assistant" && strings.Contains(message.Content, "```ducklab") {
			t.Fatalf("private reasoning was replayed verbatim: %+v", message)
		}
	}
}

func TestBareReasoningToolObjectIsSalvagedButProseIsNot(t *testing.T) {
	call := parseReasoningToolCall(`{"tool":"fs_read","args":{"path":"x"}}`)
	if call == nil || call.Name != "fs_read" {
		t.Fatalf("bare reasoning call = %+v", call)
	}
	if call := parseReasoningToolCall(`I might use {"tool":"fs_read","args":{"path":"x"}} later.`); call != nil {
		t.Fatalf("reasoning prose became executable: %+v", call)
	}
}

func TestTruncatedNativeToolCallDoesNotPoisonNextRequest(t *testing.T) {
	fake := provider.NewFake("vllm")
	invalid := provider.ToolCall{ID: "cut", Type: "function"}
	invalid.Function.Name = "fs_write"
	invalid.Function.Arguments = `{"path":"large.c","content":"unterminated`
	fake.ScriptFunc = func(req provider.ChatRequest, call int) *provider.ChatResponse {
		if call == 1 {
			return &provider.ChatResponse{
				Choices: []provider.Choice{{
					Message: provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{invalid}},
					// Some OpenAI-compatible servers report tool_calls, not length,
					// even though usage reached max_tokens.
					FinishReason: provider.FinishToolCalls,
				}},
				Usage: provider.Usage{PromptTokens: 100, CompletionTokens: 20},
			}
		}
		for _, message := range req.Messages {
			for _, toolCall := range message.ToolCalls {
				if !json.Valid([]byte(toolCall.Function.Arguments)) {
					t.Fatalf("request %d replayed invalid tool arguments: %+v", call, toolCall)
				}
			}
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != "user" || !strings.Contains(last.Content, "cut at max_tokens=20") || !strings.Contains(last.Content, "fs_patch") {
			t.Fatalf("recovery nudge = %+v", last)
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message: provider.Message{Role: "assistant", Content: "I will split the edit."}, FinishReason: provider.FinishStop,
		}}}
	}
	maxTokens := 20
	loop := truncationLoop(fake)
	loop.Duckling.Params.MaxTokens = &maxTokens
	var events []string
	loop.OnRecovery = func(_ *Turn, kind string, _ map[string]interface{}) { events = append(events, kind) }
	turn := &Turn{Role: config.RoleImplementer, Prompt: "Edit it.", Contract: "freeform", Toolbelt: []string{"fs_write"}, MaxTurns: 4}
	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("truncation recovery failed: %v", err)
	}
	if len(out.ToolCalls) != 0 || !strings.Contains(out.Text, "split the edit") {
		t.Fatalf("outcome = %+v", out)
	}
	if len(events) != 1 || events[0] != "tool_call_truncated" {
		t.Fatalf("recovery events = %v", events)
	}
}

func TestThoughtOnlyRetryChangesTheRequest(t *testing.T) {
	fake := provider.NewFake("local")
	fake.ScriptFunc = func(_ provider.ChatRequest, _ int) *provider.ChatResponse {
		return &provider.ChatResponse{
			Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Reasoning: "brief thought"}, FinishReason: provider.FinishStop}},
			Usage:   provider.Usage{PromptTokens: 10, CompletionTokens: 4},
		}
	}
	loop := truncationLoop(fake)
	loop.Duckling.Caps.NativeTools = false
	turn := &Turn{Role: config.RoleReviewer, Prompt: "Review.", Contract: "freeform", MaxTurns: 2}
	_, runErr := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if runErr == nil {
		t.Fatal("genuinely thought-only replies unexpectedly succeeded")
	}
	if !strings.Contains(runErr.Error(), "brief thought") {
		t.Fatalf("diagnosis omits the actual reasoning evidence: %v", runErr)
	}
	requests := fake.Requests()
	if len(requests) != 3 || len(requests[1].Messages) <= len(requests[0].Messages) || len(requests[2].Messages) <= len(requests[1].Messages) {
		t.Fatalf("thought-only retry requests = %d; retry did not change shape", len(requests))
	}
	if last := requests[1].Messages[len(requests[1].Messages)-1]; last.Role != "user" || !strings.Contains(last.Content, "outside the thinking block") {
		t.Fatalf("thought-only nudge = %+v", last)
	}
}
