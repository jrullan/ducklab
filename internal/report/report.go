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
	TokensIn   int64   `json:"tokens_in"`
	TokensOut  int64   `json:"tokens_out"`
	CostUSD    float64 `json:"cost_usd"`
	WallMs     int64   `json:"wallclock_ms"`
	// Estimated is true when any run in this group had estimated token counts.
	// Measured and estimated numbers are never silently mixed (04 §7).
	Estimated bool `json:"estimated"`
}

// PassRate is the share of runs that reached PASSED.
//
// UNVERIFIED is deliberately NOT counted as a pass: nothing was executed, and
// counting it would inflate exactly the number the project is trying to
// measure honestly (P3).
func (r Row) PassRate() float64 {
	if r.Runs == 0 {
		return 0
	}
	return float64(r.Passed) / float64(r.Runs) * 100
}

func (r Row) avg(total int64) int64 {
	if r.Runs == 0 {
		return 0
	}
	return total / int64(r.Runs)
}

// AvgTokens returns the mean total tokens per run.
func (r Row) AvgTokens() int64 { return r.avg(r.TokensIn + r.TokensOut) }

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
}

// Resolution counts how tournaments ended.
type Resolution struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// Options selects and groups runs.
type Options struct {
	Since time.Time
	By    string // mode | duckling | role | task
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
			switch r.Verdict {
			case "PASSED":
				g.Passed++
			case "UNVERIFIED":
				g.Unverified++
			default:
				g.Failed++
			}
			g.TokensOut += r.Budget.Tokens
			g.CostUSD += r.Budget.USD
			g.WallMs += r.WallclockMs
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
		out = append(out, Delta{
			Key: r.Key, PassRate: r.PassRate(),
			Points: r.PassRate() - baseline.PassRate(), N: r.Runs,
		})
	}
	return out
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
		var out []string
		seen := map[string]bool{}
		for _, id := range r.Roster {
			if id != "" && !seen[id] {
				seen[id] = true
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
	fmt.Fprintf(&b, "%-12s %5s %7s %11s %7s %11s %8s %9s\n",
		rep.By, "runs", "passed", "unverified", "failed", "avg_tokens", "avg_usd", "avg_wall")
	for _, r := range rep.Rows {
		marker := ""
		if r.Estimated {
			marker = "~"
		}
		fmt.Fprintf(&b, "%-12s %5d %7d %11d %7d %10s%s %8.4f %9s\n",
			r.Key, r.Runs, r.Passed, r.Unverified, r.Failed,
			formatTokens(r.AvgTokens()), marker, r.AvgCost(), formatDuration(r.AvgWall()))
	}

	if rep.By == "mode" {
		b.WriteString("\n")
		if base := findRow(rep.Rows, BaselineMode); base == nil {
			b.WriteString("no solo runs yet — without the baseline there is nothing to compare against.\n" +
				"run the same task with --mode solo to establish it.\n")
		} else {
			fmt.Fprintf(&b, "%s baseline: %.1f%% passed (n=%d)\n", BaselineMode, base.PassRate(), base.Runs)
			for _, d := range rep.Deltas {
				fmt.Fprintf(&b, "%-14s %.1f%% passed  (%+.1f pts, n=%d)\n", d.Key+":", d.PassRate, d.Points, d.N)
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
