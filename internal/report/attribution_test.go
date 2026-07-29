package report

import (
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

// Reported from a real session: every duckling showed the same average cost.
//
// The cause was not rounding. Grouping read the roster, which names a duckling
// for every role whether or not that role ran, and then added the run's whole
// cost to each of them. A solo run lists six roles and calls one model, so
// five models were credited with work they never did and the totals came out
// multiplied — with every row identical, which is how it was noticed.
func TestPerDucklingCountsOnlyWhatEachDucklingSpent(t *testing.T) {
	rep := Build([]*runlog.Run{{
		Mode: "solo", Verdict: "PASSED", WallclockMs: 10_000,
		// The roster names three, exactly as the real run did.
		Roster: map[string]string{
			"architect": "pato-sonnet", "implementer": "pato-atom", "judge": "pato-local",
		},
		Budget: runlog.BudgetState{Tokens: 28_309, USD: 0.42},
		// Only one of them was ever called.
		Spend: map[string]runlog.DucklingSpend{
			"pato-atom": {Calls: 5, Tokens: 28_309, CostUSD: 0.42},
		},
	}}, Options{By: "duckling"})

	if len(rep.Rows) != 1 {
		t.Fatalf("got %d rows, want only the duckling that ran: %+v", len(rep.Rows), rep.Rows)
	}
	got := rep.Rows[0]
	if got.Key != "pato-atom" {
		t.Errorf("row = %q, want the duckling that made the calls", got.Key)
	}
	if got.Tokens != 28_309 || got.CostUSD != 0.42 {
		t.Errorf("row = %+v, want the run's real numbers exactly once", got)
	}
}

// Two models in one run split the cost between them. The rows must still sum
// to the run's total rather than a multiple of it.
func TestAMultiDucklingRunSplitsRatherThanMultiplies(t *testing.T) {
	rep := Build([]*runlog.Run{{
		Mode: "pair", Verdict: "PASSED",
		Roster: map[string]string{"implementer": "a", "reviewer": "b", "judge": "c"},
		Budget: runlog.BudgetState{Tokens: 30_000, USD: 3},
		Spend: map[string]runlog.DucklingSpend{
			"a": {Calls: 3, Tokens: 20_000, CostUSD: 2},
			"b": {Calls: 1, Tokens: 10_000, CostUSD: 1},
		},
	}}, Options{By: "duckling"})

	if len(rep.Rows) != 2 {
		t.Fatalf("got %d rows, want the two that ran: %+v", len(rep.Rows), rep.Rows)
	}
	var tokens int64
	var cost float64
	for _, r := range rep.Rows {
		tokens += r.Tokens
		cost += r.CostUSD
	}
	if tokens != 30_000 || cost != 3 {
		t.Errorf("rows sum to %d tokens and $%.2f, want the run's own totals", tokens, cost)
	}
	// And "c", named as judge but never called, is not there at all.
	for _, r := range rep.Rows {
		if r.Key == "c" {
			t.Error("a duckling that made no calls was given a row")
		}
	}
}

// A run with no spend recorded, and no log left to rebuild it from,
// contributes no per-duckling rows. Falling back to the roster is precisely
// what caused the original fault.
func TestARunWithNoSpendContributesNoDucklingRows(t *testing.T) {
	rep := Build([]*runlog.Run{{
		Mode: "solo", Verdict: "PASSED",
		Roster: map[string]string{"implementer": "a", "reviewer": "b"},
		Budget: runlog.BudgetState{Tokens: 5000, USD: 1},
	}}, Options{By: "duckling"})
	if len(rep.Rows) != 0 {
		t.Errorf("rows = %+v, want none", rep.Rows)
	}
}

// An estimated call taints only the duckling that made it, not every model in
// the run.
func TestEstimatedIsPerDuckling(t *testing.T) {
	rep := Build([]*runlog.Run{{
		Mode: "pair", Verdict: "PASSED",
		Budget: runlog.BudgetState{Tokens: 3000},
		Spend: map[string]runlog.DucklingSpend{
			"measured":  {Calls: 1, Tokens: 1000},
			"estimated": {Calls: 1, Tokens: 2000, Estimated: true},
		},
	}}, Options{By: "duckling"})

	for _, r := range rep.Rows {
		if r.Key == "estimated" && !r.Estimated {
			t.Error("an estimated count was reported as measured")
		}
		if r.Key == "measured" && r.Estimated {
			t.Error("a measured count was marked estimated because another model's was")
		}
	}
}

// Grouping by mode is unaffected: there the run's total is the right number,
// because a run has exactly one mode.
func TestByModeStillUsesTheRunsTotal(t *testing.T) {
	rep := Build([]*runlog.Run{{
		Mode: "solo", Verdict: "PASSED",
		Budget: runlog.BudgetState{Tokens: 28_309, USD: 0.42},
		Spend:  map[string]runlog.DucklingSpend{"pato-atom": {Calls: 5, Tokens: 28_309, CostUSD: 0.42}},
	}}, Options{By: "mode"})
	if len(rep.Rows) != 1 || rep.Rows[0].Tokens != 28_309 {
		t.Errorf("rows = %+v", rep.Rows)
	}
}
