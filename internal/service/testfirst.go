package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/tools"
	"github.com/jrullan/ducklab/internal/vcs"
	"github.com/jrullan/ducklab/internal/verify"
)

// Test-first: the test is written, accepted, and only then implemented.
//
// A gate is worth exactly what its tests are worth, and a test the same model
// wrote in the same run as the code is not a gate — it is the model agreeing
// with itself. This is the same argument that keeps the implementer and the
// reviewer decorrelated in `pair` (05 §3.2), applied to the thing that decides
// the verdict.
//
// Splitting them also repairs the tampering guard. A build task that says "add
// tests" disarms it by design (05 §5.3), so bundling test and code hides
// exactly the case the guard exists for. With the test already committed, the
// build task mentions no tests, and touching one is flagged.
//
// Two facts are checked without asking a model anything:
//
//   - Only test files were written. Enforced in the write guard, not requested
//     in a prompt.
//   - The gate went red. A test that passes against code that does not exist
//     yet has asserted nothing, and accepting it would install a permanent
//     false green.

// TestFirstRequest starts a test-first run.
type TestFirstRequest struct {
	TaskID string `json:"task_id"`
	// Duckling overrides the roster. Naming a different model here from the one
	// that will implement is the point of the exercise.
	Duckling string `json:"duckling"`
}

// TestStart writes the failing test for a task.
func (s *Service) TestStart(ctx context.Context, projectID string, req TestFirstRequest) (*runlog.Run, error) {
	if strings.TrimSpace(req.TaskID) == "" {
		return nil, fmt.Errorf("test: no task given")
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	if err := checkRunnable(entry.Path); err != nil {
		return nil, err
	}
	projCfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return nil, err
	}
	if err := checkTestGate(projCfg.Verify.Mode); err != nil {
		return nil, err
	}

	run := &runlog.Run{
		ID:        runlog.GenerateRunID(),
		ProjectID: projectID,
		Stage:     "build",
		Mode:      "solo",
		TaskID:    req.TaskID,
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Stream:    true,
		Gate:      string(verify.Gate(projCfg.Verify.Mode)),
	}
	writer, err := runlog.NewWriter(entry.Path, run)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	rs := &runState{
		run: run, writer: writer, runDir: writer.RunDir(),
		projectPath: entry.Path, cancel: cancel, done: make(chan struct{}),
	}
	s.attachWriter(rs, writer)
	s.runsMu.Lock()
	s.runs[run.ID] = rs
	s.runsMu.Unlock()

	writer.AppendEvent("run_start", map[string]interface{}{
		"stage": "test", "mode": "solo", "task_id": req.TaskID,
	})

	go s.executeTestFirst(runCtx, rs, entry.Path, projCfg, req)
	return run, nil
}

func (s *Service) executeTestFirst(ctx context.Context, rs *runState, projectRoot string, projCfg *config.Project, req TestFirstRequest) {
	defer recoverRun(rs)
	defer close(rs.done)
	defer rs.writer.Close()

	// The gate before anything is written. A suite that was already red stays
	// red for its own reasons, and reading that as "the new test fails" would
	// accept a test that asserts nothing (05 §5.2).
	before, err := verify.Run(projectRoot, projCfg.Verify)
	if err != nil {
		s.failRun(rs, fmt.Errorf("gate before: %w", err))
		return
	}
	rs.writer.AppendEvent("gate", map[string]interface{}{
		"gate": string(before.Gate), "cmd": before.Command, "exit": before.ExitCode,
		"phase": "before",
	})

	roster, warning := s.resolveRoster(projCfg)
	if req.Duckling != "" {
		roster[config.RoleImplementer] = config.DucklingID(req.Duckling)
	}
	rs.run.Roster = rosterStrings(roster)
	if warning != "" {
		rs.run.Warning = warning
		rs.writer.AppendEvent("warning", map[string]interface{}{"detail": warning})
	}

	limits := &budget.Budget{
		MaxUSD:        projCfg.Budget.MaxUSD,
		MaxTokens:     int64(s.cfg.Defaults.Budget.MaxTokens),
		MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS,
		MaxTurns:      s.cfg.Defaults.Budget.MaxTurns,
	}
	tracker := budget.NewTracker(limits)
	recordLimits(rs, limits)
	rs.tracker = tracker
	ectx := &tools.ExecContext{
		ProjectRoot: projectRoot,
		RunID:       rs.run.ID,
		ShellPolicy: projCfg.Shell,
		Verify:      projCfg.Verify,
		Autonomy:    config.Autonomy(rs.run.Autonomy),
		// The rule, not a request.
		TestPathsOnly:   true,
		GlobalSkillsDir: globalSkillsDir(),
	}
	cache := &loopCache{
		svc: s, tracker: tracker,
		writer: &runLogAdapter{w: rs.writer, run: rs.run},
		loops:  map[config.DucklingID]*agent.Loop{},
	}
	s.attachStreaming(rs, cache)

	params := &strategy.ExecuteParams{
		ProjectRoot: projectRoot,
		TaskID:      req.TaskID,
		Prompt: testFirstPrompt(
			s.buildTaskPrompt(ctx, rs.run.ProjectID, projectRoot, req.TaskID),
			before.Command),
		ExecContext: ectx,
		Runner:      s.runnerFor(cache, roster, ectx),
		Roster:      roster,
		Gate: func(ctx context.Context) (string, string, error) {
			res, err := verify.Run(projectRoot, projCfg.Verify)
			if err != nil {
				return "none", "", err
			}
			return gateWord(res), res.Output, nil
		},
		Diff:    func() (string, error) { return vcs.New(projectRoot).Diff() },
		OnEvent: func(kind string, data map[string]interface{}) { rs.writer.AppendEvent(kind, data) },
	}

	if _, err := strategy.ExecuteTestFirst(ctx, params); err != nil {
		recordSpend(rs, tracker)
		s.failRun(rs, err)
		return
	}
	recordSpend(rs, tracker)

	after, err := verify.Run(projectRoot, projCfg.Verify)
	if err != nil {
		s.failRun(rs, fmt.Errorf("gate after: %w", err))
		return
	}
	rs.writer.AppendEvent("gate", map[string]interface{}{
		"gate": string(after.Gate), "cmd": after.Command, "exit": after.ExitCode,
		"phase": "after",
	})

	diff, _ := vcs.New(projectRoot).Diff()
	rs.writer.WriteDiff(diff)
	rs.writer.WriteVerify(after.Output)

	verdict, detail := judgeTestFirst(before, after, diff, projCfg.Verify.TestGlobs)
	rs.run.Verdict = verdict
	rs.writer.AppendEvent("verdict", map[string]interface{}{"verdict": verdict, "detail": detail})

	// Always a human gate, whichever way it went. A failing test is the
	// specification of the next run, and installing one nobody read would put
	// a model's opinion where a person's belongs.
	rs.run.Status = "paused"
	rs.run.PendingKind = "gate"
	rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
	rs.run.PendingData = map[string]interface{}{"kind": "test_first", "detail": detail}
	rs.writer.AppendEvent("human_needed", map[string]interface{}{
		"kind": "gate", "verdict": verdict, "detail": detail,
	})
	rs.writer.WriteState()
}

// judgeTestFirst decides whether the written test is worth accepting.
//
// Arithmetic over two gate runs and a diff. No model is asked, because "does
// this test actually fail" is a fact, and a fact a model could influence would
// not be a fact (I2).
func judgeTestFirst(before, after *verify.Result, diff string, globs []string) (verdict, detail string) {
	touched := verify.CheckTampering(diff, "write the failing test", globs)
	if len(touched.Files) == 0 {
		return "FAILED", "no test file was written, so nothing was specified"
	}
	if after.ExitCode == 0 {
		// The trap this whole flow exists to avoid. A green gate here means
		// the test asserts something already true, and accepting it would
		// install a permanent false green.
		return "FAILED", "the gate is still green, so the new test asserts nothing that is not already true"
	}
	if before.ExitCode != 0 {
		// Honest about what is and is not known. The suite was already red, so
		// "it is red now" proves nothing on its own — a person has to read the
		// output (05 §5.2).
		return "UNVERIFIED", "the gate was already red before this run, so read the output to confirm the new test is what fails"
	}
	return "PASSED", fmt.Sprintf("the gate was green and is now red: %s specifies work that does not exist yet",
		strings.Join(touched.Files, ", "))
}

// testFirstPrompt asks for the test and only the test.
func testFirstPrompt(task, gateCommand string) string {
	var b strings.Builder
	b.WriteString(task)
	b.WriteString("\n\n## What this run is for\n\n")
	b.WriteString("Write the test that will decide whether this task was done correctly. " +
		"Do not implement the task.\n\n")
	b.WriteString("The implementation is a separate run, by a different duckling, judged by " +
		"the test you write now. Writing the implementation here would mean the same model " +
		"wrote the check and the thing being checked, and the result would measure nothing.\n\n")
	b.WriteString("Requirements for the test:\n\n")
	b.WriteString("- It must **fail** against the code as it is today. A test that already " +
		"passes has asserted nothing. This is checked after your turn, and a green gate " +
		"fails the run.\n")
	b.WriteString("- It must test behaviour the task describes, not the shape of an " +
		"implementation that does not exist yet.\n")
	b.WriteString("- Cover the boundaries the task implies, not only the obvious case.\n")
	fmt.Fprintf(&b, "- The gate is `%s`. Run it with verify_run to see your test fail.\n", gateCommand)
	b.WriteString("\nThe filesystem tools will refuse any path that is not a test file. " +
		"That is deliberate, not a mistake to work around.\n")
	return b.String()
}

// checkTestGate refuses a project whose gate does not run tests.
//
// A compiler, a linter or a bespoke script gives a new test nothing to hook
// into: it can be written and the gate will not notice, so "the gate went red"
// — the one fact this whole flow rests on — cannot mean what it says.
//
// Found the hard way. On a project gated by a custom HTML checker, the model
// reasoned its way to the only place an assertion could live and tried to
// patch the gate script itself. The write guard refused, correctly, and the
// run had nowhere left to go.
//
// A custom gate that does run tests is welcome; it just has to say so, which
// is what verify.mode = tests is for.
func checkTestGate(mode string) error {
	if verify.Gate(mode) == verify.GateTests {
		return nil
	}
	what := "no gate"
	if mode != "" && mode != "none" {
		what = fmt.Sprintf("a %q gate", mode)
	}
	return fmt.Errorf(
		"test-first needs a gate that runs tests, and this project has %s. "+
			"A test written now would change nothing the gate can see.\n"+
			"If your gate does run tests, say so:\n"+
			"  ducklab project set verify.mode tests\n"+
			"  ducklab project set verify.tests \"<command>\"", what)
}
