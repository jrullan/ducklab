package report

import (
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

func run(mode, verdict string, roster map[string]string) *runlog.Run {
	return &runlog.Run{
		Mode: mode, Verdict: verdict, Roster: roster,
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		WallclockMs: 60000,
		Budget:      runlog.BudgetState{Tokens: 10000, USD: 0.01},
	}
}

// AC-24: the report states the solo baseline and every mode's delta against it.
func TestReportComputesDeltaAgainstSoloBaseline(t *testing.T) {
	var runs []*runlog.Run
	// solo: 1 of 2 passes → 50%
	runs = append(runs, run("solo", "PASSED", nil), run("solo", "FAILED", nil))
	// pair: 3 of 4 pass → 75%
	runs = append(runs, run("pair", "PASSED", nil), run("pair", "PASSED", nil),
		run("pair", "PASSED", nil), run("pair", "FAILED", nil))

	rep := Build(runs, Options{By: "mode"})
	if len(rep.Deltas) != 1 {
		t.Fatalf("got %d deltas, want 1", len(rep.Deltas))
	}
	d := rep.Deltas[0]
	if d.Key != "pair" {
		t.Fatalf("delta key = %q", d.Key)
	}
	if d.PassRate != 75 {
		t.Errorf("pair pass rate = %.1f, want 75", d.PassRate)
	}
	if d.Points != 25 {
		t.Errorf("delta = %+.1f points, want +25", d.Points)
	}

	out := Render(rep)
	if !strings.Contains(out, "solo baseline: 50.0% passed") {
		t.Errorf("baseline line missing:\n%s", out)
	}
	if !strings.Contains(out, "+25.0 pts") {
		t.Errorf("delta line missing:\n%s", out)
	}
}

// P3: UNVERIFIED is not a pass. Counting it would inflate the one number the
// project exists to report honestly.
func TestUnverifiedIsNotCountedAsPassed(t *testing.T) {
	rep := Build([]*runlog.Run{
		run("solo", "UNVERIFIED", nil), run("solo", "UNVERIFIED", nil),
	}, Options{By: "mode"})
	r := rep.Rows[0]
	if r.Passed != 0 {
		t.Errorf("Passed = %d, want 0", r.Passed)
	}
	if r.Unverified != 2 {
		t.Errorf("Unverified = %d, want 2", r.Unverified)
	}
	if r.PassRate() != 0 {
		t.Errorf("pass rate = %.1f, want 0", r.PassRate())
	}
}

// Without solo runs there is nothing to compare against, and the report must
// say so rather than invent a baseline from the best mode.
func TestNoBaselineSaysSoInsteadOfInventingOne(t *testing.T) {
	rep := Build([]*runlog.Run{run("pair", "PASSED", nil)}, Options{By: "mode"})
	if len(rep.Deltas) != 0 {
		t.Errorf("deltas computed with no baseline: %+v", rep.Deltas)
	}
	out := Render(rep)
	if !strings.Contains(out, "no solo runs yet") {
		t.Errorf("missing explanation:\n%s", out)
	}
}

// A run still in flight has no verdict and must not count as a failure.
func TestRunsWithoutAVerdictAreExcluded(t *testing.T) {
	rep := Build([]*runlog.Run{
		run("solo", "PASSED", nil),
		{Mode: "solo", Verdict: "", StartedAt: time.Now().UTC().Format(time.RFC3339)},
	}, Options{By: "mode"})
	if rep.Rows[0].Runs != 1 {
		t.Errorf("Runs = %d, want 1 — an unfinished run was counted", rep.Rows[0].Runs)
	}
}

// Grouping by duckling counts the models that actually made calls.
//
// This used to be written with rosters, which is what made it pass while the
// numbers were wrong: a roster names a duckling per role whether or not that
// role ran. Written with spend, it says what it means.
func TestGroupByDuckling(t *testing.T) {
	spent := func(r *runlog.Run, ids ...string) *runlog.Run {
		r.Spend = map[string]runlog.DucklingSpend{}
		for _, id := range ids {
			r.Spend[id] = runlog.DucklingSpend{Calls: 1, Tokens: 5000, CostUSD: 0.005}
		}
		return r
	}
	rep := Build([]*runlog.Run{
		spent(run("pair", "PASSED", nil), "pato-local", "pato-nube"),
		spent(run("solo", "FAILED", nil), "pato-local"),
	}, Options{By: "duckling"})

	if len(rep.Rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rep.Rows), rep.Rows)
	}
	byKey := map[string]Row{}
	for _, r := range rep.Rows {
		byKey[r.Key] = r
	}
	if byKey["pato-local"].Runs != 2 {
		t.Errorf("pato-local runs = %d, want 2", byKey["pato-local"].Runs)
	}
	if byKey["pato-nube"].Runs != 1 {
		t.Errorf("pato-nube runs = %d, want 1", byKey["pato-nube"].Runs)
	}
}

// A manual landing is accepted work even when the engine's ordinary accept
// path could not make the commit. It must not poison the scorecard of every
// duckling that participated merely because its original close was a reject.
func TestLandedResolutionDoesNotLowerPassRate(t *testing.T) {
	landed := run("solo", "FAILED", nil)
	landed.Resolution = "landed"
	landed.CommitSHA = "971cf8c"
	landed.Spend = map[string]runlog.DucklingSpend{
		"terra": {Calls: 1, Tokens: 500, CostUSD: 0.01},
	}

	rep := Build([]*runlog.Run{landed}, Options{By: "duckling"})
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %+v, want terra's landed run", rep.Rows)
	}
	if got := rep.Rows[0].PassRate(); got != 100 {
		t.Errorf("landed pass rate = %.1f, want 100; a manual landing must not count as a failure", got)
	}
}

func TestTournamentResolutionsAreCounted(t *testing.T) {
	a := run("tournament", "PASSED", nil)
	a.Resolution = "short_circuit"
	b := run("tournament", "PASSED", nil)
	b.Resolution = "judge_pick"
	c := run("tournament", "FAILED", nil)
	c.Resolution = "no_winner"

	rep := Build([]*runlog.Run{a, b, c}, Options{By: "mode"})
	if len(rep.Resolved) != 3 {
		t.Fatalf("got %d resolutions, want 3: %+v", len(rep.Resolved), rep.Resolved)
	}
	out := Render(rep)
	if !strings.Contains(out, "short_circuit=1") {
		t.Errorf("resolution mix missing:\n%s", out)
	}
}

func TestSinceFiltersOlderRuns(t *testing.T) {
	old := run("solo", "PASSED", nil)
	old.StartedAt = time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	recent := run("solo", "PASSED", nil)

	rep := Build([]*runlog.Run{old, recent}, Options{By: "mode", Since: time.Now().Add(-24 * time.Hour)})
	if rep.Rows[0].Runs != 1 {
		t.Errorf("Runs = %d, want 1", rep.Rows[0].Runs)
	}
}

// Estimated token counts must never be silently mixed with measured ones.
func TestEstimatedTokensAreMarked(t *testing.T) {
	r := run("solo", "PASSED", nil)
	rep := Build([]*runlog.Run{r}, Options{By: "mode"})
	rep.Rows[0].Estimated = true
	if !strings.Contains(Render(rep), "~") {
		t.Error("estimated tokens rendered without the ~ marker")
	}
}

func TestEmptyReportDoesNotPanic(t *testing.T) {
	out := Render(Build(nil, Options{By: "mode"}))
	if out == "" {
		t.Error("empty report rendered nothing at all")
	}
}

// The table the spec prints (03 §3.10) has an avg_wall column and underscored
// token counts. Render produced both; the CLI printed neither, because it had
// its own renderer — AC-16 forbids it importing this package, so a second one
// grew and drifted.
//
// This asserts the shape the CLI is now required to receive.
func TestRenderMatchesTheSpecTable(t *testing.T) {
	rep := Build([]*runlog.Run{
		{Mode: "solo", Verdict: "PASSED", Budget: runlog.BudgetState{Tokens: 18_400}, WallclockMs: 72_000},
		{Mode: "solo", Verdict: "FAILED", Budget: runlog.BudgetState{Tokens: 18_400}, WallclockMs: 72_000},
		{Mode: "pair", Verdict: "PASSED", Budget: runlog.BudgetState{Tokens: 52_100}, WallclockMs: 243_000},
	}, Options{By: "mode"})
	got := Render(rep)

	for _, want := range []string{
		"avg_wall", // the column the CLI dropped
		"18_400",   // underscored, as the spec prints them
		"1m12s",    // a duration a person can read
		"4m03s",    // zero-padded seconds
		"solo baseline:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Render is missing %q:\n%s", want, got)
		}
	}
}

// AC-61: measured and estimated counts are never summed without saying so.
func TestRenderMarksEstimatedCounts(t *testing.T) {
	rep := Build([]*runlog.Run{
		{Mode: "solo", Verdict: "PASSED", Budget: runlog.BudgetState{Tokens: 2000}, TokensEstimated: true},
	}, Options{By: "mode"})
	if got := Render(rep); !strings.Contains(got, "~") {
		t.Errorf("an estimated count was printed as measured:\n%s", got)
	}
	rep = Build([]*runlog.Run{
		{Mode: "solo", Verdict: "PASSED", Budget: runlog.BudgetState{Tokens: 2000}},
	}, Options{By: "mode"})
	if got := Render(rep); strings.Contains(got, "~") {
		t.Errorf("a measured count was marked estimated:\n%s", got)
	}
}
