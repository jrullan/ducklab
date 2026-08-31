package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

type deadlineProvider struct{}

func (deadlineProvider) ID() string { return "deadline" }

func (deadlineProvider) Chat(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	<-ctx.Done()
	return provider.ChatResponse{}, ctx.Err()
}

func (deadlineProvider) ChatStream(context.Context, provider.ChatRequest, chan<- provider.Delta) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, provider.ErrUnsupported
}

func (deadlineProvider) Models(context.Context) ([]string, error) { return nil, nil }

// The Neocapture plan's final local-model call started inside the budget and
// returned 139 seconds later, after crossing it. The call itself must inherit
// the remaining run deadline rather than waiting for the next loop check.
func TestWallclockBudgetCancelsAnInflightProviderCall(t *testing.T) {
	tracker := budget.NewTracker(&budget.Budget{MaxWallclockS: 1, MaxTokens: 1_000_000, MaxTurns: 10})
	tracker.Spend.RestoreWallclock(0.95)
	loop := &Loop{
		Provider: deadlineProvider{},
		Duckling: &DucklingConfig{ID: "local", Model: "slow"},
		Registry: tools.NewRegistry(), Budget: tracker, MaxTurns: 1,
	}
	started := time.Now()
	_, err := RunTurn(context.Background(), loop, &Turn{Role: config.RoleArchitect, Prompt: "draft", Contract: "freeform"},
		&tools.ExecContext{ProjectRoot: t.TempDir()})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("error = %v, want ErrBudgetExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("provider survived past the remaining deadline: %s", elapsed)
	}
}
