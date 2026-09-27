package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

type silentChatProvider struct{}

func (silentChatProvider) ID() string { return "silent" }
func (silentChatProvider) Chat(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	<-ctx.Done()
	return provider.ChatResponse{}, ctx.Err()
}
func (silentChatProvider) ChatStream(context.Context, provider.ChatRequest, chan<- provider.Delta) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, provider.ErrUnsupported
}
func (silentChatProvider) Models(context.Context) ([]string, error) { return nil, nil }

func TestNonStreamingCallUsesAdaptiveStallBound(t *testing.T) {
	loop := &Loop{Provider: silentChatProvider{}, NonStreamingTimeout: 10 * time.Millisecond}
	turn := &Turn{Role: config.RoleImplementer}
	var reported time.Duration
	loop.OnProviderStall = func(_ *Turn, bound time.Duration) { reported = bound }

	started := time.Now()
	_, err := chatNonStreaming(context.Background(), loop, turn, provider.ChatRequest{}, 40*time.Millisecond)
	elapsed := time.Since(started)
	if !errors.Is(err, provider.ErrProviderUnavailable) {
		t.Fatalf("error = %v, want provider unavailable", err)
	}
	if reported != 60*time.Millisecond {
		t.Fatalf("reported bound = %s, want 60ms", reported)
	}
	if elapsed < 45*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("call elapsed %s, want the adaptive ~60ms bound", elapsed)
	}
}

func TestParentDeadlineWinsWithoutProviderStallEvent(t *testing.T) {
	loop := &Loop{Provider: silentChatProvider{}, NonStreamingTimeout: time.Second}
	turn := &Turn{Role: config.RoleImplementer}
	reported := false
	loop.OnProviderStall = func(_ *Turn, _ time.Duration) { reported = true }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := chatNonStreaming(ctx, loop, turn, provider.ChatRequest{}, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want caller deadline", err)
	}
	if reported {
		t.Fatal("parent deadline was mislabeled as a provider stall")
	}
}
