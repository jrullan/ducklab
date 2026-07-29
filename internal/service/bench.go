package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jrullan/ducklab/internal/bench"
	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
	"github.com/jrullan/ducklab/internal/xplat"
)

// BenchOptions is one bench invocation.
type BenchOptions struct {
	Suite     string
	Ducklings []string
	Modes     []string
	// Root is where the temporary projects go. Empty means the OS temp dir.
	Root string
	// Keep leaves the temporary projects on disk, for looking at what a model
	// actually wrote.
	Keep bool
}

// BenchRun runs a suite and writes the results (03 §3.10, AC-60).
//
// Every cell is an ordinary run through the ordinary machinery, in its own
// throwaway project. Measuring anything else — a special code path, a stubbed
// gate — would measure something users never get.
//
// Cells run one at a time. The point is to compare models, and models sharing
// a GPU do not perform the way they do alone; a bench that ran them
// concurrently would measure contention.
func (s *Service) BenchRun(ctx context.Context, opts BenchOptions) (*bench.Result, string, error) {
	suite, err := bench.Get(opts.Suite)
	if err != nil {
		return nil, "", err
	}
	if len(opts.Ducklings) == 0 {
		return nil, "", fmt.Errorf("bench needs at least one duckling: --ducklings a,b")
	}
	if len(opts.Modes) == 0 {
		opts.Modes = []string{"solo"}
	}
	for _, m := range opts.Modes {
		// council builds no code, so a bench cell in council mode would have
		// no gate to go green. Caught here rather than twenty minutes in.
		switch m {
		case "solo", "pair", "tournament", "split":
		default:
			return nil, "", fmt.Errorf("mode %q cannot build a task; want solo, pair, tournament or split", m)
		}
	}
	for _, d := range opts.Ducklings {
		if _, err := s.ducklings.Get(config.DucklingID(d)); err != nil {
			// Checked before anything runs. Discovering the third duckling is
			// misspelled after twenty minutes of benchmarking is a bad trade.
			return nil, "", fmt.Errorf("duckling %q: %w", d, err)
		}
	}

	started := time.Now().UTC()
	res := &bench.Result{
		Suite: suite.Name, SuiteVersion: suite.Version,
		StartedAt: started.Format(time.RFC3339),
		Ducklings: opts.Ducklings, Modes: opts.Modes,
	}

	grid := suite.Cells(opts.Ducklings, opts.Modes)
	total := len(grid)
	for _, cell := range grid {
		task, ok := suite.Task(cell.Task)
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			// Interrupted. What ran is still written: a partial bench is data,
			// a discarded one is nothing.
			cell.Error = "cancelled before this cell ran"
			res.Cells = append(res.Cells, cell)
			continue
		}
		s.publishBench("bench_cell_start", cell, len(res.Cells)+1, total)
		filled := s.runBenchCell(ctx, task, cell, opts)
		res.Cells = append(res.Cells, filled)
		s.publishBench("bench_cell_end", filled, len(res.Cells), total)
	}

	path, err := writeBenchResult(res, started)
	return res, path, err
}

// runBenchCell materialises one task and runs it.
//
// An error here is recorded on the cell rather than returned: one cell the
// harness could not run must not throw away the whole bench, and a harness
// failure is a different finding from a model failure.
func (s *Service) runBenchCell(ctx context.Context, task bench.Task, cell bench.Cell, opts BenchOptions) bench.Cell {
	dir, err := os.MkdirTemp(opts.Root, "ducklab-bench-")
	if err != nil {
		cell.Error = "temp dir: " + err.Error()
		return cell
	}
	if !opts.Keep {
		defer os.RemoveAll(dir)
	}

	if err := materialise(dir, task); err != nil {
		cell.Error = err.Error()
		return cell
	}

	proj, err := s.ProjectInit(ctx, InitRequest{Path: dir, Name: "bench-" + task.ID})
	if err != nil {
		cell.Error = "project init: " + err.Error()
		return cell
	}
	// Forgotten whichever way this returns. A bench that left five hundred
	// dead projects in the registry would make `project list` useless.
	defer func() { _ = s.ProjectForget(ctx, proj.ID) }()

	// The gate comes from the task, not from detection: a suite task states
	// what decides it, and letting auto-detection pick something else would
	// make the measurement depend on the fixture's file layout.
	if err := s.setBenchVerify(dir, task.Verify); err != nil {
		cell.Error = err.Error()
		return cell
	}

	run, err := s.RunStart(ctx, proj.ID, RunRequest{
		TaskID:    task.ID,
		Mode:      cell.Mode,
		Ducklings: []string{cell.Duckling},
		// yolo: nothing here is waiting for a person, and a bench that paused
		// at a human gate would never finish.
		Autonomy: "yolo",
	})
	if err != nil {
		cell.Error = "run start: " + err.Error()
		return cell
	}
	cell.RunID = run.ID

	final, err := s.waitForRun(ctx, run.ID)
	if err != nil {
		cell.Error = err.Error()
		return cell
	}
	cell.Verdict = final.Verdict
	cell.Tokens = final.Budget.Tokens
	cell.CostUSD = final.Budget.USD
	cell.WallMs = final.WallclockMs
	cell.Estimated = final.TokensEstimated
	return cell
}

// materialise writes a task's starting tree and its plan.
//
// The plan is written the same way a person's is, so the run reads the task
// through the ordinary artifact path rather than a bench-only shortcut.
func materialise(dir string, task bench.Task) error {
	for path, content := range task.Files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	plan := fmt.Sprintf("---\nkind: plan\nversion: 1\napproved_by: human\n---\n\n"+
		"## M-001 — Bench\n\n### %s — %s\n\n**Complexity:** low\n\n%s\n",
		task.ID, task.Title, task.Body)
	if err := os.MkdirAll(filepath.Join(dir, ".ducklab", "docs"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".ducklab", "docs", "plan.md"), []byte(plan), 0o644); err != nil {
		return err
	}

	// A git repo, because a run commits and diffs. Without one the cell fails
	// for a reason that has nothing to do with the model.
	//
	// Init commits what is already on disk, so the fixture is the root commit
	// and the run's diff is exactly what the model did. Adding a second commit
	// here failed with "nothing to commit" and took the whole first bench with
	// it — five cells, five identical harness errors, no measurement.
	if err := vcs.New(dir).Init(); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	return nil
}

// writeBenchResult writes the results file (02 §1).
func writeBenchResult(res *bench.Result, started time.Time) (string, error) {
	dataDir, err := xplat.DataDir()
	if err != nil {
		return "", err
	}
	path := bench.Path(dataDir, res.Suite, started.Format("20060102-150405"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, append(raw, '\n'), 0o644)
}

// setBenchVerify writes the task's gate into the project config.
func (s *Service) setBenchVerify(dir string, v config.Verify) error {
	path := filepath.Join(dir, ".ducklab", "project.toml")
	cfg, err := config.LoadProject(path)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	cfg.Verify = v
	if err := config.SaveProject(path, cfg); err != nil {
		return fmt.Errorf("save project: %w", err)
	}
	return nil
}

// waitForRun blocks until a run reaches a terminal state.
//
// Bounded, like everything else (I3). A cell that hangs forever would hang the
// whole bench, and the only honest thing to record is that it did not finish.
func (s *Service) waitForRun(ctx context.Context, runID string) (*runlog.Run, error) {
	deadline := time.After(benchCellTimeout)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		detail, err := s.RunGet(ctx, runID)
		if err != nil || detail.Run == nil {
			return nil, fmt.Errorf("run %s vanished: %w", runID, err)
		}
		run := detail.Run
		switch run.Status {
		case "done", "failed":
			return run, nil
		case "paused":
			// Under yolo a pause means a question nobody can answer. Recorded
			// as an error, because it is not a model failing the task.
			return nil, fmt.Errorf("run %s paused waiting for a human (%s)", runID, run.PendingKind)
		}
		select {
		case <-tick.C:
		case <-deadline:
			return nil, fmt.Errorf("run %s did not finish within %s", runID, benchCellTimeout)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// benchCellTimeout bounds one cell.
//
// Generous: a tournament on a local 30B model is minutes, not seconds. It
// exists so a wedged cell cannot stall a five-hour suite, not to hurry
// anything.
const benchCellTimeout = 30 * time.Minute

// publishBench reports progress.
//
// A bench is minutes to hours of silence otherwise, and a person watching a
// quiet terminal cannot tell a slow model from a wedged one.
func (s *Service) publishBench(kind string, cell bench.Cell, n, total int) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(bus.Event{
		Type: kind, RunID: cell.RunID, TS: time.Now(),
		Data: map[string]interface{}{
			"task": cell.Task, "duckling": cell.Duckling, "mode": cell.Mode,
			"n": n, "total": total,
			"verdict": cell.Verdict, "error": cell.Error,
		},
	})
}
