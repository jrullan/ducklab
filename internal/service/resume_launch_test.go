package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// B-511, as r-20261005-110414-sumi ran: the task had failed here before, so
// the launch reminder fired at 11:04:14; the run paused and was resumed at
// 11:33:05, and the identical reminder fired again, still labelled "launch".
// Here a real pair build pauses on its budget and is resumed through the
// normal lifecycle; the record holds one launch reminder.
func TestB511AResumeDoesNotReplayTheLaunchReminder(t *testing.T) {
	s := serviceWithDucklings(t, "impl", "reviewer")
	native := true
	for id, duck := range s.cfg.Ducklings {
		duck.Caps.NativeTools = &native
		s.cfg.Ducklings[id] = duck
	}
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package fixture\n\nfunc Add(a, b int) int { return a - b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vcs.New(dir).Init(); err != nil {
		t.Fatal(err)
	}
	// The task's history: two earlier builds of T-001 that failed here.
	s.runsMu.Lock()
	for _, id := range []string{"r-prior-a", "r-prior-b"} {
		s.runs[id] = &runState{run: &runlog.Run{ID: id, ProjectID: projectID, TaskID: "T-001", Stage: "build",
			Status: "failed", Verdict: "FAILED", EndedAt: "2026-10-05T10:00:00Z"}}
	}
	s.runsMu.Unlock()
	fake := s.providers["fake"].(*provider.Fake)
	reviewerCalls := 0
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		isReviewer, hasToolResult := false, false
		for _, m := range req.Messages {
			isReviewer = isReviewer || (m.Role == "system" && strings.Contains(m.Content, "You are the reviewer"))
			hasToolResult = hasToolResult || m.Role == "tool" || (m.Role == "user" && strings.HasPrefix(m.Content, "Tool result for "))
		}
		usage := provider.Usage{PromptTokens: 10, CompletionTokens: 10}
		if isReviewer {
			reviewerCalls++
			if reviewerCalls == 1 {
				return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: "Reading.",
					ToolCalls: []provider.ToolCall{fakeToolCall("fs_list", `{"path":"."}`)}}, FinishReason: provider.FinishToolCalls}}, Usage: usage}
			}
			return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: `{"verdict":"approve","findings":[]}`}, FinishReason: provider.FinishStop}}, Usage: usage}
		}
		if !hasToolResult {
			return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant",
				ToolCalls: []provider.ToolCall{fakeToolCall("fs_patch", `{"path":"add.go","edits":[{"search":"return a - b","replace":"return a + b"}]}`)}}, FinishReason: provider.FinishToolCalls}}, Usage: usage}
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: `Fixed. {"deliverables":[{"id":1,"status":"done"}]}`}, FinishReason: provider.FinishStop}}, Usage: usage}
	}
	run, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-001", Mode: "pair", Budget: &budget.Budget{MaxTokens: 50}})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, run.ID)
	wait := func() *runState {
		s.runsMu.RLock()
		rs := s.runs[run.ID]
		s.runsMu.RUnlock()
		select {
		case <-rs.done:
		case <-time.After(20 * time.Second):
			t.Fatal("the run never stopped")
		}
		return rs
	}
	rs := wait()
	if rs.run.Status != "paused" || rs.run.PendingKind != "budget" {
		t.Fatalf("status/pending = %s/%s, want paused/budget", rs.run.Status, rs.run.PendingKind)
	}
	if _, err := s.RunBudgetLift(context.Background(), run.ID, "tokens"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunResume(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	rs = wait()
	events, err := runlog.ReadEvents(rs.runDir)
	if err != nil {
		t.Fatal(err)
	}
	launches, resumed := 0, false
	for _, e := range events {
		if e.Type == "checkpoint" && e.Data["reason"] == "resume" {
			resumed = true
		}
		if e.Type != "escalation_suggestion" {
			continue
		}
		switch e.Data["point"] {
		case "launch":
			launches++
			if resumed {
				t.Errorf("the launch reminder was replayed after the resume: %v", e.Data)
			}
		case "resume":
			t.Errorf("a resume variant was emitted, but a resume adds no history: %v", e.Data)
		}
	}
	if !resumed {
		t.Fatal("the run was never resumed")
	}
	if launches != 1 {
		t.Errorf("launch reminders = %d, want exactly one per run", launches)
	}
}

// The test-first launch path calls the same reminder: called twice on one
// run's record, it is recorded once.
func TestB511LaunchReminderIsOncePerRunRecord(t *testing.T) {
	s := newTestService(t)
	current := &runlog.Run{ID: "r-current", ProjectID: "p", TaskID: "T-008", Stage: "test", Roster: map[string]string{"implementer": "terra"}}
	w, err := runlog.NewWriter(t.TempDir(), current)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rs := &runState{run: current, writer: w}
	s.runs = map[string]*runState{current.ID: rs}
	for _, id := range []string{"r-a", "r-b", "r-c"} {
		s.runs[id] = &runState{run: &runlog.Run{ID: id, ProjectID: "p", TaskID: "T-008", Stage: "test", Verdict: "FAILED"}}
	}
	s.emitLaunchEscalation(rs)
	s.emitLaunchEscalation(rs)
	events, err := runlog.ReadEvents(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	if n := len(eventsOf(events, "escalation_suggestion")); n != 1 {
		t.Errorf("escalation_suggestion events = %d, want 1", n)
	}
}
