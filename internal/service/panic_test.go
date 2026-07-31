package service

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/runlog"
)

// A panic reported only its message — "slice bounds out of range [92:78]" — so
// finding which of a hundred slice expressions produced it meant reading the
// whole engine. A crash is the one place where the stack is worth the noise.
func TestAPanicKeepsItsStack(t *testing.T) {
	rs := &runState{run: &runlog.Run{ID: "r-1", Status: "running"}}

	func() {
		defer recoverRun(rs)
		var lines []string
		_ = lines[1:] // out of range
	}()

	if rs.run.Status != "failed" || rs.run.Verdict != "ABORTED" {
		t.Errorf("run = %q / %q", rs.run.Status, rs.run.Verdict)
	}
	if !strings.Contains(rs.run.Failure, "panic:") {
		t.Errorf("failure = %q", rs.run.Failure)
	}
	// The whole point: a location to look at.
	if !strings.Contains(rs.run.Failure, "panic_test.go") {
		t.Errorf("the stack is missing, so the panic names no location: %q", rs.run.Failure)
	}
}

// A run that did not panic must be left exactly as it was.
func TestRecoverRunLeavesAHealthyRunAlone(t *testing.T) {
	rs := &runState{run: &runlog.Run{ID: "r-2", Status: "running", Verdict: "PASSED"}}
	func() { defer recoverRun(rs) }()
	if rs.run.Status != "running" || rs.run.Verdict != "PASSED" || rs.run.Failure != "" {
		t.Errorf("run was modified: %+v", rs.run)
	}
}

// A run that panicked was written out with zero tokens and zero cost, while its
// per-duckling breakdown — updated on every call — said it had spent plenty.
//
// recordSpend runs before the error branch on an ordinary failure, so a run that
// exceeds its budget records what it burned. A panic skips straight to the
// deferred recover and skipped it. Tokens were burned and money was charged
// whatever happened next, and a report that omits them understates the cost of
// exactly the runs worth being unhappy about.
func TestAPanicStillRecordsWhatItSpent(t *testing.T) {
	tracker := budget.NewTracker(&budget.Budget{
		MaxUSD: 2, MaxTokens: 400000, MaxTurns: 24, MaxWallclockS: 600,
	})
	tracker.Record(420022, 16317, 1.5048)

	rs := &runState{
		run:     &runlog.Run{ID: "r-1", Status: "running", StartedAt: "2026-07-30T10:44:36Z"},
		tracker: tracker,
	}
	func() {
		defer recoverRun(rs)
		var lines []string
		_ = lines[1:]
	}()

	if rs.run.Budget.Tokens != 420022+16317 {
		t.Errorf("tokens = %d, want what the tracker had", rs.run.Budget.Tokens)
	}
	if rs.run.Budget.USD == 0 {
		t.Error("the cost was lost")
	}
	if !strings.Contains(rs.run.Failure, "panic:") {
		t.Errorf("failure = %q", rs.run.Failure)
	}
}

// A panic before the tracker exists must not itself panic.
func TestAPanicWithNoTrackerIsStillRecorded(t *testing.T) {
	rs := &runState{run: &runlog.Run{ID: "r-2", Status: "running"}}
	func() {
		defer recoverRun(rs)
		panic("early")
	}()
	if rs.run.Verdict != "ABORTED" {
		t.Errorf("verdict = %q", rs.run.Verdict)
	}
}
