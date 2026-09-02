package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

// advisorTestProvider makes the advisor contract test independent of a network
// endpoint while still checking the actual one-shot advisor path.
type advisorTestProvider struct {
	mu      sync.Mutex
	replies []string
	err     error
	calls   []provider.ChatRequest
	usage   provider.Usage
}

func (p *advisorTestProvider) ID() string { return "fake" }
func (p *advisorTestProvider) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, req)
	if p.err != nil {
		return provider.ChatResponse{}, p.err
	}
	i := len(p.calls) - 1
	if i >= len(p.replies) {
		i = len(p.replies) - 1
	}
	return provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Content: p.replies[i]}}}, Usage: p.usage}, nil
}
func (p *advisorTestProvider) ChatStream(context.Context, provider.ChatRequest, chan<- provider.Delta) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, provider.ErrUnsupported
}
func (p *advisorTestProvider) Models(context.Context) ([]string, error) { return nil, nil }

func TestAdvisorStripsDeliberationAndKeepsTerseAdvice(t *testing.T) {
	s := serviceWithDucklings(t, "pato-dos")
	p := &advisorTestProvider{replies: []string{
		"<think>First I should weigh both options.</think>\n\nUse PostgreSQL.",
		"Use PostgreSQL. It is the project's established entrypoint contract.",
	}}
	s.ducklings.RegisterProvider(p)

	dir := t.TempDir()
	run := &runlog.Run{ID: "r-govern", ProjectID: "p", Status: "paused", PendingKind: "question", Roster: map[string]string{"architect": "pato-dos"}}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}

	answer, _, err := s.advise(context.Background(), rs, &tools.PendingQuestion{ID: "q", Question: "Which entrypoint contract should the test use?"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(answer), "think") {
		t.Fatalf("advisor returned deliberation: %q", answer)
	}
	if n := len(strings.Fields(answer)); n == 0 {
		t.Fatal("advisor returned an empty answer")
	}
	if sentences := advisorSentenceCount(answer); sentences != 1 {
		t.Errorf("answer has %d sentence boundaries, want 1: %q", sentences, answer)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) != 1 {
		t.Fatalf("provider calls = %d, want terse initial reply without repair", len(p.calls))
	}
}

// A question pauses before its advisor call completes, but that call still
// belongs to the paused run rather than disappearing from its per-duckling spend.
func TestAdvisorSentenceCountUsesProseBoundaries(t *testing.T) {
	tests := []struct {
		name, text string
		want       int
	}{
		{"dotted identifiers", "Use fs_read on lifecycle_test.go. Run go test ./internal/service. Inspect service.go:1182 and rs.done. This is the final recommendation.", 4},
		{"inline code and abbreviations", "Read `service.go:1182`. Keep e.g. existing behavior. Choose the documented option.", 3},
		{"punctuation boundaries", "Choose A! It matches the spec? Confirm with the test.", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := advisorSentenceCount(tt.text); got != tt.want {
				t.Errorf("advisorSentenceCount(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}

func TestAdvisorAllowsRecommendationVocabularyAndTruncatesOverlength(t *testing.T) {
	text := "I recommend option A. Keep the queue tests we need. One. Two. Three. Four. Five. Six. Nine."
	if violation := advisorViolation(text); violation != "" {
		t.Fatalf("advisor rejected useful recommendation: %s", violation)
	}
	if got := advisorSentenceCount(truncateAdvisorAnswer(text)); got != 8 {
		t.Fatalf("truncated sentence count = %d, want 8", got)
	}
}

func TestFailedOneShotPreservesCallerRole(t *testing.T) {
	s := serviceWithDucklings(t, "pato-dos")
	d, err := s.ducklings.Get("pato-dos")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	run := &runlog.Run{ID: "r-librarian-failure", ProjectID: "p"}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir()}
	s.logFailedOneShot(rs, "pato-dos", d, "librarian", "digest this", errors.New("offline"))
	data, err := os.ReadFile(filepath.Join(w.RunDir(), "llm.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"role":"librarian"`) {
		t.Fatalf("failed one-shot role was not preserved: %s", data)
	}
}

func TestAdvisorRejectedAnswerIsRecorded(t *testing.T) {
	s := serviceWithDucklings(t, "pato-dos")
	p := &advisorTestProvider{replies: []string{
		"One. Two. Three. Four. Five. Six. Seven. Eight. Nine. Ten. Eleven. Twelve. Thirteen. Fourteen. Fifteen. Sixteen. Seventeen.",
		"One. Two. Three. Four. Five. Six. Seven. Eight. Nine. Ten. Eleven. Twelve. Thirteen. Fourteen. Fifteen. Sixteen. Seventeen.",
	}}
	s.ducklings.RegisterProvider(p)
	dir := t.TempDir()
	run := &runlog.Run{ID: "r-advisor-rejected", ProjectID: "p", Status: "paused", Roster: map[string]string{"advisor": "pato-dos"}}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
	if _, _, err := s.advise(context.Background(), rs, &tools.PendingQuestion{ID: "q", Question: "Which option?"}); err == nil {
		t.Fatal("runaway advisor response was accepted")
	}
	data, err := os.ReadFile(filepath.Join(w.RunDir(), "llm.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Seventeen.") || !strings.Contains(string(data), "advisor contract violation") {
		t.Fatalf("rejected answer was not auditable in llm log: %s", data)
	}
}

func TestPausedQuestionAdvisorSpendIsAttributedToTheRun(t *testing.T) {
	s := serviceWithDucklings(t, "fast-advisor")
	s.ducklings.RegisterProvider(&advisorTestProvider{
		replies: []string{"Use the documented option. It is the project contract."},
		usage:   provider.Usage{PromptTokens: 400, CompletionTokens: 60, TotalTokens: 460},
	})
	dir := t.TempDir()
	run := &runlog.Run{ID: "r-paused-advisor-spend", ProjectID: "p", Status: "paused", PendingKind: "question", Roster: map[string]string{"advisor": "fast-advisor"}}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
	rs.setTracker(budget.NewTracker(&budget.Budget{MaxTokens: 10_000}))

	if _, _, err := s.advise(context.Background(), rs, &tools.PendingQuestion{ID: "q", Question: "Which option?"}); err != nil {
		t.Fatal(err)
	}
	spent := rs.snapshotRun().Spend["fast-advisor"]
	if spent.Calls != 1 || spent.Tokens != 460 {
		t.Fatalf("paused question advisor spend = %+v, want one 460-token call", spent)
	}
}

func TestAdvisorUsesDedicatedConfigurableRosterSeat(t *testing.T) {
	s := serviceWithDucklings(t, "slow-architect", "fast-advisor")
	p := &advisorTestProvider{replies: []string{"Use the documented option. It is the project contract."}}
	s.ducklings.RegisterProvider(p)

	dir := t.TempDir()
	run := &runlog.Run{ID: "r-advisor-seat", ProjectID: "p", Status: "paused", PendingKind: "question", Roster: map[string]string{
		"architect": "slow-architect",
		"advisor":   "fast-advisor",
	}}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}

	_, advisor, err := s.advise(context.Background(), rs, &tools.PendingQuestion{ID: "q", Question: "Which option?"})
	if err != nil {
		t.Fatal(err)
	}
	if advisor != "fast-advisor" {
		t.Fatalf("advisor seat = %q, want configured advisor fast-advisor (not architect)", advisor)
	}
}

func TestAdvisorPromptIncludesProjectDocumentContext(t *testing.T) {
	s := serviceWithDucklings(t, "fast-advisor")
	p := &advisorTestProvider{replies: []string{"Use the documented option. It is the project contract."}}
	s.ducklings.RegisterProvider(p)

	dir := t.TempDir()
	docs := filepath.Join(dir, ".ducklab", "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "spec.md"), []byte("## SPEC-ADVISOR\nThe canonical decision is FAST_PATH.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "requirements.md"), []byte("## REQ-ADVISOR\nThe advisor must cite project decisions.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{ID: "r-advisor-context", ProjectID: "p", Status: "paused", PendingKind: "question", Roster: map[string]string{"advisor": "fast-advisor"}}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir,
		execCtx: &tools.ExecContext{HarnessContext: "Stack invariant (gtk4-clipboard/completion): GdkClipboard has no ready signal; use store_async/store_finish for persistence."}}
	if _, _, err := s.advise(context.Background(), rs, &tools.PendingQuestion{ID: "q", Question: "Which project decision applies?"}); err != nil {
		t.Fatal(err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) == 0 {
		t.Fatal("advisor made no provider call")
	}
	prompt := p.calls[0].Messages[len(p.calls[0].Messages)-1].Content
	for _, want := range []string{"SPEC-ADVISOR", "FAST_PATH", "REQ-ADVISOR", "Active harness/stack invariants", "GdkClipboard has no ready signal", "authoritative"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("advisor prompt lacks project document context %q: %s", want, prompt)
		}
	}
}

func TestAdvisorDraftStartPrecedesRecommendation(t *testing.T) {
	s := serviceWithDucklings(t, "pato-dos")
	s.ducklings.RegisterProvider(&advisorTestProvider{replies: []string{"Use the documented option. It is the project contract."}})
	dir := t.TempDir()
	run := &runlog.Run{ID: "r-advice-start", ProjectID: "p", Status: "paused", PendingKind: "question", PendingData: map[string]interface{}{}}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
	s.adviseQuestion(rs, &tools.PendingQuestion{ID: "q", Question: "Which option?"})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		events, _ := runlog.ReadEvents(w.RunDir())
		advice := -1
		started := -1
		for i, e := range events {
			if e.Type == "advice_started" && e.Data["question_id"] == "q" && e.Data["advisor"] == "pato-dos" {
				started = i
			}
			if e.Type == "advice" && e.Data["question_id"] == "q" {
				advice = i
			}
		}
		if advice >= 0 {
			if started < 0 {
				t.Fatalf("advice was recorded without advice_started: %+v", events)
			}
			if started > advice {
				t.Fatalf("advice_started must precede advice: %+v", events)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("advisor recommendation was not recorded")
}

func TestAdvisorFailureIsRecordedOnQuestion(t *testing.T) {
	s := serviceWithDucklings(t, "pato-dos")
	s.ducklings.RegisterProvider(&advisorTestProvider{err: errors.New("advisor offline")})
	dir := t.TempDir()
	run := &runlog.Run{ID: "r-failed-advice", ProjectID: "p", Status: "paused", PendingKind: "question", PendingData: map[string]interface{}{}}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
	s.adviseQuestion(rs, &tools.PendingQuestion{ID: "q", Question: "Which option?"})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		events, _ := runlog.ReadEvents(w.RunDir())
		failed := -1
		started := -1
		for i, e := range events {
			if e.Type == "advice_started" && e.Data["question_id"] == "q" && e.Data["advisor"] == "pato-dos" {
				started = i
			}
			if e.Type == "advice_failed" {
				failed = i
				data := e.Data
				if !strings.Contains(data["error"].(string), "advisor offline") {
					t.Fatalf("advice_failed event lacks cause: %+v", e.Data)
				}
			}
		}
		if failed >= 0 {
			if started < 0 {
				t.Fatalf("advice_failed was recorded without advice_started: %+v", events)
			}
			if started > failed {
				t.Fatalf("advice_started must precede advice_failed: %+v", events)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("advisor failure was silently dropped; want advice_failed with its cause")
}
