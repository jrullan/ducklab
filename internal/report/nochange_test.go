package report

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

func buildRun(mode, verdict string, noChanges bool) *runlog.Run {
	return &runlog.Run{
		Mode: mode, Stage: "build", Verdict: verdict, NoChanges: noChanges,
		StartedAt: "2026-07-29T12:00:00Z",
	}
}

// Reported from a real session, and it invalidated a finding I had already
// given as measurement.
//
// pair ran twice and both passed, so the report said "+25.0 pts over solo".
// One of those two runs changed no file: the implementer found the work
// already in the tree, the reviewer disagreed with a critical finding, the
// gate was green because the code was already there, and the run was recorded
// PASSED with no commit of its own.
//
// A run that wrote nothing is not evidence that a mode can build.
func TestARunThatChangedNothingIsNotEvidence(t *testing.T) {
	rep := Build([]*runlog.Run{
		buildRun("pair", "PASSED", true),  // the no-op
		buildRun("pair", "PASSED", false), // the real one
		buildRun("solo", "PASSED", false),
		buildRun("solo", "PASSED", false),
		buildRun("solo", "PASSED", false),
		buildRun("solo", "FAILED", false),
	}, Options{By: "mode"})

	pair := findRow(rep.Rows, "pair")
	if pair == nil {
		t.Fatal("pair is missing")
	}
	// One real attempt, and it passed.
	if pair.Effective() != 1 {
		t.Errorf("effective runs = %d, want 1", pair.Effective())
	}
	if pair.NoChanges != 1 {
		t.Errorf("no-change runs = %d, want 1", pair.NoChanges)
	}
	if got := pair.PassRate(); got != 100 {
		t.Errorf("pass rate = %.1f, want 100 over the one real attempt", got)
	}
	// The row still shows both runs: the no-op happened and cost tokens.
	if pair.Runs != 2 {
		t.Errorf("runs = %d, want both still counted", pair.Runs)
	}

	// And the reader is told, because a rate over one attempt printed beside a
	// count of two is a rate they cannot check.
	out := Render(rep)
	if !strings.Contains(out, "changed no file") || !strings.Contains(out, "left out of the rate") {
		t.Errorf("the report hides the subtraction:\n%s", out)
	}
}

// A mode whose only runs did nothing has no rate at all, rather than a
// flattering 100%.
func TestAModeOfNothingButNoOpsHasNoRate(t *testing.T) {
	rep := Build([]*runlog.Run{
		buildRun("solo", "PASSED", false),
		buildRun("tournament", "PASSED", true),
	}, Options{By: "mode"})

	tour := findRow(rep.Rows, "tournament")
	if tour == nil {
		t.Fatal("tournament is missing")
	}
	if tour.Effective() != 0 {
		t.Errorf("effective = %d, want 0", tour.Effective())
	}
	if got := tour.PassRate(); got != 0 {
		t.Errorf("pass rate = %.1f, want 0 — nothing was attempted", got)
	}
}

// The ordinary case is untouched: no no-ops, no subtraction, no note.
func TestOrdinaryRunsAreUnaffected(t *testing.T) {
	rep := Build([]*runlog.Run{
		buildRun("solo", "PASSED", false),
		buildRun("solo", "FAILED", false),
	}, Options{By: "mode"})

	row := findRow(rep.Rows, "solo")
	if row.Effective() != 2 || row.PassRate() != 50 {
		t.Errorf("row = %+v, rate = %.1f", row, row.PassRate())
	}
	if strings.Contains(Render(rep), "changed no file") {
		t.Error("a note was printed about runs that all changed something")
	}
}
