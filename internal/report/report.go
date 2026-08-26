// Package report aggregates run records into the comparison the whole project
// exists to produce: did the combination beat the single model?
//
// Everything here is arithmetic over recorded runs. No model is consulted —
// a measurement that a model could influence would not be a measurement.
package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

// BaselineMode is what every other mode is measured against (05 §4.1).
const BaselineMode = "solo"

// Row is one aggregated group.
type Row struct {
	Key        string  `json:"key"`
	Runs       int     `json:"runs"`
	Passed     int     `json:"passed"`
	Unverified int     `json:"unverified"`
	Failed     int     `json:"failed"`
	Tokens     int64   `json:"tokens"`
	CostUSD    float64 `json:"cost_usd"`
	WallMs     int64   `json:"wallclock_ms"`
	// Estimated is true when any run in this group had estimated token counts.
	// Measured and estimated numbers are never silently mixed (04 §7).
	Estimated bool `json:"estimated"`
	// Builds counts the runs in this group that could have reached PASSED.
	//
	// Only a build run has an executable gate. An artifact stage — intake,
	// spec, plan — writes a document and ends UNVERIFIED by design, so its
	// pass rate is always zero and comparing it against a build mode's is a
	// category error dressed up as a measurement.
	Builds int `json:"builds"`
	// Excluded counts document runs superseded by a revision. They are neutral
	// evidence and are left out of the pass-rate denominator.
	Excluded int `json:"excluded,omitempty"`
	// NoChanges counts runs that finished without touching a file, because the
	// work was already in the tree.
	//
	// Kept out of the pass rate. A run that wrote nothing is not evidence that
	// a mode can build: on a real session, one of pair's two "passes" changed
	// no files, and pair's reported +25 points over solo rested on a sample of
	// one real build and one no-op.
	NoChanges int `json:"no_changes"`
	// NoChangePasses is how many of those also reached PASSED, so they can be
	// taken off both sides of the rate rather than only the denominator.
	NoChangePasses int `json:"no_change_passes"`
}

// Comparable reports whether this group's pass rate means anything.
//
// A group of nothing but artifact stages has a pass rate of zero because
// nothing in it could ever pass, not because anything went wrong.
func (r Row) Comparable() bool { return r.Builds > 0 }

// Effective is the runs that carry information about whether a mode works:
// the ones that actually attempted something.
func (r Row) Effective() int { return r.Runs - r.NoChanges - r.Excluded }

// PassRate is the share of attempts that succeeded. Document stages use
// human acceptance; executable stages use the gate verdict. Superseded
// document runs and runs that changed no file are left out of both sides.
func (r Row) PassRate() float64 {
	if r.Effective() == 0 {
		return 0
	}
	// Passed counts every PASSED, including the no-change ones, so they come
	// off the top as well as off the bottom.
	passed := r.Passed - r.NoChangePasses
	if passed < 0 {
		passed = 0
	}
	return float64(passed) / float64(r.Effective()) * 100
}

func (r Row) avg(total int64) int64 {
	if r.Runs == 0 {
		return 0
	}
	return total / int64(r.Runs)
}

// AvgTokens returns the mean total tokens per run.
func (r Row) AvgTokens() int64 { return r.avg(r.Tokens) }

// AvgCost returns the mean cost per run.
func (r Row) AvgCost() float64 {
	if r.Runs == 0 {
		return 0
	}
	return r.CostUSD / float64(r.Runs)
}

// AvgWall returns the mean wall-clock time per run.
func (r Row) AvgWall() time.Duration {
	return time.Duration(r.avg(r.WallMs)) * time.Millisecond
}

// Report is the aggregation result.
type Report struct {
	By       string       `json:"by"`
	Rows     []Row        `json:"rows"`
	Baseline string       `json:"baseline"`
	Deltas   []Delta      `json:"deltas,omitempty"`
	Since    time.Time    `json:"since"`
	Resolved []Resolution `json:"resolutions,omitempty"`
}

// Delta is a mode's pass rate compared with the solo baseline.
type Delta struct {
	Key      string  `json:"key"`
	PassRate float64 `json:"pass_rate"`
	Points   float64 `json:"points_vs_baseline"`
	N        int     `json:"n"`
	// OneRun marks a delta drawn from a single run, where the pass rate can
	// only be 0 or 100 and therefore says nothing about the mode. Not a
	// threshold judgement: one sample is not a rate at all.
	OneRun bool `json:"one_run,omitempty"`
}

// Resolution counts how tournaments ended.
type Resolution struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// Options selects and groups runs.
type Options struct {
	Since time.Time
	By    string // mode | duckling | role | task | duckling_role
}

// Build aggregates runs into a report.
func Build(runs []*runlog.Run, opts Options) *Report {
	by := opts.By
	if by == "" {
		by = "mode"
	}
	rep := &Report{By: by, Baseline: BaselineMode, Since: opts.Since}

	groups := map[string]*Row{}
	resolutions := map[string]int{}

	for _, r := range runs {
		if r == nil {
			continue
		}
		if !opts.Since.IsZero() && !startedAfter(r, opts.Since) {
			continue
		}
		// A run that never reached a verdict is still in flight; counting it
		// would make an unfinished run look like a failure.
		if r.Verdict == "" {
			continue
		}
		for _, key := range keysFor(r, by) {
			g := groups[key]
			if g == nil {
				g = &Row{Key: key}
				groups[key] = g
			}
			g.Runs++
			documentStage := isDocumentStage(r.Stage)
			// A superseded document was replaced rather than accepted or
			// rejected, so it is neutral evidence.
			if documentStage && r.Resolution == "superseded" {
				g.Excluded++
			}
			// Only a build run could have passed. An artifact stage ends
			// UNVERIFIED by design, and counting its zero against a build
			// mode's rate is a category error dressed as a measurement.
			if r.Stage == "build" || r.Stage == "" {
				g.Builds++
			}
			// A run that touched nothing passed because the work was already
			// there. Counted, and kept out of the rate.
			if r.NoChanges && !(documentStage && r.Resolution == "superseded") {
				g.NoChanges++
				if r.Verdict == "PASSED" || r.Resolution == "landed" {
					g.NoChangePasses++
				}
			}
			if documentStage {
				// These stages deliberately have no executable gate.
				if r.Resolution != "superseded" {
					if r.Accepted {
						g.Passed++
					} else {
						g.Failed++
					}
				}
				if r.Verdict == "UNVERIFIED" {
					g.Unverified++
				}
			} else {
				switch r.Verdict {
				case "PASSED":
					g.Passed++
				case "UNVERIFIED":
					g.Unverified++
				default:
					if r.Resolution == "landed" {
						g.Passed++
					} else {
						g.Failed++
					}
				}
			}
			// Grouped by duckling, the numbers are that duckling's share.
			// Adding the run's total to every row was what made the rows
			// identical: three models, one run, the whole cost three times.
			if by == "duckling" || by == "duckling_role" {
				id := key
				if by == "duckling_role" {
					id = key[:strings.Index(key, "/")]
				}
				spend := r.Spend[id]
				g.Tokens += spend.Tokens
				g.CostUSD += spend.CostUSD
				if spend.Estimated {
					g.Estimated = true
				}
				// Wallclock stays the run's: two models in one run did not
				// each take the whole time, but nothing records how it split,
				// and inventing a division would be worse than a known
				// overlap.
				g.WallMs += r.WallclockMs
			} else {
				g.Tokens += r.Budget.Tokens
				g.CostUSD += r.Budget.USD
				g.WallMs += r.WallclockMs
				if r.TokensEstimated {
					g.Estimated = true
				}
			}
		}
		if r.Resolution != "" {
			resolutions[r.Resolution]++
		}
	}

	for _, g := range groups {
		rep.Rows = append(rep.Rows, *g)
	}
	sort.Slice(rep.Rows, func(i, j int) bool { return rep.Rows[i].Key < rep.Rows[j].Key })

	for kind, n := range resolutions {
		rep.Resolved = append(rep.Resolved, Resolution{Kind: kind, Count: n})
	}
	sort.Slice(rep.Resolved, func(i, j int) bool { return rep.Resolved[i].Kind < rep.Resolved[j].Kind })

	if by == "mode" {
		rep.Deltas = computeDeltas(rep.Rows)
	}
	return rep
}

// computeDeltas compares every mode against solo.
func computeDeltas(rows []Row) []Delta {
	var baseline *Row
	for i := range rows {
		if rows[i].Key == BaselineMode {
			baseline = &rows[i]
			break
		}
	}
	if baseline == nil {
		// Without a baseline there is no comparison to report. Inventing one
		// from the best-performing mode would be exactly the self-flattery
		// this report exists to prevent.
		return nil
	}
	var out []Delta
	for _, r := range rows {
		if r.Key == BaselineMode {
			continue
		}
		// A mode that only ever ran artifact stages is not in this race.
		//
		// council writes documents and ends UNVERIFIED by design, so its pass
		// rate is zero forever. Reported against a build baseline it read as
		// "council is 75 points worse than solo", which is not a finding about
		// council — it is a comparison of two different jobs.
		if !r.Comparable() {
			continue
		}
		out = append(out, Delta{
			Key: r.Key, PassRate: r.PassRate(),
			Points: r.PassRate() - baseline.PassRate(), N: r.Runs,
			// One run gives 0% or 100% and nothing else, so it is not a rate.
			OneRun: r.Runs == 1,
		})
	}
	return out
}

func isDocumentStage(stage string) bool {
	switch stage {
	case "intake", "spec", "plan", "release", "chat", "triage":
		return true
	}
	return false
}

func keysFor(r *runlog.Run, by string) []string {
	switch by {
	case "mode":
		if r.Mode == "" {
			return []string{"solo"}
		}
		return []string{r.Mode}
	case "task":
		if r.TaskID == "" {
			return nil
		}
		return []string{r.TaskID}
	case "duckling":
		// What each model actually did, not what the roster said it might.
		//
		// This read the roster and gave every duckling in it the run's whole
		// cost. A solo run names six roles and calls one model, so five models
		// were credited with work they never did and the totals came out
		// tripled — with every row identical, which is how it was noticed.
		var out []string
		for id, spend := range r.Spend {
			if id != "" && spend.Calls > 0 {
				out = append(out, id)
			}
		}
		sort.Strings(out)
		return out
	case "role":
		var out []string
		for role, id := range r.Roster {
			if id != "" {
				out = append(out, role)
			}
		}
		sort.Strings(out)
		return out
	case "duckling_role":
		// What each model did IN EACH SEAT: "id/role" for every seat the
		// roster gave it that it actually worked (spend > 0). A reviewer's
		// pass rate is a different fact from the same model's as
		// implementer, and a seat suggestion needs the one for the seat.
		var out []string
		for role, id := range r.Roster {
			if id == "" {
				continue
			}
			if spend, ok := r.Spend[id]; ok && spend.Calls > 0 {
				out = append(out, id+"/"+role)
			}
		}
		sort.Strings(out)
		return out
	}
	return nil
}

func startedAfter(r *runlog.Run, since time.Time) bool {
	t, err := time.Parse(time.RFC3339, r.StartedAt)
	if err != nil {
		// An unparseable timestamp is included rather than dropped: losing a
		// run from the numbers is worse than including one slightly out of range.
		return true
	}
	return !t.Before(since)
}

// Render formats the report as the table of 03-CLI.md §3.10.
func Render(rep *Report) string {
	var b strings.Builder
	// total_usd beside avg_usd: the average says which model is expensive to
	// run once, the total says where the project's money actually went — a
	// cheap model called constantly can out-spend an expensive one used
	// sparingly, and neither number reveals that alone.
	fmt.Fprintf(&b, "%-12s %5s %7s %11s %7s %11s %8s %10s %9s\n",
		rep.By, "runs", "passed", "unverified", "failed", "avg_tokens", "avg_usd", "total_usd", "avg_wall")
	for _, r := range rep.Rows {
		marker := ""
		if r.Estimated {
			marker = "~"
		}
		fmt.Fprintf(&b, "%-12s %5d %7d %11d %7d %10s%s %8.4f %10.4f %9s\n",
			r.Key, r.Runs, r.Passed, r.Unverified, r.Failed,
			formatTokens(r.AvgTokens()), marker, r.AvgCost(), r.CostUSD, formatDuration(r.AvgWall()))
	}

	if rep.By == "mode" {
		b.WriteString("\n")
		if base := findRow(rep.Rows, BaselineMode); base == nil {
			b.WriteString("no solo runs yet — without the baseline there is nothing to compare against.\n" +
				"run the same task with --mode solo to establish it.\n")
		} else {
			fmt.Fprintf(&b, "%s baseline: %.1f%% passed (n=%d)\n", BaselineMode, base.PassRate(), base.Runs)
			for _, d := range rep.Deltas {
				fmt.Fprintf(&b, "%-14s %.1f%% passed  (%+.1f pts, n=%d)", d.Key+":", d.PassRate, d.Points, d.N)
				if d.OneRun {
					// Said rather than left to the reader to notice: one run
					// gives 0% or 100% and nothing in between.
					b.WriteString("  — one run, so this is not a rate yet")
				}
				b.WriteString("\n")
			}
			// Named rather than silently absent. A mode that vanished from the
			// comparison with no explanation reads as a bug in the report.
			for _, r := range rep.Rows {
				if r.Key != BaselineMode && !r.Comparable() {
					fmt.Fprintf(&b, "%-14s not compared — %d artifact stage(s), which have no gate to pass\n",
						r.Key+":", r.Runs)
				}
			}
			// Said where the rate is read. A rate computed over three runs and
			// presented beside a count of four is a rate the reader cannot
			// check, and the difference is exactly the runs that did nothing.
			for _, r := range rep.Rows {
				if r.NoChanges > 0 {
					fmt.Fprintf(&b,
						"%-14s %d of %d runs changed no file (the work was already there) and are left out of the rate\n",
						r.Key+":", r.NoChanges, r.Runs)
				}
			}
		}
	}

	if len(rep.Resolved) > 0 {
		b.WriteString("\nresolutions: ")
		var parts []string
		for _, r := range rep.Resolved {
			parts = append(parts, fmt.Sprintf("%s=%d", r.Kind, r.Count))
		}
		b.WriteString(strings.Join(parts, " "))
		b.WriteString("\n")
	}
	return b.String()
}

func findRow(rows []Row, key string) *Row {
	for i := range rows {
		if rows[i].Key == key {
			return &rows[i]
		}
	}
	return nil
}

func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d_%03d", n/1000, n%1000)
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
