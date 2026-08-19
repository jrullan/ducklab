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
	"github.com/jrullan/ducklab/internal/bug"
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
	// AgentTurns overrides model calls per reply for every test-phase seat.
	AgentTurns int `json:"agent_turns,omitempty"`
	// Build configures the chained run: mode, ducklings, token ceiling.
	Build RunRequest `json:"build,omitempty"`
	// Origin marks a chain started by the autopilot rather than a person.
	Origin string `json:"origin,omitempty"`
	// Note rides the test prompt — what only the launcher knows now, which
	// for an autopilot retry is why the previous attempt failed.
	Note string `json:"note,omitempty"`
	// Verify overrides the project's gate for this task only (for example,
	// `cd frontend && npx vitest run`), without changing project config.
	Verify string `json:"verify,omitempty"`
	// Redo is the explicit consent to redo a task that was already accepted;
	// without it, launching finished work is refused (see RunRequest.Redo).
	Redo bool `json:"redo,omitempty"`
}

const maxRedoCommitsPerTask = 10

// TestStart writes the failing test for a task.
func (s *Service) TestStart(ctx context.Context, projectID string, req TestFirstRequest) (*runlog.Run, error) {
	if strings.TrimSpace(req.TaskID) == "" {
		return nil, fmt.Errorf("test: no task given")
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	// The explicit relaunch is the decision on a FAILED gate for this task;
	// without this the retry queued forever behind the run it retries.
	s.settleFailedGateForRetry(ctx, projectID, req.TaskID)
	// A finished task is refused before any other door is tried: "your gate
	// does not run tests" is noise when the real answer is "T-001 was done
	// days ago". Launched test-first by an overnight operator, the launch
	// itself is the mistake worth catching — before any model is paid.
	tv := s.findTask(ctx, projectID, req.TaskID)
	var priorAcceptedSHA string
	accepted := tv != nil && (tv.Status == "accepted" || tv.TestReady)
	// Raw history supplies redo provenance only; it never changes accepted.
	s.runsMu.RLock()
	for _, candidate := range s.runs {
		r := candidate.run
		if r.ProjectID == projectID && r.TaskID == req.TaskID && r.Accepted && r.Stage == "test" && r.CommitSHA != "" && priorAcceptedSHA == "" {
			priorAcceptedSHA = r.CommitSHA
		}
	}
	s.runsMu.RUnlock()
	if accepted && !req.Redo {
		return nil, fmt.Errorf("%s is already accepted; its work is committed. A new run would redo finished work — pass redo (and say why in a note) if that is truly the intent", req.TaskID)
	}
	if accepted && req.Redo {
		// Redo is a bounded escape hatch, not a way to accumulate an
		// unreviewed second history forever. Count accepted test commits, which
		// includes the original accepted test and every committed redo.
		redoCommits := 0
		s.runsMu.RLock()
		for _, candidate := range s.runs {
			r := candidate.run
			if r.ProjectID == projectID && r.TaskID == req.TaskID && r.Accepted && r.Stage == "test" && r.CommitSHA != "" && r.RevertSHA == "" {
				redoCommits++
			}
		}
		s.runsMu.RUnlock()
		if redoCommits >= maxRedoCommitsPerTask {
			return nil, fmt.Errorf("cannot redo %s: redo commit limit reached (%d per task)", req.TaskID, maxRedoCommitsPerTask)
		}
		if strings.TrimSpace(req.Note) == "" {
			return nil, fmt.Errorf("redo of accepted task %s requires a note explaining why", req.TaskID)
		}
		var open string
		s.runsMu.RLock()
		for _, candidate := range s.runs {
			r := candidate.run
			if r.ProjectID != projectID || r.TaskID != req.TaskID {
				continue
			}
			if r.Accepted && r.Stage == "test" && r.CommitSHA != "" && priorAcceptedSHA == "" {
				priorAcceptedSHA = r.CommitSHA
			}
			switch r.Status {
			case "running", "queued", "paused":
				open = r.ID + " (" + r.Status + ")"
			}
		}
		s.runsMu.RUnlock()
		if open != "" {
			return nil, fmt.Errorf("cannot redo %s while its run is still open (%s); decide or abort it first", req.TaskID, open)
		}
		git := vcs.New(entry.Path)
		clean, cleanErr := git.IsClean()
		if cleanErr != nil {
			return nil, fmt.Errorf("cannot redo %s: could not inspect the working tree: %w", req.TaskID, cleanErr)
		}
		if !clean {
			// Engine metadata is written under .ducklab and is not task work;
			// it must not make an otherwise clean source tree un-relaunchable.
			for _, path := range git.DirtyPaths() {
				if !strings.HasPrefix(path, ".ducklab/") && path != ".ducklab" {
					return nil, fmt.Errorf("cannot redo %s: the working tree is dirty; commit or clean it first", req.TaskID)
				}
			}
		}
		// A redo supersedes an accepted test that has not yet been built. Remove
		// that promise first, using the same deterministic inverse-patch path as
		// the explicit retire action. Ignore synthetic/legacy ledger SHAs that no
		// longer exist in git; they cannot describe a committed test in this tree.
		if priorAcceptedSHA != "" {
			git := vcs.New(entry.Path)
			if _, showErr := git.ShowCommit(priorAcceptedSHA); showErr == nil {
				if _, retireErr := s.TestRetire(ctx, projectID, req.TaskID); retireErr != nil {
					return nil, retireErr
				}
			}
		}
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
	if req.Verify != "" {
		projCfg.Verify = verifyOverride(projCfg.Verify, req.Verify)
	}

	run := &runlog.Run{
		ID:        runlog.GenerateRunID(),
		ProjectID: projectID,
		// Its own stage, because it is its own kind of run: accepting it
		// commits a FAILING test, not the work. Labelled "build", the board
		// read an accepted test-first as a finished task and offered
		// "build again" for work that had never been built once.
		Stage:            "test",
		Mode:             testMode(s.testModeDefault(req.Mode)),
		AgentTurns:       req.AgentTurns,
		TaskID:           req.TaskID,
		TaskBodyHash:     taskBodyHashForTask(ctx, s, projectID, req.TaskID),
		Status:           "running",
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
		Stream:           true,
		Gate:             string(verify.Gate(projCfg.Verify.Mode)),
		Origin:           req.Origin,
		Note:             req.Note,
		PriorAcceptedSHA: priorAcceptedSHA,
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
	if priorAcceptedSHA != "" {
		appendBugAudit(entry.Path, bug.AuditEntry{
			Bug: req.TaskID, Actor: "human", Via: "redo",
			Note: req.Note + " (prior accepted SHA: " + priorAcceptedSHA + ")",
		})
	}

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
		filtered := dirty[:0]
		for _, path := range dirty {
			if !strings.HasPrefix(path, ".ducklab/") && path != ".ducklab" {
				filtered = append(filtered, path)
			}
		}
		dirty = filtered
		if len(dirty) > 0 {
			sample := strings.Join(dirty[:min(10, len(dirty))], ", ")
			if len(dirty) > 10 {
				sample += fmt.Sprintf(" and %d more", len(dirty)-10)
			}
			return nil, fmt.Errorf("not retired — the working tree has uncommitted changes (%s); commit or clean them, then retire", sample)
		}
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
			if head, herr := git.HeadSHA(); herr == nil {
				rs.run.TreeSnapshotHead = head
			} else {
				rs.writer.AppendEvent("warning", map[string]interface{}{
					"detail": "could not record HEAD with the tree snapshot; cleanup will refuse unless the snapshot matches HEAD: " + herr.Error(),
				})
			}
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
	// Announced BEFORE it runs: the suite takes minutes, and a transcript
	// that stays blank while it does reads as an engine hang or a bug —
	// the person must see that something is happening and what.
	rs.writer.AppendEvent("gate_started", map[string]interface{}{
		"phase":  "before",
		"detail": "running the suite before any test is written — a red test only means something against a green baseline",
	})
	before, err := verify.Run(ctx, projectRoot, projCfg.Verify)
	if err != nil {
		s.failRun(rs, fmt.Errorf("gate before: %w", err))
		return
	}
	rs.writer.AppendEvent("gate", map[string]interface{}{
		"gate": string(before.Gate), "cmd": before.Command, "exit": before.ExitCode,
		"phase": "before",
	})

	roster, warning := s.resolveRoster(projCfg, rs.run.Mode)
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
	if roster[config.RoleImplementer] == "" {
		s.failRun(rs, fmt.Errorf("no implementer seated to write the test for %s — assign one on the Roster board (or pass ducklings on the launch)", rs.run.Mode))
		return
	}
	rs.run.Roster = rosterStrings(roster)
	rs.run.RosterSources = s.rosterSources(projCfg, rs.run.Mode, req.Ducklings)
	if req.Duckling != "" {
		rs.run.RosterSources[string(config.RoleImplementer)] = "request"
	}
	if warning != "" {
		rs.run.Warning = warning
		rs.writer.AppendEvent("warning", map[string]interface{}{"detail": warning})
	}

	limitsValue := projectBudget(budget.Budget{
		MaxUSD: s.cfg.Defaults.Budget.MaxUSD, MaxTokens: int64(s.cfg.Defaults.Budget.MaxTokens),
		MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS, MaxTurns: s.cfg.Defaults.Budget.MaxTurns,
	}, projCfg.Budget)
	limits := &limitsValue
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
	rs.execCtx = ectx
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
			before.Command) + humanNote(req.Note) + rs.answeredDecisions(),
		ExecContext: ectx,
		Runner:      s.runnerFor(cache, roster, ectx),
		Roster:      roster,
		TurnCaps:    s.roleTurnCapsFor(req.AgentTurns),
		Diff:        func() (string, error) { return vcs.New(projectRoot).Diff() },
		OnEvent:     func(kind string, data map[string]interface{}) { rs.writer.AppendEvent(kind, data) },
	}

	// The round gate earns its suite only in pair: two rounds, and a green
	// (the test does not fail) sends the writer back with the reviewer's
	// verdict. In solo there is no second round for it to buy, and the
	// stage's own "after" gate measures the same unchanged tree minutes
	// later — so solo runs no round gate at all.
	if testMode(req.Mode) == "pair" {
		params.Gate = func(ctx context.Context) (string, string, error) {
			res, err := verify.Run(ctx, projectRoot, projCfg.Verify)
			if err != nil {
				return "none", "", err
			}
			return gateWord(res), res.Output, nil
		}
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

	rs.writer.AppendEvent("gate_started", map[string]interface{}{
		"phase":  "after",
		"detail": "running the suite over the new test — an honest red is the deliverable",
	})
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

	verdict, detail := judgeTestFirstWithGate(before, after, diff, projCfg.Verify.TestGlobs, after.Command)
	rs.run.Verdict = verdict
	rs.writer.AppendEvent("verdict", map[string]interface{}{"verdict": verdict, "detail": detail})

	// Under yolo with the autopilot driving, a FAILED verdict — green gate,
	// no test written — is a retryable terminal failure, not an inbox item:
	// nobody is watching the pause, and the loop's retry carries the reason
	// as a note. PASSED and UNVERIFIED keep their human gate even here —
	// installing a spec nobody read stays off the table (P3).
	if verdict == "FAILED" && rs.run.Autonomy == "yolo" && s.autopilotOn(rs.run.ProjectID) {
		rs.run.Status = "failed"
		rs.run.Failure = detail
		rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
		rs.writer.AppendEvent("run_end", map[string]interface{}{"verdict": "FAILED"})
		restoreAfterUnaccepted(rs)
		rs.writer.WriteState()
		s.autopilotOnFail(rs.run)
		return
	}

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
	return judgeTestFirstWithGate(before, after, diff, globs, "")
}

func judgeTestFirstWithGate(before, after *verify.Result, diff string, globs []string, gateCommand string) (verdict, detail string) {
	touched := verify.CheckTampering(diff, "write the failing test", globs)
	if len(touched.Files) == 0 {
		return "FAILED", "no test file was written, so nothing was specified"
	}
	if after.ExitCode == 0 {
		// A green result is only meaningful for files the command actually
		// reaches. The old Go-only gate made a correct frontend test look
		// vacuous because it never ran Vitest.
		for _, file := range touched.Files {
			if strings.HasPrefix(filepath.ToSlash(file), "frontend/") && !gateCoversFrontend(gateCommand) {
				return "FAILED", "the new test lives in frontend/, which the gate never runs — widen the gate or move the test"
			}
		}
		// The trap this whole flow exists to avoid. A green gate here means
		// the test asserts something already true, and accepting it would
		// install a permanent false green.
		return "FAILED", "the gate is still green, so the new test asserts nothing that is not already true"
	}
	// A non-zero test command is not enough: go test also returns non-zero when
	// the package (including the new test) cannot be compiled. That is a broken
	// specification, not the assertion-red state the implementation run is
	// supposed to turn green. Check this before the already-red guard so a
	// compile error is never hidden by an unrelated pre-existing failure.
	if compileFailure(after.Output) {
		return "FAILED", fmt.Sprintf("the test specification does not compile; fix the compiler error before retrying: %s", after.Output)
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

// compileFailure reports the compiler/build diagnostics emitted by a test gate.
// Keep this deliberately conservative: ordinary assertion failures remain valid
// red specifications, while the common Go compile diagnostics are rejected.
func gateCoversFrontend(command string) bool {
	lower := strings.ToLower(command)
	return strings.Contains(lower, "frontend") || strings.Contains(lower, "vitest")
}

func compileFailure(output string) bool {
	// Structural, not lexical. Grepping the whole output for compiler
	// vocabulary misfired the moment a TEST carried those words as fixture
	// data — T-050's detector test contained "does not compile" and was
	// itself judged uncompilable, an assertion-red refused as broken. Go
	// stamps an unbuildable package on its FAIL line — [build failed] or
	// [setup failed] — and tsc names its errors "error TS1234:"; assertion
	// failures produce neither.
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "FAIL") &&
			(strings.HasSuffix(trimmed, "[build failed]") || strings.HasSuffix(trimmed, "[setup failed]")) {
			return true
		}
		if strings.Contains(trimmed, "): error TS") {
			return true
		}
		// Vitest/esbuild reports syntax and transform failures without a Go-style
		// package marker. These failures happen before any assertion runs, so they
		// are structural compile failures rather than valid test-first red.
		if strings.Contains(trimmed, "Transform failed with ") ||
			strings.Contains(trimmed, "Failed to parse source") {
			return true
		}
	}
	return false
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
// testModeDefault fills an empty request from the configured default, so a
// launcher-less caller (CLI, autopilot) tests the way the person chose.
func (s *Service) testModeDefault(m string) string {
	if m != "" {
		return m
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg.Defaults.TestMode
}

func testMode(m string) string {
	if m == "pair" {
		return "pair"
	}
	return "solo"
}
