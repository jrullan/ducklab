package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

// streamingProvider emits deltas and assembles them into a final response.
type streamingProvider struct {
	chunks      []string
	unsupported bool
	chatCalls   int
	mu          sync.Mutex
}

func (p *streamingProvider) ID() string { return "streaming" }

func (p *streamingProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.mu.Lock()
	p.chatCalls++
	p.mu.Unlock()
	return provider.ChatResponse{
		Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: strings.Join(p.chunks, "")},
			FinishReason: provider.FinishStop,
		}},
		Usage: provider.Usage{PromptTokens: 10, CompletionTokens: 5},
	}, nil
}

func (p *streamingProvider) ChatStream(ctx context.Context, req provider.ChatRequest, ch chan<- provider.Delta) (provider.ChatResponse, error) {
	if p.unsupported {
		return provider.ChatResponse{}, provider.ErrUnsupported
	}
	for _, c := range p.chunks {
		ch <- provider.Delta{Text: c}
	}
	return provider.ChatResponse{
		Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: strings.Join(p.chunks, "")},
			FinishReason: provider.FinishStop,
		}},
		Usage: provider.Usage{PromptTokens: 10, CompletionTokens: 5},
	}, nil
}

func (p *streamingProvider) Models(ctx context.Context) ([]string, error) { return nil, nil }

// AC-26: with OnDelta set, streamed text is delivered chunk by chunk.
func TestStreamingEmitsDeltas(t *testing.T) {
	p := &streamingProvider{chunks: []string{"func ", "Add(", "a, b int", ") int"}}
	loop := testLoop(p, 2)

	var mu sync.Mutex
	var got []string
	loop.OnDelta = func(_ *Turn, text string) {
		mu.Lock()
		got = append(got, text)
		mu.Unlock()
	}

	turn := &Turn{Role: config.RoleImplementer, Prompt: "write Add", Contract: "freeform", MaxTurns: 1}
	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(p.chunks) {
		t.Fatalf("got %d deltas, want %d: %v", len(got), len(p.chunks), got)
	}
	// The assembled text, not the deltas, is what the run works from.
	if out.Text != strings.Join(p.chunks, "") {
		t.Errorf("final text = %q, want the assembled stream", out.Text)
	}
}

// Without OnDelta nothing streams: streaming is opt-in per run.
func TestNoDeltaCallbackMeansNoStreaming(t *testing.T) {
	p := &streamingProvider{chunks: []string{"a", "b"}}
	loop := testLoop(p, 2)
	turn := &Turn{Role: config.RoleImplementer, Prompt: "x", Contract: "freeform", MaxTurns: 1}

	if _, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.chatCalls != 1 {
		t.Errorf("Chat called %d times, want 1 (the non-streaming path)", p.chatCalls)
	}
}

// A provider that cannot stream must still produce output for a watcher,
// rather than showing nothing for the whole turn.
func TestUnsupportedStreamingFallsBackToOneDelta(t *testing.T) {
	p := &streamingProvider{chunks: []string{"hello world"}, unsupported: true}
	loop := testLoop(p, 2)

	var got []string
	loop.OnDelta = func(_ *Turn, text string) {
		got = append(got, text)
	}
	turn := &Turn{Role: config.RoleImplementer, Prompt: "x", Contract: "freeform", MaxTurns: 1}
	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "hello world" {
		t.Errorf("fallback deltas = %v, want one delta with the full text", got)
	}
	if out.Text != "hello world" {
		t.Errorf("text = %q", out.Text)
	}
}

// I11: a delta consumer that panics or blocks must not corrupt the run. The
// run's correctness comes from the assembled response, never the deltas.
func TestSlowDeltaConsumerDoesNotAffectTheResult(t *testing.T) {
	p := &streamingProvider{chunks: []string{"a", "b", "c", "d", "e"}}
	loop := testLoop(p, 2)

	var count int
	var mu sync.Mutex
	loop.OnDelta = func(_ *Turn, text string) {
		mu.Lock()
		count++
		mu.Unlock()
		// A consumer doing slow work must not change the outcome.
		for i := 0; i < 1000; i++ {
			_ = i * i
		}
	}
	turn := &Turn{Role: config.RoleImplementer, Prompt: "x", Contract: "freeform", MaxTurns: 1}
	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "abcde" {
		t.Errorf("text = %q, want abcde", out.Text)
	}
	mu.Lock()
	defer mu.Unlock()
	if count != 5 {
		t.Errorf("delivered %d deltas, want 5", count)
	}
}

// Deltas carry the role and duckling so a UI can put text in the right lane.
func TestDeltasCarryRoleAndDuckling(t *testing.T) {
	p := &streamingProvider{chunks: []string{"x"}}
	loop := testLoop(p, 2)

	var gotRole config.Role
	var gotDuckling config.DucklingID
	loop.OnDelta = func(tn *Turn, _ string) {
		gotRole, gotDuckling = tn.Role, loop.Duckling.ID
	}
	turn := &Turn{Role: config.RoleReviewer, Prompt: "x", Contract: "freeform", MaxTurns: 1}
	if _, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if gotRole != config.RoleReviewer {
		t.Errorf("role = %q, want reviewer", gotRole)
	}
	if gotDuckling != "pato-test" {
		t.Errorf("duckling = %q, want pato-test", gotDuckling)
	}
}
