package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestParsedContractPolicyUsesOrdinaryRepairPath(t *testing.T) {
	p := &countingProvider{replies: []string{
		`{"verdict":"request-changes","findings":[{"severity":"major","file":"app.c","line":1,"issue":"x","fix":"forbidden remedy"}]}`,
		`{"verdict":"approve","findings":[]}`,
	}}
	loop := testLoop(p, 2)
	turn := &Turn{Role: config.RoleReviewer, Prompt: "review", Contract: "verdict", MaxTurns: 2}
	validations := 0
	ectx := &tools.ExecContext{ProjectRoot: t.TempDir()}
	ectx.NormalizeContract = func(role config.Role, contract string, parsed interface{}) (bool, error) {
		validations++
		v := parsed.(*Verdict)
		if len(v.Findings) > 0 && v.Findings[0].Fix == "forbidden remedy" {
			return false, errors.New("remedy conflicts with active capability")
		}
		return false, nil
	}

	out, err := RunTurn(context.Background(), loop, turn, ectx)
	if err != nil {
		t.Fatalf("policy repair failed: %v", err)
	}
	if out.Repairs != 1 || validations != 2 || !out.Parsed.(*Verdict).Approved() {
		t.Fatalf("outcome = %+v, validations = %d", out, validations)
	}
	if got := p.requests[1].Messages[len(p.requests[1].Messages)-1].Content; !strings.Contains(got, "remedy conflicts with active capability") {
		t.Fatalf("repair prompt omitted policy failure: %s", got)
	}
}

func TestParsedContractNormalizationRewritesRecordedReplyWithoutRepair(t *testing.T) {
	p := &countingProvider{replies: []string{
		`{"verdict":"request-changes","findings":[{"severity":"major","file":"app.c","line":1,"issue":"x","fix":"inadmissible"}]}`,
	}}
	loop := testLoop(p, 2)
	turn := &Turn{Role: config.RoleReviewer, Prompt: "review", Contract: "verdict", MaxTurns: 2}
	ectx := &tools.ExecContext{ProjectRoot: t.TempDir()}
	ectx.NormalizeContract = func(role config.Role, contract string, parsed interface{}) (bool, error) {
		v := parsed.(*Verdict)
		v.Findings = nil
		v.Verdict = "approve"
		return true, nil
	}

	out, err := RunTurn(context.Background(), loop, turn, ectx)
	if err != nil {
		t.Fatal(err)
	}
	if out.Repairs != 0 || p.calls() != 1 || !out.Parsed.(*Verdict).Approved() {
		t.Fatalf("outcome = %+v, calls = %d", out, p.calls())
	}
	if !strings.Contains(out.Text, `"verdict":"approve"`) || strings.Contains(out.Text, "inadmissible") {
		t.Fatalf("recorded reply was not canonicalized: %s", out.Text)
	}
}

func TestNativeVerdictRepairInstructionIncludesCompleteSchema(t *testing.T) {
	got := repairInstruction("verdict:native", errors.New("native_checks is required"))
	for _, want := range []string{"native_checks", "completion", "resources", "threads", "representation", "cleanup", "authoritative validation result", "remove that finding", "approve with an empty findings array"} {
		if !strings.Contains(got, want) {
			t.Errorf("native repair instruction lacks %q:\n%s", want, got)
		}
	}
}

// Contract repair requests must use the same bounded output budget as the
// original structured turn, rather than the duckling's large declared cap.
func TestJSONTriageRepairUsesTheContractOutputCap(t *testing.T) {
	p := &countingProvider{replies: []string{
		"not json",
		`{"severity":"high","component":"auth","task_title":"x","reason":"classified"}`,
	}}
	loop := testLoop(p, 1)
	loop.Duckling.Params.MaxTokens = func() *int { n := 20000; return &n }()
	turn := &Turn{Role: config.RoleTriager, Prompt: "classify", Contract: "json:triage", MaxTurns: 1}

	if _, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()}); err != nil {
		t.Fatalf("repair failed: %v", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 2 {
		t.Fatalf("recorded %d requests, want original plus repair", len(p.requests))
	}
	if p.requests[1].MaxTokens == nil {
		t.Fatal("repair request has no MaxTokens")
	}
	if got := *p.requests[1].MaxTokens; got > 2048 {
		t.Fatalf("triage repair cap = %d, want no larger than 2048", got)
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

// Tokens spent with nothing returned has TWO causes, and they earn different
// fixes: a handful of reasoning tokens is a stochastic glitch — retried in
// place, because one bad sample among fifty good calls used to kill the whole
// run — and only when it persists does the run fail, blaming the endpoint
// rather than max_tokens, which was never the problem at 72 tokens.
func TestThoughtOnlyResponseIsRetriedThenDiagnosed(t *testing.T) {
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
	for _, want := range []string{"hidden reasoning", "times in a row", "disable thinking"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say what happened (%q): %v", want, err)
		}
	}
	// The glitch was retried, not taken at its first word.
	p.mu.Lock()
	calls := len(p.requests)
	p.mu.Unlock()
	if calls != 3 {
		t.Errorf("the model was called %d times, want 3 — one glitch must not be terminal", calls)
	}
}

// I3: nothing unbounded. A request with no max_tokens lets a looping model
// generate until the context window fills — measured at over ten minutes of
// wall clock on a fast endpoint, then multiplied by the retry policy.
func TestEveryRequestCarriesAnOutputCap(t *testing.T) {
	p := &countingProvider{fallback: "done"}
	loop := testLoop(p, 2)
	// A duckling that declares nothing, which is the common case.
	loop.Duckling.Params.MaxTokens = nil

	turn := &Turn{Role: config.RoleArchitect, Prompt: "draft", Contract: "freeform", MaxTurns: 1}
	if _, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		t.Fatal("no request made")
	}
	got := p.requests[0].MaxTokens
	if got == nil {
		t.Fatal("the request carried no max_tokens; a looping model would run unbounded")
	}
	if *got != DefaultMaxOutputTokens {
		t.Errorf("max_tokens = %d, want the default %d", *got, DefaultMaxOutputTokens)
	}
}

// A duckling's own limit still wins.
func TestDeclaredOutputCapIsRespected(t *testing.T) {
	want := 512
	p := &countingProvider{fallback: "done"}
	loop := testLoop(p, 2)
	loop.Duckling.Params.MaxTokens = &want

	turn := &Turn{Role: config.RoleArchitect, Prompt: "draft", Contract: "freeform", MaxTurns: 1}
	RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := p.requests[0].MaxTokens; got == nil || *got != want {
		t.Errorf("max_tokens = %v, want %d", got, want)
	}
}

// A closing tag with no opening one. The provider suppressed the marker that
// starts the block but not the one that ends it, so the reply arrived as
// reasoning, then </think>, then the answer — and the reasoning was recorded
// verbatim as what the model said.
//
// Measured against a live endpoint: "Tests pass.\n</think>\n\n**Changed:**
// `add.go` …" was written into the run log as the implementer's message.
func TestStripThinkingHandlesADanglingCloseTag(t *testing.T) {
	got := stripThinking("Tests pass.\n</think>\n\n**Changed:** `add.go` — a + b.")
	if want := "**Changed:** `add.go` — a + b."; got != want {
		t.Errorf("stripThinking() = %q, want %q", got, want)
	}
}

// Content that never mentions thinking must survive untouched, and a stray
// mention inside prose must not eat the answer around it.
func TestStripThinkingLeavesOrdinaryContentAlone(t *testing.T) {
	for _, in := range []string{
		"**Changed:** add.go",
		"I was thinking about the </think> tag in the parser docs.",
	} {
		if got := stripThinking(in); got != in {
			t.Errorf("stripThinking(%q) = %q; ordinary content must not change", in, got)
		}
	}
}

// A streamed response does not always carry finish_reason: the chunk that
// would have said "tool_calls" can simply be absent. Dispatch used to be gated
// on that reason alone, so the calls were dropped in silence and the turn
// ended treating the model's narration as its final answer.
//
// Measured as an A/B on one task and one pair of ducklings: without streaming
// the implementer made five tool calls, patched the file and passed; with
// streaming it made two, never patched, and the reviewer was handed nothing.
func TestToolCallsAreDispatchedWithoutAFinishReason(t *testing.T) {
	fake := provider.NewFake("f")
	var call int
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		call++
		if call == 1 {
			// Tool calls present, finish_reason absent — what a stream gives.
			tc := provider.ToolCall{ID: "c1", Type: "function"}
			tc.Function.Name = "fs_read"
			tc.Function.Arguments = `{"path":"add.go"}`
			return &provider.ChatResponse{
				Choices: []provider.Choice{{
					Message:      provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{tc}},
					FinishReason: "",
				}},
				Usage: provider.Usage{PromptTokens: 10, CompletionTokens: 5},
			}
		}
		return &provider.ChatResponse{
			Choices: []provider.Choice{{
				Message:      provider.Message{Role: "assistant", Content: "Read it."},
				FinishReason: provider.FinishStop,
			}},
			Usage: provider.Usage{PromptTokens: 10, CompletionTokens: 5},
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{
		Provider: fake,
		Duckling: &DucklingConfig{ID: "pato", Model: "m", Caps: provider.Capabilities{NativeTools: true}},
		Registry: tools.NewRegistry(),
		Budget:   budget.NewTracker(&budget.Budget{MaxUSD: 10, MaxTokens: 1e6, MaxTurns: 50, MaxWallclockS: 600}),
		MaxTurns: 4,
	}
	turn := &Turn{Role: config.RoleImplementer, Prompt: "read add.go",
		Toolbelt: []string{"fs_read"}, Contract: "freeform", MaxTurns: 4}

	out, err := RunTurn(context.Background(), loop, turn,
		&tools.ExecContext{ProjectRoot: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("tool calls dispatched = %d, want 1: a call with no finish_reason was dropped", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Name != "fs_read" {
		t.Errorf("dispatched %q", out.ToolCalls[0].Name)
	}
}

// Dialect B was told the syntax and never the vocabulary. The preamble says
// "you act only through the tools you are given" and then gave none, so a
// text-protocol duckling had to guess names — and guessed from the only name
// in front of it, the fs_write in the @payload example. A reviewer, whose
// ceiling is read-only, was measured asking for fs_write for exactly that
// reason.
func TestTextProtocolIsToldWhichToolsItHas(t *testing.T) {
	reg := tools.NewRegistry()
	turn := &Turn{Role: config.RoleReviewer, Prompt: "review it",
		Toolbelt: []string{"fs_read", "fs_search"}}

	msgs := BuildMessages(turn, &tools.ExecContext{ProjectRoot: t.TempDir(), Registry: reg}, false)
	var system string
	for _, m := range msgs {
		if m.Role == "system" {
			system += m.Content
		}
	}

	for _, want := range []string{"Your tools", "fs_read", "fs_search", "Arguments:", "does not search file names"} {
		if !strings.Contains(system, want) {
			t.Errorf("the prompt never mentions %q", want)
		}
	}
	// The catalogue has to be the belt, not a superset: naming a tool the role
	// cannot use invites a refused call and a wasted turn.
	if strings.Contains(system, "- `fs_write`") {
		t.Error("a tool outside the belt was listed as available")
	}
}

// Production ExecContexts do not carry a registry. RunTurn must still render
// the loop's tool descriptions and schemas for a text-protocol model.
func TestTextProtocolCatalogueFallsBackToLoopRegistry(t *testing.T) {
	p := &countingProvider{fallback: "done"}
	loop := testLoop(p, 2)
	turn := &Turn{Role: config.RoleReviewer, Prompt: "review", Toolbelt: []string{"fs_search"}, Contract: "freeform", MaxTurns: 1}
	if _, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		t.Fatal("no model request")
	}
	var system string
	for _, msg := range p.requests[0].Messages {
		if msg.Role == "system" {
			system += msg.Content
		}
	}
	if !strings.Contains(system, "does not search file names") || !strings.Contains(system, `"glob"`) {
		t.Fatalf("text catalogue lacks description/schema: %s", system)
	}
}

// A bare Markdown fence is protocol debris, not an implementation report.
// After tool use it earns the existing tools-closed conclusion call instead
// of silently advancing an empty diff to review.
func TestFenceOnlyFinalAnswerIsRetriedBeforeTurnEnds(t *testing.T) {
	p := &countingProvider{replies: []string{
		"```ducklab\n{\"tool\":\"fs_read\",\"args\":{\"path\":\"add.go\"}}\n```",
		"```",
		"I could not complete the edit.",
	}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loop := testLoop(p, 2)
	turn := &Turn{Role: config.RoleImplementer, Prompt: "fix it", Toolbelt: []string{"fs_read"}, Contract: "edits", MaxTurns: 2}
	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: dir})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "I could not complete the edit." {
		t.Fatalf("final text = %q", out.Text)
	}
	if p.calls() != 3 {
		t.Fatalf("model calls = %d, want tool call + fence + forced conclusion", p.calls())
	}
}

func TestSubstantiveAnswerKeepsOneLineFencedContent(t *testing.T) {
	if !substantiveAnswer("```json {\"ok\":true}\n```") {
		t.Fatal("content on the opening-fence line was discarded as protocol debris")
	}
	for _, text := range []string{"```", "```json\n```", "~~~c\n~~~"} {
		if substantiveAnswer(text) {
			t.Errorf("fence-only response %q was accepted", text)
		}
	}
}

// A turn with no tools that is not told so spends its budget trying to call
// one.
func TestATurnWithNoToolsIsToldSo(t *testing.T) {
	msgs := BuildMessages(&Turn{Role: config.RoleReviewer, Prompt: "x"},
		&tools.ExecContext{ProjectRoot: t.TempDir(), Registry: tools.NewRegistry()}, false)
	var system string
	for _, m := range msgs {
		if m.Role == "system" {
			system += m.Content
		}
	}
	if !strings.Contains(system, "no tools") {
		t.Error("a turn with an empty toolbelt is not told it has none")
	}
}

// Native tool calling carries the schemas in the request, so the catalogue
// would be a second, drifting copy.
func TestNativeDialectGetsNoTextCatalogue(t *testing.T) {
	msgs := BuildMessages(&Turn{Role: config.RoleReviewer, Prompt: "x", Toolbelt: []string{"fs_read"}},
		&tools.ExecContext{ProjectRoot: t.TempDir(), Registry: tools.NewRegistry()}, true)
	for _, m := range msgs {
		if strings.Contains(m.Content, "Your tools") {
			t.Error("the native dialect was given a text tool catalogue as well")
		}
	}
}

// A document council's critic must not get the code-review framing: told to
// examine "this code" and "the tests", a real critic spent three of its six
// turns calling git_diff, artifact_read and fs_read in search of a draft that
// exists only in its conversation.
func TestTheCriticPersonaSwapsTheReviewerFraming(t *testing.T) {
	msgs := BuildMessages(&Turn{Role: config.RoleReviewer, Persona: "critic", Prompt: "x"},
		&tools.ExecContext{ProjectRoot: t.TempDir(), Registry: tools.NewRegistry()}, true)
	var system string
	for _, m := range msgs {
		if m.Role == "system" {
			system += m.Content
		}
	}
	if strings.Contains(system, "did not write this code") {
		t.Error("a document critic was framed as a code reviewer")
	}
	if !strings.Contains(system, "has not been written to the tree or the artifact store") {
		t.Error("the critic is not told where the draft lives")
	}
	// The e2e fake provider recognises reviewer turns by these words; a critic
	// that drops them breaks every council e2e.
	if !strings.Contains(system, "You are the reviewer") {
		t.Error("the critic prompt lost the opening the fake provider matches on")
	}
	// And the persona is a narrowing of REVIEWER only.
	plain := BuildMessages(&Turn{Role: config.RoleReviewer, Prompt: "x"},
		&tools.ExecContext{ProjectRoot: t.TempDir(), Registry: tools.NewRegistry()}, true)
	if !strings.Contains(plain[0].Content, "did not write this code") {
		t.Error("a plain reviewer lost the code framing")
	}
}

func TestPlanManifestPersonaAsksForTopologyNotMarkdown(t *testing.T) {
	msgs := BuildMessages(&Turn{Role: config.RoleArchitect, Persona: "plan_manifest", Contract: "json:plan_manifest", Prompt: "plan"},
		&tools.ExecContext{ProjectRoot: t.TempDir(), Registry: tools.NewRegistry()}, true)
	var system string
	for _, msg := range msgs {
		if msg.Role == "system" {
			system += msg.Content
		}
	}
	if !strings.Contains(system, "compact dependency manifest") || !strings.Contains(system, `"milestones"`) {
		t.Fatalf("manifest persona lacks topology contract:\n%s", system)
	}
	if strings.Contains(system, "final reply IS the document") {
		t.Fatal("manifest persona was also told to emit the full Markdown document")
	}
}

func TestCodingSeatsReceiveTheResolvedHarnessCapsule(t *testing.T) {
	for _, role := range []config.Role{config.RoleImplementer, config.RoleReviewer, config.RoleAdvisor} {
		msgs := BuildMessages(&Turn{Role: role, Prompt: "work"}, &tools.ExecContext{
			ProjectRoot: t.TempDir(), HarnessContext: "## Resolved project harness\n- Stack: c-native (meson.build)",
		}, true)
		if !strings.Contains(msgs[0].Content, "Resolved project harness") {
			t.Errorf("%s did not receive harness capsule", role)
		}
		if role == config.RoleReviewer && !strings.Contains(msgs[0].Content, "authoritative compatibility constraints") {
			t.Error("reviewer was not told capability invariants outrank stale API recipes")
		}
	}
	msgs := BuildMessages(&Turn{Role: config.RoleArchitect, Prompt: "draft"}, &tools.ExecContext{
		ProjectRoot: t.TempDir(), HarnessContext: "## Resolved project harness\nsecret stack facts",
	}, true)
	if strings.Contains(msgs[0].Content, "secret stack facts") {
		t.Fatal("a document architect received the coding harness capsule")
	}
}

// A critic surveying a real codebase spent all its calls reading and
// searching, legitimately, and ran out mid-verification — and the whole run
// died with it, architect's draft included. Twice. A model out of looking is
// not a model with nothing to say: it gets one final call, tools withheld, to
// answer from what it has seen.
func TestTurnExhaustionForcesAConclusionInsteadOfFailing(t *testing.T) {
	reg := tools.NewRegistry()
	toolCall := "Let me look.\n```ducklab\n{\"tool\":\"fs_read\",\"args\":{\"path\":\"a.go\"}}\n```"
	p := &countingProvider{
		replies: []string{toolCall, toolCall,
			`{"verdict":"request-changes","findings":[{"severity":"major","file":"requirements.md","issue":"could not verify REQ-009 against the tree","fix":"narrow it"}]}`},
	}
	loop := testLoop(p, 0)
	loop.Registry = reg

	turn := &Turn{Role: config.RoleReviewer, Prompt: "critique the draft", Contract: "verdict",
		MaxTurns: 2, Toolbelt: []string{"fs_read"}}
	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir(), Role: config.RoleReviewer})
	if err != nil {
		t.Fatalf("an exhausted turn still failed: %v", err)
	}
	v, ok := out.Parsed.(*Verdict)
	if !ok || v.Verdict != "request-changes" {
		t.Fatalf("no verdict came out of the forced conclusion: %+v", out.Parsed)
	}
	// The final call must offer no tools and say why.
	last := p.requests[len(p.requests)-1]
	lastMsg := last.Messages[len(last.Messages)-1]
	if !strings.Contains(lastMsg.Content, "out of tool calls") {
		t.Errorf("the conclusion request does not say what is happening:\n%s", lastMsg.Content)
	}
	if len(last.Tools) != 0 {
		t.Error("the conclusion call still offered tools")
	}
}

// And a model with genuinely nothing to say still fails, with the same words.
func TestTurnExhaustionStillFailsWhenTheConclusionIsEmpty(t *testing.T) {
	reg := tools.NewRegistry()
	toolCall := "```ducklab\n{\"tool\":\"fs_read\",\"args\":{\"path\":\"a.go\"}}\n```"
	p := &countingProvider{replies: []string{toolCall, toolCall}, fallback: ""}
	loop := testLoop(p, 0)
	loop.Registry = reg
	turn := &Turn{Role: config.RoleReviewer, Prompt: "critique", Contract: "verdict",
		MaxTurns: 2, Toolbelt: []string{"fs_read"}}
	_, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir(), Role: config.RoleReviewer})
	if err == nil || !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("err = %v, want ErrNoAnswer", err)
	}
}

// A spec regeneration hit pato-sonnet's 8192 output cap, and the truncation
// retry — "stop deliberating, be brief" — cannot shrink a whole document into
// the same budget: it burned a duplicate call and died on the same wall,
// twice. A document turn that hits the cap fails at once, naming the lever.
func TestADocumentThatDoesNotFitFailsAtOnceWithTheLever(t *testing.T) {
	p := &lengthProvider{}
	loop := testLoop(p, 0)
	turn := &Turn{Role: config.RoleArchitect, Prompt: "write the spec",
		Contract: "markdown_sections:SPEC", MaxTurns: 2}
	_, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err == nil {
		t.Fatal("a truncated document turn did not fail")
	}
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	for _, must := range []string{"did not fit", "max_tokens", "pato-test", "8192"} {
		if !strings.Contains(err.Error(), must) {
			t.Errorf("the failure does not carry %q: %v", must, err)
		}
	}
	// One call: the retry that cannot help was not bought.
	if p.calls != 1 {
		t.Errorf("provider called %d times, want 1", p.calls)
	}
}

// A non-document turn keeps the retry — a model deliberating in circles is
// exactly what the nudge exists for — and the second wall names the lever too.
func TestARepeatTruncationNamesTheCap(t *testing.T) {
	p := &lengthProvider{}
	loop := testLoop(p, 0)
	turn := &Turn{Role: config.RoleImplementer, Prompt: "fix it", Contract: "edits", MaxTurns: 2}
	_, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	if err == nil || !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	if !strings.Contains(err.Error(), "max_tokens") {
		t.Errorf("the failure does not name the lever: %v", err)
	}
	if p.calls != 2 {
		t.Errorf("provider called %d times, want 2 (original + one nudge)", p.calls)
	}
}

// lengthProvider always answers truncated.
type lengthProvider struct{ calls int }

func (p *lengthProvider) ID() string { return "length" }
func (p *lengthProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.calls++
	return provider.ChatResponse{
		Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: "## SPEC-001 — Half a doc"},
			FinishReason: provider.FinishLength,
		}},
		Usage: provider.Usage{PromptTokens: 100, CompletionTokens: 8192},
	}, nil
}
func (p *lengthProvider) ChatStream(ctx context.Context, req provider.ChatRequest, ch chan<- provider.Delta) (provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}
func (p *lengthProvider) Models(ctx context.Context) ([]string, error) { return nil, nil }
