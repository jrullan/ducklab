package service

import (
	"testing"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/runlog"
)

// recordSpend copies the tracker's totals onto the run only when the run ENDS,
// so a fetch made mid-run served an aggregate of zeros — and a run view opened
// while a slow local model worked showed a dead meter for the whole call, with
// nothing wrong anywhere but the read.
func TestAFetchMidRunServesTheLiveSpend(t *testing.T) {
	tracker := budget.NewTracker(&budget.Budget{MaxTokens: 1500000})
	tracker.Spend.AddUSD(0.0130352)
	tracker.Spend.AddTokens(59202)
	tracker.Spend.AddTurn()
	rs := &runState{run: &runlog.Run{ID: "r-1", Status: "running"}}
	rs.setTracker(tracker)

	got := rs.snapshotRun()
	if got.Budget.Tokens != 59202 || got.Budget.Turns != 1 {
		t.Errorf("mid-run fetch served tokens=%d turns=%d, want the tracker's 59202/1",
			got.Budget.Tokens, got.Budget.Turns)
	}

	// Paused counts too: a run waiting at a question has spent real money.
	rs.run.Status = "paused"
	if rs.snapshotRun().Budget.Tokens != 59202 {
		t.Error("a paused run's fetch lost its live spend")
	}

	// A finished run keeps its recorded numbers; the tracker no longer speaks.
	rs.run.Status = "done"
	rs.run.Budget.Tokens = 60000
	if got := rs.snapshotRun().Budget.Tokens; got != 60000 {
		t.Errorf("a done run's record was overwritten to %d", got)
	}
}

// The spend map is shared with the adapter that writes it on every call; the
// served copy must be a copy, or a fetch marshalling it races the call landing.
func TestTheServedSpendMapIsACopy(t *testing.T) {
	rs := &runState{run: &runlog.Run{
		ID: "r-1", Status: "running",
		Spend: map[string]runlog.DucklingSpend{"luna": {Calls: 1, Tokens: 100}},
	}}
	got := rs.snapshotRun()
	rs.run.Spend["luna"] = runlog.DucklingSpend{Calls: 2, Tokens: 999}
	if got.Spend["luna"].Tokens != 100 {
		t.Error("the served spend map aliases the live one")
	}
}
