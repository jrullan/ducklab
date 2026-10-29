package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
)

// B-547: an operator aborted two building runs. The cancelled worker then
// recorded its own ending — "provider chat: stream read: context canceled",
// a run_end FAILED, a tree restore — before the abort wrote run_end ABORTED
// with an empty resolution. The record blamed the provider (on the other
// run, the gate and the visual check) and never said who aborted, or why.
func TestAnAbortIsRecordedAsTheAbort(t *testing.T) {
	s := newTestService(t)
	dir := t.TempDir()
	run := &runlog.Run{
		ID: "r-abort-record", ProjectID: "p", Stage: "build", Status: "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	workerCtx, stopWorker := context.WithCancel(context.Background())
	done := make(chan struct{})
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir, cancel: stopWorker, done: done}
	// The worker, cancelled mid-reply, fails the way the provider reports it.
	go func() {
		<-workerCtx.Done()
		s.failRun(rs, fmt.Errorf("provider chat: stream read: %w", errors.Join(provider.ErrProviderUnavailable, context.Canceled)))
		close(done)
	}()
	s.runsMu.Lock()
	s.runs[run.ID] = rs
	s.runsMu.Unlock()

	const reason = "run en distress"
	if err := s.RunAbortAs(context.Background(), run.ID, reason, "mcp:opencode"); err != nil {
		t.Fatal(err)
	}
	want := "aborted by mcp:opencode: " + reason
	if run.Verdict != "ABORTED" || run.Resolution != want || run.Failure != want {
		t.Fatalf("verdict=%q resolution=%q failure=%q, want ABORTED and %q", run.Verdict, run.Resolution, run.Failure, want)
	}
	events, err := runlog.ReadEvents(rs.runDir)
	if err != nil {
		t.Fatal(err)
	}
	ends := 0
	for _, e := range events {
		switch e.Type {
		case "run_end":
			ends++
			if e.Data["verdict"] != "ABORTED" {
				t.Errorf("a run_end %v was recorded besides the abort's", e.Data["verdict"])
			}
		case "error", "verdict", "human_needed":
			t.Errorf("the cancelled worker recorded %s %v", e.Type, e.Data)
		}
	}
	if ends != 1 {
		t.Errorf("%d run_end events, want exactly the abort's", ends)
	}
}
