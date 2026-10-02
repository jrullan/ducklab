package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

// mandatoryReasoner behaves like qwen/qwen3.8-max on OpenRouter's Alibaba
// endpoint (verified 2026-10-02): reasoning.enabled=false is a 400, and the
// model reasons for ~2000 tokens before it answers, inside the same cap.
type mandatoryReasoner struct {
	mu   sync.Mutex
	caps []int
}

func (m *mandatoryReasoner) ID() string                               { return "openrouter" }
func (m *mandatoryReasoner) Models(context.Context) ([]string, error) { return nil, nil }
func (m *mandatoryReasoner) ChatStream(ctx context.Context, req provider.ChatRequest, _ chan<- provider.Delta) (provider.ChatResponse, error) {
	return m.Chat(ctx, req)
}
func (m *mandatoryReasoner) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if r, ok := req.Extra["reasoning"].(map[string]interface{}); ok && r["enabled"] == false {
		return provider.ChatResponse{}, fmt.Errorf("chat: 400 Bad Request: Reasoning is mandatory for this endpoint and cannot be disabled.")
	}
	limit := 1 << 30
	if req.MaxTokens != nil {
		limit = *req.MaxTokens
	}
	m.mu.Lock()
	m.caps = append(m.caps, limit)
	m.mu.Unlock()
	if limit <= 2000 {
		return provider.ChatResponse{
			Choices: []provider.Choice{{Message: provider.Message{Reasoning: strings.Repeat("thinking ", 900)}, FinishReason: provider.FinishLength}},
			Usage:   provider.Usage{PromptTokens: 9000, CompletionTokens: limit, ReasoningTokens: limit},
		}, nil
	}
	return provider.ChatResponse{
		Choices: []provider.Choice{{Message: provider.Message{Content: "Use node --test \"tests/**/*.test.mjs\". It runs exactly the suites under tests/ on Node 22."}, FinishReason: provider.FinishStop}},
		Usage:   provider.Usage{PromptTokens: 9000, CompletionTokens: 2400, ReasoningTokens: 2300},
	}, nil
}

// B-479: Jose's advisor (qwen38-max, "suppress thinking" ticked) answered
// nothing twice. One-shots lost the probed ThinkingControl, sent the local
// template parameter OpenRouter ignores, and capped the call at 2000 tokens,
// all of which the mandatory reasoning consumed. With the duckling's
// effective caps the advisor gets room and answers.
func TestAMandatoryReasoningAdvisorGetsRoomToAnswer(t *testing.T) {
	isolate(t)
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{
		"openrouter": {Kind: config.ProviderKindOpenAI, BaseURL: "https://openrouter.ai/api/v1"},
	}
	native, maxTok, window := true, 131072, 262144
	cfg.Ducklings = map[config.DucklingID]config.Duckling{
		"qwen38-max": {Provider: "openrouter", Model: "qwen/qwen3.8-max",
			Params: config.SamplingParams{DisableThinking: true, MaxTokens: &maxTok},
			Caps:   config.Caps{NativeTools: &native, ContextTokens: &window}},
	}
	s, err := New(cfg, Options{Bus: bus.New(16)})
	if err != nil {
		t.Fatal(err)
	}
	fake := &mandatoryReasoner{}
	s.ducklings.RegisterProvider(fake)
	// The capability probe learns the endpoint's control, as it does in use.
	if caps, err := s.ducklings.ProbeForce(context.Background(), "qwen38-max"); err != nil || caps.ThinkingControl != "mandatory" {
		t.Fatalf("probe: %+v, %v", caps, err)
	}
	fake.mu.Lock()
	fake.caps = nil // only the advice's own calls count below
	fake.mu.Unlock()
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true, GitName: "Ada", GitEmail: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{ID: "r-adv", ProjectID: p.ID, Stage: "build", Status: "paused", PendingKind: "question", Mode: "pair",
		Roster: map[string]string{"advisor": "qwen38-max"}, StartedAt: "2026-10-02T16:40:00Z"}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
	answer, advisor, err := s.advise(context.Background(), rs, &tools.PendingQuestion{ID: "q", Question: "Which test script works on Node 22?"})
	if err != nil {
		t.Fatalf("advice failed: %v (caps sent %v)", err, fake.caps)
	}
	if advisor != "qwen38-max" || !strings.Contains(answer, "tests/**/*.test.mjs") {
		t.Fatalf("advisor %q answered %q", advisor, answer)
	}
	for _, c := range fake.caps {
		if c < maxTok {
			t.Errorf("a one-shot to a mandatory-reasoning seat was capped at %d, want its configured %d", c, maxTok)
		}
	}
}

// When advice still comes back empty, the record says why: finish reason,
// usage and how much went to reasoning (B-479: "empty answer" was all it
// showed).
func TestAFailedAdviceRecordShowsWhyItWasEmpty(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	fake := &mandatoryReasoner{}
	dir := t.TempDir()
	w, err := runlog.NewWriter(dir, &runlog.Run{ID: "r-x", ProjectID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: &runlog.Run{ID: "r-x"}, writer: w, runDir: w.RunDir(), projectPath: dir}
	d, _ := s.ducklings.Get("pato-uno")
	resp, _ := fake.Chat(context.Background(), provider.ChatRequest{MaxTokens: intPtrTest(2000)})
	s.logFailedAdvisorAnswer(rs, "pato-uno", d, "advisor", "q", "", fmt.Errorf("advisor contract violation after repair: empty answer"), &resp)
	w.Close() // flush llm.jsonl
	raw, err := readLLMLog(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"finish_reason":"length"`, `"reasoning_tokens":2000`, `"reasoning_chars":`} {
		if !strings.Contains(raw, want) {
			t.Errorf("failed-advice record lacks %s:\n%s", want, raw)
		}
	}
}

func intPtrTest(n int) *int { return &n }

func readLLMLog(runDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(runDir, "llm.jsonl"))
	return string(data), err
}

// reasonThenFail reasons through its whole cap on the first call and errors on
// the repair, the path where the original response is the only evidence.
type reasonThenFail struct {
	mu    sync.Mutex
	calls int
}

func (r *reasonThenFail) ID() string                               { return "local" }
func (r *reasonThenFail) Models(context.Context) ([]string, error) { return nil, nil }
func (r *reasonThenFail) ChatStream(ctx context.Context, req provider.ChatRequest, _ chan<- provider.Delta) (provider.ChatResponse, error) {
	return r.Chat(ctx, req)
}
func (r *reasonThenFail) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	r.mu.Unlock()
	if n == 1 {
		return provider.ChatResponse{
			Choices: []provider.Choice{{Message: provider.Message{Reasoning: strings.Repeat("thinking ", 900)}, FinishReason: provider.FinishLength}},
			Usage:   provider.Usage{PromptTokens: 9000, CompletionTokens: 2000, ReasoningTokens: 2000},
		}, nil
	}
	return provider.ChatResponse{}, fmt.Errorf("chat: 504 Gateway Timeout")
}

// Review of #134: when the repair itself errors, the failed-advice record
// still carries the response that triggered it — finish, usage, reasoning —
// through the real executeAdvice path.
func TestAFailedRepairKeepsTheOriginalResponseAsEvidence(t *testing.T) {
	isolate(t)
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{
		"local": {Kind: config.ProviderKindOpenAI, BaseURL: "http://localhost:8080/v1"},
	}
	native := true
	cfg.Ducklings = map[config.DucklingID]config.Duckling{
		"pato-local": {Provider: "local", Model: "local-model",
			Params: config.SamplingParams{DisableThinking: true}, Caps: config.Caps{NativeTools: &native}},
	}
	s, err := New(cfg, Options{Bus: bus.New(16)})
	if err != nil {
		t.Fatal(err)
	}
	s.ducklings.RegisterProvider(&reasonThenFail{})
	dir := t.TempDir()
	run := &runlog.Run{ID: "r-repair", ProjectID: "p", Stage: "build", Status: "paused", PendingKind: "question",
		Roster: map[string]string{"advisor": "pato-local"}, StartedAt: "2026-10-02T20:00:00Z"}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
	if _, _, err := s.advise(context.Background(), rs, &tools.PendingQuestion{ID: "q", Question: "Which script?"}); err == nil {
		t.Fatal("advice succeeded although the repair errored")
	}
	w.Close()
	raw, err := readLLMLog(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"finish_reason":"length"`, `"reasoning_tokens":2000`, `"completion_tokens":2000`, `"reasoning_chars":`, `504 Gateway Timeout`} {
		if !strings.Contains(raw, want) {
			t.Errorf("failed-advice record lacks %s:\n%s", want, raw)
		}
	}
}

// smallWindow is a mandatory reasoner behind an 8K context, rejecting what
// vLLM and llama.cpp reject: a max_tokens that does not fit beside the prompt.
type smallWindow struct {
	mu   sync.Mutex
	caps []int
}

const smallWindowTokens = 8192

func (m *smallWindow) ID() string                               { return "openrouter" }
func (m *smallWindow) Models(context.Context) ([]string, error) { return nil, nil }
func (m *smallWindow) ChatStream(ctx context.Context, req provider.ChatRequest, _ chan<- provider.Delta) (provider.ChatResponse, error) {
	return m.Chat(ctx, req)
}
func (m *smallWindow) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if r, ok := req.Extra["reasoning"].(map[string]interface{}); ok && r["enabled"] == false {
		return provider.ChatResponse{}, fmt.Errorf("chat: 400 Bad Request: Reasoning is mandatory for this endpoint and cannot be disabled.")
	}
	prompt := 0
	for _, msg := range req.Messages {
		prompt += provider.EstimateTokens(msg.Content)
	}
	if req.MaxTokens == nil {
		return provider.ChatResponse{}, fmt.Errorf("chat: 400 Bad Request: max_tokens is required")
	}
	m.mu.Lock()
	m.caps = append(m.caps, *req.MaxTokens)
	m.mu.Unlock()
	if prompt+*req.MaxTokens > smallWindowTokens {
		return provider.ChatResponse{}, fmt.Errorf("chat: 400 Bad Request: max_tokens (%d) + prompt (%d) exceeds the context length %d", *req.MaxTokens, prompt, smallWindowTokens)
	}
	return provider.ChatResponse{
		Choices: []provider.Choice{{Message: provider.Message{Content: "Use node --test \"tests/**/*.test.mjs\"."}, FinishReason: provider.FinishStop}},
		Usage:   provider.Usage{PromptTokens: prompt, CompletionTokens: 900, ReasoningTokens: 800},
	}, nil
}

// Review of #134: the unsuppressed floor (16K) exceeded an 8K seat's window,
// so the endpoint rejected every advisor and digest call. Both one-shot paths,
// end to end, on a seat that declares only its context window (no
// native_tools: the declaration used to be dropped without one).
func TestOneShotsFitASmallContextWindow(t *testing.T) {
	isolate(t)
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{
		"openrouter": {Kind: config.ProviderKindOpenAI, BaseURL: "https://openrouter.ai/api/v1"},
	}
	window := smallWindowTokens
	cfg.Ducklings = map[config.DucklingID]config.Duckling{
		"small": {Provider: "openrouter", Model: "small/reasoner",
			Params: config.SamplingParams{DisableThinking: true},
			Caps:   config.Caps{ContextTokens: &window}},
	}
	s, err := New(cfg, Options{Bus: bus.New(16)})
	if err != nil {
		t.Fatal(err)
	}
	fake := &smallWindow{}
	s.ducklings.RegisterProvider(fake)
	dir := t.TempDir()
	run := &runlog.Run{ID: "r-small", ProjectID: "p", Stage: "build", Status: "paused", PendingKind: "question",
		Roster: map[string]string{"advisor": "small"}, StartedAt: "2026-10-02T21:00:00Z"}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}

	// A question carrying the failing output, as implementers ask them.
	question := "Which test script works on Node 22? The gate printed:\n" + strings.Repeat("not ok 1 - tests/ is not a module\n", 450)
	if _, _, err := s.advise(context.Background(), rs, &tools.PendingQuestion{ID: "q", Question: question}); err != nil {
		t.Errorf("advice on an 8K seat failed: %v (caps sent %v)", err, fake.caps)
	}
	d, _ := s.ducklings.Get("small")
	p, err := s.ducklings.Provider("small")
	if err != nil {
		t.Fatal(err)
	}
	// A reference chunk sized like the ones digestion sends a small seat.
	prompt := "Distill this reference.\n\n" + strings.Repeat("The calculator shows two lines of output. ", 400)
	if _, err := s.refDigestCall(context.Background(), rs, "small", d.Model, d.Provider, d.Cost, p, prompt); err != nil {
		t.Errorf("digest on an 8K seat failed: %v (caps sent %v)", err, fake.caps)
	}
	if len(fake.caps) < 2 {
		t.Fatalf("expected an advice call and a digest call, saw caps %v", fake.caps)
	}
	for _, c := range fake.caps {
		if c >= smallWindowTokens {
			t.Errorf("a one-shot asked for %d output tokens on an %d window", c, smallWindowTokens)
		}
	}
}

// runawayThenTerse answers the first call with a runaway (more than 16
// sentences, ~3K tokens) and the repair tersely, behind the same 8K window
// that rejects a max_tokens which does not fit beside the prompt.
type runawayThenTerse struct {
	mu    sync.Mutex
	calls []struct{ prompt, max int }
}

func (m *runawayThenTerse) ID() string                               { return "openrouter" }
func (m *runawayThenTerse) Models(context.Context) ([]string, error) { return nil, nil }
func (m *runawayThenTerse) ChatStream(ctx context.Context, req provider.ChatRequest, _ chan<- provider.Delta) (provider.ChatResponse, error) {
	return m.Chat(ctx, req)
}
func (m *runawayThenTerse) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if r, ok := req.Extra["reasoning"].(map[string]interface{}); ok && r["enabled"] == false {
		return provider.ChatResponse{}, fmt.Errorf("chat: 400 Bad Request: Reasoning is mandatory for this endpoint and cannot be disabled.")
	}
	prompt := 0
	for _, msg := range req.Messages {
		prompt += provider.EstimateTokens(msg.Content)
	}
	m.mu.Lock()
	m.calls = append(m.calls, struct{ prompt, max int }{prompt, *req.MaxTokens})
	n := len(m.calls)
	m.mu.Unlock()
	if prompt+*req.MaxTokens > smallWindowTokens {
		return provider.ChatResponse{}, fmt.Errorf("chat: 400 Bad Request: max_tokens (%d) + prompt (%d) exceeds the context length %d", *req.MaxTokens, prompt, smallWindowTokens)
	}
	text := "Use node --test \"tests/**/*.test.mjs\". It runs the suites under tests/ on Node 22."
	if n == 1 {
		text = strings.Repeat("The runner treats the tests directory as a module path and every suite is skipped by the glob that the gate passes to it today. ", 100)
	}
	return provider.ChatResponse{
		Choices: []provider.Choice{{Message: provider.Message{Content: text}, FinishReason: provider.FinishStop}},
		Usage:   provider.Usage{PromptTokens: prompt, CompletionTokens: provider.EstimateTokens(text)},
	}, nil
}

// Review of #134: the cap was sized for the advice prompt and reused for the
// repair, whose prompt also carries the rejected answer, so the repair
// overran the window the first cap had been clamped to.
func TestAnAdvisorRepairFitsASmallContextWindow(t *testing.T) {
	isolate(t)
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{
		"openrouter": {Kind: config.ProviderKindOpenAI, BaseURL: "https://openrouter.ai/api/v1"},
	}
	window := smallWindowTokens
	cfg.Ducklings = map[config.DucklingID]config.Duckling{
		"small": {Provider: "openrouter", Model: "small/reasoner",
			Params: config.SamplingParams{DisableThinking: true},
			Caps:   config.Caps{ContextTokens: &window}},
	}
	s, err := New(cfg, Options{Bus: bus.New(16)})
	if err != nil {
		t.Fatal(err)
	}
	fake := &runawayThenTerse{}
	s.ducklings.RegisterProvider(fake)
	dir := t.TempDir()
	run := &runlog.Run{ID: "r-repair-small", ProjectID: "p", Stage: "build", Status: "paused", PendingKind: "question",
		Roster: map[string]string{"advisor": "small"}, StartedAt: "2026-10-02T23:30:00Z"}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
	question := "Which test script works on Node 22? The gate printed:\n" + strings.Repeat("not ok 1 - tests/ is not a module\n", 300)
	answer, _, err := s.advise(context.Background(), rs, &tools.PendingQuestion{ID: "q", Question: question})
	if err != nil {
		t.Fatalf("advice with a repair on an 8K seat failed: %v (calls %+v)", err, fake.calls)
	}
	if !strings.Contains(answer, "tests/**/*.test.mjs") {
		t.Fatalf("the repaired answer was not used: %q", answer)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected the advice and one repair, saw %+v", fake.calls)
	}
	first, repair := fake.calls[0], fake.calls[1]
	if first.max < provider.EstimateTokens(strings.Repeat("x", 13000)) {
		t.Fatalf("fixture: the first cap (%d) must leave room for the runaway answer", first.max)
	}
	// The quoted answer is bounded, so the repair keeps room to answer.
	if repair.max < 2000 {
		t.Errorf("the repair was capped at %d (prompt %d): quoting the whole runaway answer took its room", repair.max, repair.prompt)
	}
}
