package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// A budget running out is a decision point, not a defect. It used to fail the
// run AND restore the tree — two million tokens of work rolled back when one
// click of headroom would have finished the task. Now it pauses with the work
// in place; abort is what restores.
func TestABudgetDeathPausesWithTheWorkInPlace(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}

	// The run's snapshot precedes its work, exactly as executeRun takes it.
	git := vcs.New(dir)
	snap, err := git.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "half_done.py")
	if err := os.WriteFile(work, []byte("# two million tokens of progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{
		ID: "r-bp", ProjectID: p.ID, TaskID: "T-001", Stage: "build",
		Status: "running", StartedAt: "2026-08-06T15:30:00Z", TreeSnapshot: snap,
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
	rs.setTracker(budget.NewTracker(&budget.Budget{MaxTokens: 20}))
	s.runsMu.Lock()
	s.runs[run.ID] = rs
	s.runsMu.Unlock()

	s.failRun(rs, fmt.Errorf("agent: %w: token budget exceeded: 21 >= 20", agent.ErrBudgetExceeded))

	if run.Status != "paused" || run.PendingKind != "budget" {
		t.Fatalf("status/pending = %s/%s, want paused/budget", run.Status, run.PendingKind)
	}
	if _, err := os.Stat(work); err != nil {
		t.Error("the pause restored the tree — the work the pause exists to save is gone")
	}
	if got := runNext(run); len(got) == 0 || got[0] != "resume" {
		t.Errorf("next = %v, want resume first", got)
	}

	// Abort is still the exit that restores.
	if err := s.RunAbort(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	restoreAfterUnaccepted(rs)
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Error("abort did not restore the tree")
	}
}

// Lifting one cap frees exactly that cap, is recorded on the run, and is
// refused once the run has ended — a finished run spends nothing.
func TestLiftingACapIsRecordedAndGuarded(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-lift", ProjectID: p.ID, TaskID: "T-001", Stage: "build",
		Status: "paused", PendingKind: "budget", StartedAt: "2026-08-06T15:30:00Z",
	}
	run.Budget.Limit = runlog.BudgetLimits{Tokens: 100, USD: 5}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
	rs.setTracker(budget.NewTracker(&budget.Budget{MaxTokens: 100, MaxUSD: 5}))
	s.runsMu.Lock()
	s.runs[run.ID] = rs
	s.runsMu.Unlock()

	got, err := s.RunBudgetLift(context.Background(), run.ID, "tokens")
	if err != nil {
		t.Fatal(err)
	}
	if got.Budget.Limit.Tokens != 0 {
		t.Errorf("recorded token cap = %d, want 0 (lifted)", got.Budget.Limit.Tokens)
	}
	if got.Budget.Limit.USD != 5 {
		t.Errorf("the dollar cap moved too: %v", got.Budget.Limit.USD)
	}

	if _, err := s.RunBudgetLift(context.Background(), run.ID, "vibes"); err == nil {
		t.Error("an unknown cap name was accepted")
	}

	run.Status = "done"
	_, err = s.RunBudgetLift(context.Background(), run.ID, "usd")
	if err == nil || !strings.Contains(err.Error(), "not lifted") {
		t.Errorf("a finished run's budget was lifted: %v", err)
	}
}
