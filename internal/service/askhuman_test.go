package service

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

// AC-23: ask_human must not block. It signals, and the engine checkpoints.
func TestAskHumanSignalsInsteadOfBlocking(t *testing.T) {
	tool := &tools.AskHuman{}
	ectx := &tools.ExecContext{ProjectRoot: t.TempDir(), Autonomy: config.AutonomyGuarded}
	args := json.RawMessage(`{"question":"Should sessions expire after 24h or 7d?"}`)

	before := runtime.NumGoroutine()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := tool.Execute(context.Background(), ectx, args)
		if err != tools.ErrHumanNeeded {
			t.Errorf("err = %v, want ErrHumanNeeded", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ask_human blocked; it must return immediately so no goroutine waits on a human")
	}

	if ectx.Pending == nil {
		t.Fatal("no pending question recorded")
	}
	if ectx.Pending.Question == "" || ectx.Pending.ID == "" {
		t.Errorf("pending question is incomplete: %+v", ectx.Pending)
	}

	// Nothing may be left running.
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+1 {
		t.Errorf("goroutines went from %d to %d; ask_human leaked a waiter", before, after)
	}
}

// A stored answer resolves the question instead of pausing again, which is how
// a resumed run gets past the point that stopped it.
func TestAskHumanReturnsAStoredAnswer(t *testing.T) {
	tool := &tools.AskHuman{}
	question := "Should sessions expire after 24h or 7d?"
	id := tools.QuestionID(question)

	ectx := &tools.ExecContext{
		ProjectRoot: t.TempDir(),
		Autonomy:    config.AutonomyGuarded,
		Answers:     map[string]string{id: "7d"},
	}
	res, err := tool.Execute(context.Background(), ectx, json.RawMessage(`{"question":"`+question+`"}`))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if res.IsError || res.Content != "7d" {
		t.Errorf("result = %+v, want the stored answer", res)
	}
	if ectx.Pending != nil {
		t.Error("a question with an answer still paused the run")
	}
}

// The id must be stable across processes, or a resumed run cannot match an
// answer to the question that produced it.
func TestQuestionIDIsStableAndWhitespaceInsensitive(t *testing.T) {
	a := tools.QuestionID("Which database?")
	b := tools.QuestionID("  Which database?  ")
	if a != b {
		t.Errorf("id changed with surrounding whitespace: %q vs %q", a, b)
	}
	if a == tools.QuestionID("Which cache?") {
		t.Error("different questions produced the same id")
	}
}

// With nobody to ask, the tool says so rather than stalling the run forever.
func TestAskHumanWithNoHumanAvailable(t *testing.T) {
	tool := &tools.AskHuman{}
	for _, autonomy := range []config.Autonomy{config.AutonomyAuto, config.AutonomyYolo} {
		ectx := &tools.ExecContext{ProjectRoot: t.TempDir(), Autonomy: autonomy}
		res, err := tool.Execute(context.Background(), ectx, json.RawMessage(`{"question":"x?"}`))
		if err != nil {
			t.Fatalf("autonomy %s: err = %v", autonomy, err)
		}
		if !res.IsError {
			t.Errorf("autonomy %s: expected an error result", autonomy)
		}
		if ectx.Pending != nil {
			t.Errorf("autonomy %s: paused the run with no human to answer", autonomy)
		}
	}

	ectx := &tools.ExecContext{ProjectRoot: t.TempDir(), Autonomy: config.AutonomyGuarded, NoHuman: true}
	if _, err := tool.Execute(context.Background(), ectx, json.RawMessage(`{"question":"x?"}`)); err != nil {
		t.Fatalf("NoHuman: err = %v", err)
	}
	if ectx.Pending != nil {
		t.Error("NoHuman still paused the run")
	}
}

func TestAskHumanRejectsEmptyQuestion(t *testing.T) {
	tool := &tools.AskHuman{}
	ectx := &tools.ExecContext{ProjectRoot: t.TempDir(), Autonomy: config.AutonomyGuarded}
	res, err := tool.Execute(context.Background(), ectx, json.RawMessage(`{"question":"   "}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !res.IsError {
		t.Error("an empty question was accepted")
	}
}

// AC-23 at the service layer: a paused run stays paused, the engine stays
// responsive, and answering resumes it.
func TestPauseForQuestionCheckpointsAndAnswerResumes(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-ask", "running")
	s.RecoverRuns(context.Background())

	s.runsMu.RLock()
	rs := s.runs["r-ask"]
	s.runsMu.RUnlock()
	rs.run.Status = "running"
	rs.run.PendingKind = ""

	q := &tools.PendingQuestion{
		ID: tools.QuestionID("Which database?"), Question: "Which database?",
	}
	s.pauseForQuestion(rs, q)

	if rs.run.Status != "paused" {
		t.Fatalf("status = %q, want paused", rs.run.Status)
	}
	if rs.run.PendingKind != "question" {
		t.Errorf("pending_kind = %q, want question", rs.run.PendingKind)
	}
	if rs.run.PendingData["question"] != "Which database?" {
		t.Errorf("pending data lost the question: %+v", rs.run.PendingData)
	}

	// The engine must still answer queries while a run waits.
	if _, err := s.RunList(context.Background(), RunFilter{}); err != nil {
		t.Errorf("engine unresponsive while a run is paused: %v", err)
	}

	// Left alone, it stays paused. A waiting run is not a hung run.
	time.Sleep(100 * time.Millisecond)
	if rs.run.Status != "paused" {
		t.Errorf("status drifted to %q while waiting", rs.run.Status)
	}

	// Answering records the answer for the replay.
	rs.recordAnswer(q.ID, q.Question, "postgres")
	if got := rs.answers()[q.ID]; got != "postgres" {
		t.Errorf("stored answer = %q, want postgres", got)
	}
}

func TestRunAnswerRejectsARunNotWaiting(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-done", "done")
	s.RecoverRuns(context.Background())

	err := s.RunAnswer(context.Background(), "r-done", "", "yes")
	if err == nil {
		t.Fatal("answered a run that is not waiting for one")
	}
}

func TestRunAnswerUnknownRun(t *testing.T) {
	s := newTestService(t)
	if err := s.RunAnswer(context.Background(), "r-nope", "q", "a"); err == nil {
		t.Error("answered a run that does not exist")
	}
}

// A run that has been accepted, rejected or aborted is no longer waiting for
// anyone, and must not keep advertising that it is.
func TestTerminalRunsClearThePendingBlock(t *testing.T) {
	for _, name := range []string{"accept", "reject", "abort"} {
		run := &runlog.Run{
			ID: "r-x", Status: "paused", PendingKind: "gate",
			PendingSince: "2026-07-26T12:00:00Z",
			PendingData:  map[string]interface{}{"verdict": "PASSED"},
		}
		clearPending(run)
		if run.PendingKind != "" || run.PendingSince != "" || run.PendingData != nil {
			t.Errorf("%s: pending block survived: %+v", name, run)
		}
	}
}
