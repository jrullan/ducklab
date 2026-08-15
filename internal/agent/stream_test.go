package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

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
// capProbeProvider records request caps and returns a minimal valid response.
type capProbeProvider struct {
	requests []provider.ChatRequest
}

func (p *capProbeProvider) ID() string { return "cap-probe" }
func (p *capProbeProvider) Models(context.Context) ([]string, error) { return nil, nil }
func (p *capProbeProvider) ChatStream(context.Context, provider.ChatRequest, chan<- provider.Delta) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, provider.ErrUnsupported
}
func (p *capProbeProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	return provider.ChatResponse{Choices: []provider.Choice{{
		Message: provider.Message{Role: "assistant", Content: `{"severity":"high","component":"auth","task_title":"x"}`},
		FinishReason: provider.FinishStop,
	}}}, nil
}

// A classification is deliberately short-lived work. It must not inherit the
// long-document cap merely because both requests came from the same duckling.
func TestStructuredTriageUsesAContractAwareOutputCap(t *testing.T) {
	p := &capProbeProvider{}
	loop := testLoop(p, 0)
	loop.Duckling.Params.MaxTokens = func() *int { n := 20000; return &n }()
	turn := &Turn{Role: config.RoleTriager, Prompt: "classify", Contract: "json:triage", MaxTurns: 1}
	if _, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 1 || p.requests[0].MaxTokens == nil {
		t.Fatalf("recorded requests = %d, cap = %+v", len(p.requests), p.requests)
	}
	if got := *p.requests[0].MaxTokens; got > 2048 {
		t.Fatalf("triage cap = %d, want a classification cap no larger than 2048", got)
	}
}

// repetitionProvider produces a loop on the first streamed call and a valid
// answer only after a retry. The first call must be cut at the stream layer;
// waiting for its finish would reproduce the production failure.
type repetitionProvider struct {
	calls       int
	requests    []provider.ChatRequest
	wasCanceled bool
}

func (p *repetitionProvider) ID() string { return "repetition" }
func (p *repetitionProvider) Models(context.Context) ([]string, error) { return nil, nil }
func (p *repetitionProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, provider.ErrUnsupported
}
func (p *repetitionProvider) ChatStream(ctx context.Context, req provider.ChatRequest, ch chan<- provider.Delta) (provider.ChatResponse, error) {
	p.calls++
	p.requests = append(p.requests, req)
	if p.calls == 1 {
		for i := 0; i < 20; i++ {
			select {
			case ch <- provider.Delta{Text: "same phrase same phrase same phrase "}:
			case <-ctx.Done():
				p.wasCanceled = true
				return provider.ChatResponse{}, ctx.Err()
			}
		}
		// A detector should cancel before the provider naturally finishes.
		<-ctx.Done()
		p.wasCanceled = true
		return provider.ChatResponse{}, ctx.Err()
	}
	return provider.ChatResponse{Choices: []provider.Choice{{
		Message: provider.Message{Role: "assistant", Content: `{"severity":"high","component":"auth","task_title":"fixed"}`},
		FinishReason: provider.FinishStop,
	}}}, nil
}

func TestStreamingRepetitionIsCanceledAndRetriedWithDiagnosis(t *testing.T) {
	p := &repetitionProvider{}
	loop := testLoop(p, 0)
	turn := &Turn{Role: config.RoleTriager, Prompt: "classify", Contract: "json:triage", MaxTurns: 1}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := RunTurn(ctx, loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text == "" || p.calls != 2 {
		t.Fatalf("calls = %d, text = %q; want one canceled loop and one retry", p.calls, out.Text)
	}
	if !p.wasCanceled {
		t.Fatal("repetition stream was not canceled")
	}
	if len(p.requests[1].Messages) == 0 || !strings.Contains(strings.ToLower(p.requests[1].Messages[len(p.requests[1].Messages)-1].Content), "repetition") {
		t.Fatal("retry prompt did not name the repetition loop")
	}
}

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
