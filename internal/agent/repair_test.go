package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

// countingProvider records every request and replies with a scripted sequence.
type countingProvider struct {
	mu       sync.Mutex
	requests []provider.ChatRequest
	replies  []string
	fallback string
	err      error
}

func (p *countingProvider) ID() string { return "counting" }

func (p *countingProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return provider.ChatResponse{}, p.err
	}
	n := len(p.requests)
	p.requests = append(p.requests, req)
	text := p.fallback
	if n < len(p.replies) {
		text = p.replies[n]
	}
	return provider.ChatResponse{
		Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: text},
			FinishReason: provider.FinishStop,
		}},
		Usage: provider.Usage{PromptTokens: 10, CompletionTokens: 5},
	}, nil
}

func (p *countingProvider) ChatStream(ctx context.Context, req provider.ChatRequest, ch chan<- provider.Delta) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, provider.ErrUnsupported
}

func (p *countingProvider) Models(ctx context.Context) ([]string, error) { return nil, nil }

func (p *countingProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func testLoop(p provider.Provider, repairs int) *Loop {
	return &Loop{
		Provider: p,
		Duckling: &DucklingConfig{
			ID: "pato-test", Provider: "counting", Model: "fake",
			Caps: provider.Capabilities{NativeTools: false, ContextTokens: 32768},
		},
		Registry:       tools.NewRegistry(),
		Budget:         budget.NewTracker(&budget.Budget{MaxUSD: 10, MaxTokens: 1e6, MaxTurns: 50, MaxWallclockS: 600}),
		MaxTurns:       4,
		RepairAttempts: repairs,
	}
}

// AC-21: a judge that replies with prose triggers exactly 2 repair prompts and
// then fails with ErrContract — never a guessed choice (I6).
func TestJudgeProseTriggersExactlyTwoRepairsThenFails(t *testing.T) {
	p := &countingProvider{fallback: "I think candidate B looks nicer, honestly."}
	loop := testLoop(p, 2)

	turn := &Turn{Role: config.RoleJudge, Prompt: "choose", Contract: "choice", MaxTurns: 2}
	ectx := &tools.ExecContext{ProjectRoot: t.TempDir(), Role: config.RoleJudge}

	out, err := RunTurn(context.Background(), loop, turn, ectx)
	if err == nil {
		t.Fatal("prose was accepted as a choice")
	}
	if !errors.Is(err, ErrContract) {
		t.Fatalf("error = %v, want ErrContract", err)
	}
	if out.Repairs != 2 {
		t.Errorf("Repairs = %d, want exactly 2", out.Repairs)
	}
	// 1 original call + 2 repairs.
	if p.calls() != 3 {
		t.Errorf("provider called %d times, want 3 (1 original + 2 repairs)", p.calls())
	}
	if out.Parsed != nil {
		t.Errorf("a value was produced from unparseable output: %+v", out.Parsed)
	}
}

func TestRepairAttemptsAreConfigurable(t *testing.T) {
	p := &countingProvider{fallback: "still prose"}
	loop := testLoop(p, 4)
	turn := &Turn{Role: config.RoleJudge, Prompt: "choose", Contract: "choice", MaxTurns: 2}

	out, _ := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if out.Repairs != 4 {
		t.Errorf("Repairs = %d, want 4", out.Repairs)
	}
	if p.calls() != 5 {
		t.Errorf("provider called %d times, want 5", p.calls())
	}
}

// A model that corrects itself on the first repair must succeed, and the
// parsed value must reach the caller.
func TestRepairSucceedsAndReturnsTheParsedValue(t *testing.T) {
	p := &countingProvider{replies: []string{
		"Candidate A is the better one.",
		`{"choice":"A","reason":"only green candidate"}`,
	}}
	loop := testLoop(p, 2)
	turn := &Turn{Role: config.RoleJudge, Prompt: "choose", Contract: "choice", MaxTurns: 2}

	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if out.Repairs != 1 {
		t.Errorf("Repairs = %d, want 1", out.Repairs)
	}
	choice, ok := out.Parsed.(*Choice)
	if !ok || choice.Choice != "A" {
		t.Fatalf("Parsed = %+v, want *Choice{A}", out.Parsed)
	}
}

// The repair conversation must include what the model actually said, or it is
// being asked to fix something it cannot see.
func TestRepairPromptIncludesTheOriginalExchange(t *testing.T) {
	const bad = "I prefer candidate B because the code reads better."
	p := &countingProvider{replies: []string{bad, `{"choice":"A","reason":"green"}`}}
	loop := testLoop(p, 2)
	turn := &Turn{Role: config.RoleJudge, Prompt: "choose between A and B", Contract: "choice", MaxTurns: 2}

	if _, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) < 2 {
		t.Fatal("no repair request was made")
	}
	repair := p.requests[1]

	var sawBadAnswer, sawOriginalTask, sawCorrection bool
	for _, m := range repair.Messages {
		if strings.Contains(m.Content, bad) {
			sawBadAnswer = true
		}
		if strings.Contains(m.Content, "choose between A and B") {
			sawOriginalTask = true
		}
		if strings.Contains(m.Content, "did not satisfy the required output format") {
			sawCorrection = true
		}
	}
	if !sawBadAnswer {
		t.Error("the repair prompt does not include the model's bad answer")
	}
	if !sawOriginalTask {
		t.Error("the repair prompt lost the original task")
	}
	if !sawCorrection {
		t.Error("the repair prompt does not state what was wrong")
	}
}

// A transport failure must not be reported as the model failing its contract,
// and must never produce a value.
func TestTransportErrorDuringRepairProducesNoValue(t *testing.T) {
	p := &countingProvider{replies: []string{"prose"}, fallback: "prose"}
	loop := testLoop(p, 2)
	turn := &Turn{Role: config.RoleJudge, Prompt: "choose", Contract: "choice", MaxTurns: 2}

	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err == nil {
		t.Fatal("expected failure")
	}
	if out != nil && out.Parsed != nil {
		t.Errorf("a value was produced despite failure: %+v", out.Parsed)
	}
}

// The verdict contract gets the same treatment as choice.
func TestReviewerProseAlsoFailsAfterRepairs(t *testing.T) {
	p := &countingProvider{fallback: "Looks good to me!"}
	loop := testLoop(p, 2)
	turn := &Turn{Role: config.RoleReviewer, Prompt: "review", Contract: "verdict", MaxTurns: 2}

	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if !errors.Is(err, ErrContract) {
		t.Fatalf("error = %v, want ErrContract", err)
	}
	if out.Repairs != 2 {
		t.Errorf("Repairs = %d, want 2", out.Repairs)
	}
	// The failure must name the contract and the role, or a run log gives no
	// way to tell a bad verdict from a bad choice.
	if out.ContractError == nil ||
		!strings.Contains(out.ContractError.Error(), "verdict") ||
		!strings.Contains(out.ContractError.Error(), "reviewer") {
		t.Errorf("ContractError = %v; should name the contract and role", out.ContractError)
	}
}

// A "</think>" stop sequence used to be added as thinking suppression. On a
// server that separates reasoning from content it ended generation exactly
// when the answer was about to start, so content came back empty with
// hundreds of tokens spent — the failure the suppression existed to prevent.
// Measured against a live llama.cpp endpoint before this was removed.
func TestThinkingSuppressionDoesNotTruncateTheAnswer(t *testing.T) {
	req := provider.ChatRequest{}
	applyThinkingSuppression(&req, provider.Capabilities{})
	for _, s := range req.Stop {
		if strings.Contains(s, "think") {
			t.Errorf("a think marker is used as a stop sequence (%q); it truncates the answer", s)
		}
	}
	if req.Extra["chat_template_kwargs"] == nil || req.Extra["reasoning"] == nil {
		t.Error("suppression no longer asks the provider to skip reasoning")
	}
}

func TestStripThinkingRemovesTheBlockNotTheAnswer(t *testing.T) {
	cases := map[string]string{
		"<think>reasoning here</think>\n{\"verdict\":\"approve\"}": `{"verdict":"approve"}`,
		"no thinking at all":                         "no thinking at all",
		"<think>only reasoning, cut off mid-thought": "",
		"before <think>middle</think> after":         "before  after",
	}
	for in, want := range cases {
		if got := stripThinking(in); got != want {
			t.Errorf("stripThinking(%q) = %q, want %q", in, got, want)
		}
	}
}

// Tokens spent with nothing returned has a specific cause and a specific fix;
// "empty response" names neither.
func TestThoughtOnlyResponseIsDiagnosed(t *testing.T) {
	p := &countingProvider{fallback: ""}
	p.replies = []string{""}
	loop := testLoop(p, 2)
	turn := &Turn{Role: config.RoleReviewer, Prompt: "review", Contract: "verdict", MaxTurns: 2}

	_, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !errors.Is(err, ErrThoughtOnly) {
		t.Fatalf("err = %v; want it identified as a thought-only response", err)
	}
	for _, want := range []string{"hidden reasoning", "max_tokens"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say what to do (%q): %v", want, err)
		}
	}
}
