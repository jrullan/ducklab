package service

import (
	"context"
	"encoding/json"
	"errors"
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
	// Mode picks the test-writing conversation: solo (the default, one model
	// writes the failing test) or pair (a decorrelated reviewer critiques the
	// TEST — worth paying for exactly when the chain will commit it unread).
	Mode string `json:"mode,omitempty"`
	// Ducklings seats the test phase positionally: writer, then reviewer.
	// Overrides Duckling when present.
	Ducklings []string `json:"ducklings,omitempty"`
	// ThenBuild chains the flow the person already decided on: when the test
	// lands red, it is committed — pre-authorized by this very request — and
	// the build starts against it at once. Four interactions per task became
	// one decision per task, at the build's gate, with the committed test in
	// the diff. A test that does NOT land red stops the chain and waits for a
	// person, exactly as an unchained run would.
	ThenBuild bool `json:"then_build,omitempty"`
	// Build configures the chained run: mode, ducklings, token ceiling.
	Build RunRequest `json:"build,omitempty"`
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
		// Its own stage, because it is its own kind of run: accepting it
		// commits a FAILING test, not the work. Labelled "build", the board
		// read an accepted test-first as a finished task and offered
		// "build again" for work that had never been built once.
		Stage:     "test",
		Mode:      testMode(req.Mode),
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

	// The chain rides ON THE RECORD, not in this goroutine: acceptance is
	// what triggers the build, and acceptance can come from the automatic
	// red-landing path or from a person deciding a paused UNVERIFIED — the
	// promise must survive both.
	if req.ThenBuild {
		build := req.Build
		build.TaskID = req.TaskID
		if b, mErr := json.Marshal(build); mErr == nil {
			var m map[string]interface{}
			if json.Unmarshal(b, &m) == nil {
				run.ChainBuild = m
			}
		}
	}
	writer.AppendEvent("run_start", map[string]interface{}{
		"stage": "test", "mode": run.Mode, "task_id": req.TaskID,
	})

	// Through the queue, like every run that writes the tree. This path used
	// to spawn its goroutine directly — test-first arrived after the queue
	// and was never wired in — so launching several TDD tasks at once raced
	// their test runs over one working tree, gates measuring each other's
	// half-written files.
	s.queue.submit(s, &queued{
		rs: rs, ctx: runCtx,
		exec: func(c context.Context) { s.executeTestFirst(c, rs, entry.Path, projCfg, req) },
	})
	return run, nil
}

// TestRetire withdraws a committed failing test: git reverts its commit, the
// task returns to a clean todo, and the project's queue is released.
//
// A broken chain — test accepted, build failed or abandoned — leaves the
// suite deliberately red, which holds the whole project (projectHeld). A
// state that blocks the queue owes the person both exits: finish the promise
// (build until green) or withdraw it. This is the second one, and it is
// deterministic — git's own inverse patch, no model involved.
func (s *Service) TestRetire(ctx context.Context, projectID, taskID string) (*runlog.Run, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}

	var target *runState
	var open, builtBy string
	s.runsMu.RLock()
	for _, rs := range s.runs {
		r := rs.run
		if r.ProjectID != projectID || r.TaskID != taskID {
			continue
		}
		switch r.Status {
		case "running", "queued", "paused":
			open = fmt.Sprintf("%s (%s)", r.ID, r.Status)
		}
		if r.Accepted && r.Stage == "build" {
			builtBy = r.ID
		}
		if r.Accepted && r.Stage == "test" && r.RevertSHA == "" {
			if target == nil || r.StartedAt > target.run.StartedAt {
				target = rs
			}
		}
	}
	s.runsMu.RUnlock()

	// Every refusal says the verdict first: the person who clicked is owed
	// "did it happen?" before "why not" — a bare warning left them unsure
	// whether the retire was done, pending, or refused.
	if open != "" {
		return nil, fmt.Errorf("not retired — a run for %s is still open (%s); decide or abort it first", taskID, open)
	}
	if builtBy != "" {
		return nil, fmt.Errorf("not retired — %s was built and accepted (%s): its test is part of the accepted work now, not an outstanding promise", taskID, builtBy)
	}
	if target == nil {
		return nil, fmt.Errorf("not retired — %s has no committed test awaiting a build", taskID)
	}
	if target.run.CommitSHA == "" {
		return nil, fmt.Errorf("not retired — the accepted test run %s recorded no commit; unwind it by hand", target.run.ID)
	}

	git := vcs.New(entry.Path)
	if clean, cerr := git.IsClean(); cerr != nil || !clean {
		dirty := git.DirtyPaths()
		sample := strings.Join(dirty[:min(3, len(dirty))], ", ")
		if len(dirty) > 3 {
			sample += fmt.Sprintf(" and %d more", len(dirty)-3)
		}
		return nil, fmt.Errorf("not retired — the working tree has uncommitted changes (%s); commit or clean them, then retire", sample)
	}
	sha, err := git.Revert(target.run.CommitSHA)
	if err != nil {
		return nil, fmt.Errorf("not retired — git could not undo the test commit: %w", err)
	}

	w, werr := s.ensureWriter(target)
	if werr != nil {
		return nil, fmt.Errorf("the test was reverted (%s) but its record could not be opened: %w", sha, werr)
	}
	target.run.RevertSHA = sha
	target.run.Resolution += fmt.Sprintf("; test retired by human (revert %.8s)", sha)
	w.AppendEvent("test_retired", map[string]interface{}{
		"task": taskID, "revert_sha": sha, "reverted": target.run.CommitSHA,
	})
	if err := w.WriteState(); err != nil {
		return nil, err
	}
	// The suite is green again; whatever queued behind the broken chain can go.
	s.queue.poke(s)
	return target.run, nil
}

func (s *Service) executeTestFirst(ctx context.Context, rs *runState, projectRoot string, projCfg *config.Project, req TestFirstRequest) {
	defer recoverRun(rs)
	defer close(rs.done)
	defer rs.writer.Close()

	// The tree before the test is written — what abort, reject and failure
	// restore. The build path always took this; test-first never did, so an
	// aborted test run left its half-written test file in the tree, where
	// every later gate measured a file nobody had accepted.
	if git := vcs.New(projectRoot); git.HasGit() {
		if snap, serr := git.SnapshotTree(); serr == nil {
			rs.run.TreeSnapshot = snap
			rs.writer.WriteState()
		} else {
			rs.writer.AppendEvent("warning", map[string]interface{}{
				"detail": "could not snapshot the tree; a failure will leave its edits behind: " + serr.Error(),
			})
		}
	}

	// The gate before anything is written. A suite that was already red stays
	// red for its own reasons, and reading that as "the new test fails" would
	// accept a test that asserts nothing (05 §5.2).
	before, err := verify.Run(ctx, projectRoot, projCfg.Verify)
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
	// The test phase's own seats: writer first, reviewer second. Independent
	// of the build's — a person who pairs the build does not owe the test a
	// pair too, and vice versa.
	if len(req.Ducklings) > 0 && req.Ducklings[0] != "" {
		roster[config.RoleImplementer] = config.DucklingID(req.Ducklings[0])
	}
	if len(req.Ducklings) > 1 && req.Ducklings[1] != "" {
		roster[config.RoleReviewer] = config.DucklingID(req.Ducklings[1])
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
	rs.setTracker(tracker)
	ectx := &tools.ExecContext{
		ProjectRoot: projectRoot,
		RunID:       rs.run.ID,
		ShellPolicy: projCfg.Shell,
		Verify:      projCfg.Verify,
		Autonomy:    config.Autonomy(rs.run.Autonomy),
		// The rule, not a request.
		TestPathsOnly:   true,
		GlobalSkillsDir: globalSkillsDir(),
		// Answers a person already gave. Without this a resumed test run
		// asked its question again, was answered again, and asked again —
		// the answer existed and never reached the tool.
		Answers: rs.answers(),
	}
	cache := &loopCache{
		svc: s, tracker: tracker,
		writer:  s.llmWriter(rs, tracker),
		capLift: rs.capLifted.Load,
		loops:   map[config.DucklingID]*agent.Loop{},
	}
	s.attachStreaming(rs, cache)

	params := &strategy.ExecuteParams{
		LiveToolEvents: true,
		ProjectRoot:    projectRoot,
		TaskID:         req.TaskID,
		// The decisions the person already made ride on the prompt itself —
		// the exact wound: a resumed test run reworded its answered question
		// and asked it again, because the answer was filed under a hash of
		// words the model no longer used.
		Prompt: testFirstPrompt(
			s.buildTaskPrompt(ctx, rs.run.ProjectID, projectRoot, req.TaskID),
			before.Command) + rs.answeredDecisions(),
		ExecContext: ectx,
		Runner:      s.runnerFor(cache, roster, ectx),
		Roster:      roster,
		Gate: func(ctx context.Context) (string, string, error) {
			res, err := verify.Run(ctx, projectRoot, projCfg.Verify)
			if err != nil {
				return "none", "", err
			}
			return gateWord(res), res.Output, nil
		},
		Diff:    func() (string, error) { return vcs.New(projectRoot).Diff() },
		OnEvent: func(kind string, data map[string]interface{}) { rs.writer.AppendEvent(kind, data) },
	}

	res, err := strategy.ExecuteTestFirstMode(ctx, testMode(req.Mode), params)
	recordSpend(rs, tracker)
	if err != nil {
		// A pause is not a failure. The prompt licenses the test writer to
		// ask about a decision the task left open — the test IS where such a
		// decision gets baked in — and this path dropped the question on the
		// floor: the run died saying "human input needed" with no question
		// recorded and nothing for a person to answer. Twice, on one task.
		var pending *pendingErr
		if errors.As(pendingOrErr(res, err), &pending) {
			s.pauseForQuestion(rs, pending.q)
			return
		}
		s.failRun(rs, err)
		return
	}

	after, err := verify.Run(ctx, projectRoot, projCfg.Verify)
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
	if req.ThenBuild && verdict == "PASSED" {
		// The chain: commit the red test, start the build. No pause — the
		// person authorized this path when they clicked it.
		rs.writer.WriteState()
		s.chainBuild(ctx, rs, req)
		return
	}
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
	// The test is where an underdetermined decision gets baked in first: a
	// test that assumes "week = Sunday start" makes the build assume it too,
	// and the person learns which was chosen only when the dashboard looks
	// wrong. Asking belongs HERE, before the assumption becomes the spec.
	b.WriteString("\nThe test you write becomes the task's definition of done, so a decision " +
		"the task leaves open — a boundary (where does a \"week\" start?), a format, an " +
		"external contract — gets decided by your assertions. Do not guess one and do not " +
		"spend turns deliberating: call ask_human once, with concrete options. Internals " +
		"the task does not constrain are yours to choose; never ask about those.\n")
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

// chainBuild commits a red test-first result; the accept path sees the
// recorded chain and starts the build.
//
// A failed accept must not lose the test run's own result: it leaves the run
// at its gate with the reason recorded, and the person decides as they would
// have unchained — their accept then continues the chain, because the chain
// lives on the record.
func (s *Service) chainBuild(ctx context.Context, rs *runState, req TestFirstRequest) {
	if _, err := s.RunAcceptAs(ctx, rs.run.ID, "chained: the test landed red", "auto:tdd"); err != nil {
		rs.run.Status = "paused"
		rs.run.PendingKind = "gate"
		rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
		rs.writer.AppendEvent("warning", map[string]interface{}{
			"detail": fmt.Sprintf("tdd chain: accept failed (%v); decide this run by hand", err),
		})
		rs.writer.AppendEvent("human_needed", map[string]interface{}{"kind": "gate", "verdict": rs.run.Verdict})
		rs.writer.WriteState()
	}
}

// testMode normalises the test phase's mode: pair is the one alternative.
func testMode(m string) string {
	if m == "pair" {
		return "pair"
	}
	return "solo"
}
