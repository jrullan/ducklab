package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

func TestTextProtocolReceivesLiveToolAvailabilityAfterResearchCloses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fact.txt"), []byte("known\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := provider.NewFake("f")
	fake.ScriptFunc = func(req provider.ChatRequest, call int) *provider.ChatResponse {
		content := "```ducklab\n{\"tool\":\"fs_read\",\"args\":{\"path\":\"fact.txt\"}}\n```"
		if call == 2 {
			last := req.Messages[len(req.Messages)-1].Content
			for _, want := range []string{"RUNTIME TOOL UPDATE", "fs_read", "CLOSED", "fs_write", "AVAILABLE"} {
				if !strings.Contains(last, want) {
					t.Errorf("live update lacks %q:\n%s", want, last)
				}
			}
			content = "The evidence is sufficient; ready to implement."
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message: provider.Message{Role: "assistant", Content: content}, FinishReason: provider.FinishStop,
		}}, Usage: provider.Usage{PromptTokens: 10, CompletionTokens: 10}}
	}
	loop := &Loop{
		Provider: fake,
		Duckling: &DucklingConfig{ID: "small", Provider: "local", Model: "m"},
		Registry: tools.NewRegistry(),
		Budget: budget.NewTracker(&budget.Budget{
			MaxUSD: 10, MaxTokens: 1e6, MaxTurns: 20, MaxWallclockS: 60,
		}),
	}
	turn := &Turn{Role: config.RoleImplementer, Prompt: "Implement it.", Contract: "freeform",
		Toolbelt: []string{"fs_read", "fs_write"}, MaxTurns: 3}
	outcome, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{
		ProjectRoot: dir, ExplorationCallLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Text != "The evidence is sufficient; ready to implement." {
		t.Fatalf("outcome = %q", outcome.Text)
	}
}

func TestTextProtocolReassertsAdvisorToolbeltAndClosesAfterSecondViolation(t *testing.T) {
	fake := provider.NewFake("f")
	fake.ScriptFunc = func(req provider.ChatRequest, call int) *provider.ChatResponse {
		content := "```ducklab\n{\"tool\":\"fs_write\",\"args\":{\"path\":\"nope\",\"content\":\"x\"}}\n```"
		switch call {
		case 2:
			last := req.Messages[len(req.Messages)-1].Content
			for _, want := range []string{"RUNTIME TOOL UPDATE", "fs_read", "AVAILABLE"} {
				if !strings.Contains(last, want) {
					t.Errorf("role-boundary update lacks %q:\n%s", want, last)
				}
			}
			if strings.Contains(last, "fs_write") {
				t.Errorf("out-of-toolbelt tool advertised in live update:\n%s", last)
			}
			content = "```ducklab\n{\"tool\":\"verify_run\",\"args\":{}}\n```"
		case 3:
			last := req.Messages[len(req.Messages)-1].Content
			if !strings.Contains(last, "all tools are now closed") {
				t.Errorf("second violation did not close tools:\n%s", last)
			}
			content = "Read-only advice based on the supplied evidence."
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message: provider.Message{Role: "assistant", Content: content}, FinishReason: provider.FinishStop,
		}}, Usage: provider.Usage{PromptTokens: 10, CompletionTokens: 10}}
	}
	loop := &Loop{
		Provider: fake,
		Duckling: &DucklingConfig{ID: "small", Provider: "local", Model: "m"},
		Registry: tools.NewRegistry(),
		Budget: budget.NewTracker(&budget.Budget{
			MaxUSD: 10, MaxTokens: 1e6, MaxTurns: 20, MaxWallclockS: 60,
		}),
	}
	turn := &Turn{Role: config.RoleAdvisor, Prompt: "Advise.", Contract: "freeform",
		Toolbelt: []string{"fs_read"}, MaxTurns: 4}
	outcome, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Text != "Read-only advice based on the supplied evidence." {
		t.Fatalf("outcome = %q", outcome.Text)
	}
	if len(outcome.ToolCalls) != 2 || !outcome.ToolCalls[1].Result.EndTurn {
		t.Fatalf("tool calls = %+v", outcome.ToolCalls)
	}
}
