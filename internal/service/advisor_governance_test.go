package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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
	return provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Content: p.replies[i]}}}}, nil
}
func (p *advisorTestProvider) ChatStream(context.Context, provider.ChatRequest, chan<- provider.Delta) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, provider.ErrUnsupported
}
func (p *advisorTestProvider) Models(context.Context) ([]string, error) { return nil, nil }

func TestAdvisorRepairsAndStripsDeliberation(t *testing.T) {
	s := serviceWithDucklings(t, "pato-dos")
	p := &advisorTestProvider{replies: []string{
		"We need answer the user's request.\n\n<think>First I should weigh both options.</think>\n\nUse PostgreSQL.",
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
	if strings.Contains(strings.ToLower(answer), "think") || strings.Contains(answer, "We need") {
		t.Fatalf("advisor returned deliberation/preamble: %q", answer)
	}
	if n := len(strings.Fields(answer)); n == 0 {
		t.Fatal("advisor returned an empty answer")
	}
	if sentences := strings.Count(answer, "."); sentences < 2 || sentences > 8 {
		t.Errorf("answer has %d sentences, want 2-8: %q", sentences, answer)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) != 2 {
		t.Fatalf("provider calls = %d, want initial reply plus one repair", len(p.calls))
	}
	if !strings.Contains(strings.ToLower(p.calls[1].Messages[len(p.calls[1].Messages)-1].Content), "violation") {
		t.Error("repair prompt did not name the contract violation")
	}
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
		for _, e := range events {
			if e.Type == "advice_failed" {
				data := e.Data
				if !strings.Contains(data["error"].(string), "advisor offline") {
					t.Fatalf("advice_failed event lacks cause: %+v", e.Data)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("advisor failure was silently dropped; want advice_failed with its cause")
}
