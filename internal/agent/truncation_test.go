package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

func truncationLoop(fake provider.Provider) *Loop {
	return &Loop{
		Provider: fake,
		Duckling: &DucklingConfig{ID: "pato", Model: "m", Caps: provider.Capabilities{NativeTools: true}},
		Registry: tools.NewRegistry(),
		Budget:   budget.NewTracker(&budget.Budget{MaxUSD: 10, MaxTokens: 1e6, MaxTurns: 50, MaxWallclockS: 600}),
		MaxTurns: 4,
	}
}

// The retry after a truncated reply never reached the model.
//
// It appended the nudge to `conversation` and resent the same req, whose
// Messages had been assigned before the append and carries its own length. So
// the retry was the identical request, and it produced the identical answer.
//
// That cost a real run. A model deliberating in circles — "let me implement
// this now… actually, I just realized…" a dozen times over — filled its 4096
// output tokens, got a retry that said nothing new, filled them again, and the
// run was marked FAILED.
func TestTheTruncationRetryActuallySaysSomething(t *testing.T) {
	fake := provider.NewFake("f")
	var sent []provider.ChatRequest
	fake.ScriptFunc = func(req provider.ChatRequest, callCount int) *provider.ChatResponse {
		sent = append(sent, req)
		if callCount == 1 {
			// Ran out of room mid-deliberation.
			return &provider.ChatResponse{Choices: []provider.Choice{{
				Message:      provider.Message{Role: "assistant", Content: "Let me think about this. Actually,"},
				FinishReason: provider.FinishLength,
			}}}
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: "No change is needed."},
			FinishReason: provider.FinishStop,
		}}}
	}

	turn := &Turn{Role: config.RoleImplementer, Prompt: "Do the thing.", Contract: "freeform", MaxTurns: 4}
	out, err := RunTurn(context.Background(), truncationLoop(fake), turn,
		&tools.ExecContext{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("a recoverable truncation failed the turn: %v", err)
	}
	if !strings.Contains(out.Text, "No change is needed") {
		t.Errorf("the retried answer was lost: %q", out.Text)
	}

	if len(sent) < 2 {
		t.Fatalf("only %d request(s) were made; the retry did not happen", len(sent))
	}
	// The whole point: the second request must differ from the first.
	first, second := sent[0], sent[1]
	if len(second.Messages) <= len(first.Messages) {
		t.Fatalf("the retry carried %d messages against the first's %d — the nudge never reached the model",
			len(second.Messages), len(first.Messages))
	}
	last := second.Messages[len(second.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "cut off") {
		t.Errorf("the nudge is not in the retry: %+v", last)
	}
	// And it must tell the model what to do instead of deliberating, or it
	// will deliberate again.
	if !strings.Contains(last.Content, "Stop deliberating") {
		t.Errorf("the nudge does not say to stop: %q", last.Content)
	}
	// A model that has concluded there is nothing to do needs somewhere to
	// land other than another lap.
	if !strings.Contains(last.Content, "needs no change") {
		t.Errorf("the nudge offers no way to conclude: %q", last.Content)
	}
}

// Twice in a row is a model that cannot be talked down, and the turn is over.
func TestTruncatedTwiceStillFails(t *testing.T) {
	fake := provider.NewFake("f")
	fake.ScriptFunc = func(_ provider.ChatRequest, _ int) *provider.ChatResponse {
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: "and another thing,"},
			FinishReason: provider.FinishLength,
		}}}
	}
	turn := &Turn{Role: config.RoleImplementer, Prompt: "Do the thing.", Contract: "freeform", MaxTurns: 4}
	if _, err := RunTurn(context.Background(), truncationLoop(fake), turn,
		&tools.ExecContext{ProjectRoot: t.TempDir()}); err == nil {
		t.Error("two truncations in a row were treated as success")
	}
}
