// Package bench defines the benchmark suites and the shape of their results
// (03 §3.10).
//
// A bench is the only way to answer the question the project exists to ask:
// is a pair better than a solo, and by how much, on this machine with these
// models. A report answers it from whatever runs happened to be made — real
// tasks, uneven, self-selected. A bench answers it from the same tasks every
// time.
//
// Everything here is data and arithmetic. Running the cells belongs to the
// service, because a bench cell is an ordinary run and measuring anything else
// would be measuring a different thing than users get.
package bench

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/config"
)

// Task is one self-contained problem.
//
// Self-contained means it brings its own files and its own gate, needs no
// network, and does not depend on any other task. A suite task that needed the
// internet would measure the internet.
type Task struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
	// Files is the starting tree, path to content.
	Files map[string]string `json:"files"`
	// Verify is the gate. It decides the cell, and no model touches it.
	Verify config.Verify `json:"verify"`
}

// Suite is a versioned set of tasks.
//
// The version is part of every result. Comparing a run of std v1 against std
// v2 is comparing two different questions, and a results file that did not say
// which it answered would invite exactly that.
type Suite struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Tasks   []Task `json:"tasks"`
}

// Cell is one (task, duckling, mode) measurement.
type Cell struct {
	Task     string `json:"task"`
	Duckling string `json:"duckling"`
	Mode     string `json:"mode"`
	RunID    string `json:"run_id"`
	Verdict  string `json:"verdict"`
	Tokens   int64  `json:"tokens"`
	// Estimated is true when the provider reported no usage and the tokens
	// were counted by estimate. Never summed with measured counts silently.
	Estimated bool    `json:"estimated"`
	CostUSD   float64 `json:"cost_usd"`
	WallMs    int64   `json:"wallclock_ms"`
	// Error is set when the cell could not be run at all, which is different
	// from a task the model failed. A crashed harness that looked like a
	// failing model would corrupt the only number here worth having.
	Error string `json:"error,omitempty"`
}

// Passed reports whether the cell reached a green gate.
//
// UNVERIFIED is not a pass. Nothing ran, so nothing is known, and counting it
// would inflate the number this whole package exists to measure honestly.
func (c Cell) Passed() bool { return c.Verdict == "PASSED" }

// Result is one bench invocation.
type Result struct {
	Suite        string `json:"suite"`
	SuiteVersion int    `json:"suite_version"`
	// StartedAt is passed in rather than read from the clock, so the caller
	// owns the timestamp and a test can be deterministic.
	StartedAt string   `json:"started_at"`
	Ducklings []string `json:"ducklings"`
	Modes     []string `json:"modes"`
	Cells     []Cell   `json:"cells"`
}

// Cells returns the (task, duckling, mode) grid in a fixed order.
//
// Sorted, not in map order, so two runs of the same suite produce files that
// diff cleanly. "Structurally reproducible" (AC-60) is a property of this
// ordering, not of the numbers — models vary, and a bench that produced
// identical numbers would be measuring nothing.
func (s Suite) Cells(ducklings, modes []string) []Cell {
	var out []Cell
	for _, t := range s.Tasks {
		for _, d := range ducklings {
			for _, m := range modes {
				out = append(out, Cell{Task: t.ID, Duckling: d, Mode: m})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Task != out[j].Task {
			return out[i].Task < out[j].Task
		}
		if out[i].Duckling != out[j].Duckling {
			return out[i].Duckling < out[j].Duckling
		}
		return out[i].Mode < out[j].Mode
	})
	return out
}

// Task returns a task by id.
func (s Suite) Task(id string) (Task, bool) {
	for _, t := range s.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// Path is where a result file goes (02 §1).
//
// ducklabDir is xplat.DataDir(), which already ends in "ducklab". Joining the
// name again here produced .../ducklab/ducklab/bench, which is the kind of
// thing nobody notices until they go looking for the file.
func Path(ducklabDir, suite, stamp string) string {
	return filepath.Join(ducklabDir, "bench", suite, stamp+".json")
}

// Row is a bench result aggregated one way.
type Row struct {
	Key       string
	Runs      int
	Passed    int
	Errors    int
	Tokens    int64
	CostUSD   float64
	WallMs    int64
	Estimated bool
}

// PassRate is the share of cells that reached a green gate.
func (r Row) PassRate() float64 {
	if r.Runs == 0 {
		return 0
	}
	return float64(r.Passed) / float64(r.Runs) * 100
}

// Aggregate groups cells by mode, by duckling, or by both.
func Aggregate(cells []Cell, by string) []Row {
	groups := map[string]*Row{}
	for _, c := range cells {
		var key string
		switch by {
		case "duckling":
			key = c.Duckling
		case "cross":
			key = c.Duckling + " / " + c.Mode
		default:
			key = c.Mode
		}
		g := groups[key]
		if g == nil {
			g = &Row{Key: key}
			groups[key] = g
		}
		g.Runs++
		if c.Passed() {
			g.Passed++
		}
		if c.Error != "" {
			g.Errors++
		}
		g.Tokens += c.Tokens
		g.CostUSD += c.CostUSD
		g.WallMs += c.WallMs
		if c.Estimated {
			g.Estimated = true
		}
	}
	out := make([]Row, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Render prints a bench result the way `ducklab report` prints a report.
func Render(res Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "suite %s v%d — %d cells, %d ducklings, %d modes\n\n",
		res.Suite, res.SuiteVersion, len(res.Cells), len(res.Ducklings), len(res.Modes))

	fmt.Fprintf(&b, "%-28s %5s %7s %8s %11s %9s\n",
		"duckling / mode", "cells", "passed", "rate", "tokens", "wall")
	for _, r := range Aggregate(res.Cells, "cross") {
		marker := ""
		if r.Estimated {
			marker = "~"
		}
		fmt.Fprintf(&b, "%-28s %5d %7d %7.1f%% %10s%s %9s\n",
			r.Key, r.Runs, r.Passed, r.PassRate(), formatTokens(r.Tokens), marker, formatMs(r.WallMs))
	}

	// The comparison the suite exists for. Without a solo row there is no
	// baseline, and every other number is a measurement of nothing in
	// particular (05 §4.1).
	byMode := Aggregate(res.Cells, "mode")
	var base *Row
	for i := range byMode {
		if byMode[i].Key == "solo" {
			base = &byMode[i]
		}
	}
	b.WriteString("\n")
	if base == nil {
		b.WriteString("no solo cells — without the baseline there is nothing to compare against.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "solo baseline: %.1f%% passed (n=%d)\n", base.PassRate(), base.Runs)
	for _, r := range byMode {
		if r.Key == "solo" {
			continue
		}
		fmt.Fprintf(&b, "%-14s %.1f%% passed  (%+.1f pts, n=%d)\n",
			r.Key+":", r.PassRate(), r.PassRate()-base.PassRate(), r.Runs)
	}

	if errs := countErrors(res.Cells); errs > 0 {
		// Said separately from failures. A harness that could not run a cell
		// and a model that could not solve it are different findings, and
		// folding them together would blame the model for our bug.
		fmt.Fprintf(&b, "\n%d cell(s) could not be run — see `error` in the results file.\n", errs)
	}
	return b.String()
}

func countErrors(cells []Cell) int {
	n := 0
	for _, c := range cells {
		if c.Error != "" {
			n++
		}
	}
	return n
}

func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d_%03d", n/1000, n%1000)
}

func formatMs(ms int64) string {
	s := ms / 1000
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm%02ds", s/60, s%60)
}
