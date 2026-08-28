package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// Accepting a test-first run commits a FAILING test — the definition of done,
// not the work. Labelled "build", the board read it as a finished task and
// offered "build again" for work that had never been built once; the person
// asked, reasonably, why nothing suggested it was not done yet.
func TestAnAcceptedTestFirstIsNotAFinishedTask(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-001 — Core\n\n### T-047 — Pick the stack\n\nDo it.\n",
	})
	run := &runlog.Run{
		ID: "r-test", ProjectID: id, TaskID: "T-047", Stage: "test",
		Status: "done", Verdict: "PASSED", Accepted: true,
		StartedAt: "2026-08-06T03:00:12Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var tv *TaskView
	for i := range tasks {
		if tasks[i].ID == "T-047" {
			tv = &tasks[i]
		}
	}
	if tv == nil {
		t.Fatal("T-047 not listed")
	}
	if tv.Status != "todo" {
		t.Errorf("status = %q, want todo — the task was never built", tv.Status)
	}
	if !tv.TestReady {
		t.Error("the committed failing test is not surfaced")
	}
	offersRun := false
	for _, a := range tv.Next {
		if a == "run" {
			offersRun = true
		}
	}
	if !offersRun {
		t.Errorf("the buildable task offers no run: %v", tv.Next)
	}

	// The build reaches its gate and pauses: the task sits in Review with
	// the build DONE and waiting for a person. "Build it to make it pass"
	// would be false there — the flag speaks only while building is the
	// next move.
	paused := &runlog.Run{
		ID: "r-paused", ProjectID: id, TaskID: "T-047", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		StartedAt: "2026-08-06T03:30:00Z",
	}
	pw, err := runlog.NewWriter(dir, paused)
	if err != nil {
		t.Fatal(err)
	}
	pw.Close()
	s.RecoverRuns(context.Background())
	tasks, _ = s.TaskList(context.Background(), id)
	for i := range tasks {
		if tasks[i].ID == "T-047" {
			tv = &tasks[i]
		}
	}
	if tv.Status != "review" {
		t.Fatalf("with the build paused at its gate, status = %q, want review", tv.Status)
	}
	if tv.TestReady {
		t.Error("a task in review still says build it to make it pass")
	}
	if err := s.RunReject(context.Background(), "r-paused", "not this one"); err != nil {
		t.Fatal(err)
	}

	// The build lands and is accepted: the test no longer awaits anything,
	// and the card must stop saying it does.
	build := &runlog.Run{
		ID: "r-build", ProjectID: id, TaskID: "T-047", Stage: "build",
		Status: "done", Verdict: "PASSED", Accepted: true,
		StartedAt: "2026-08-06T04:00:00Z",
	}
	bw, err := runlog.NewWriter(dir, build)
	if err != nil {
		t.Fatal(err)
	}
	bw.Close()
	s.RecoverRuns(context.Background())
	tasks, _ = s.TaskList(context.Background(), id)
	for i := range tasks {
		if tasks[i].ID == "T-047" {
			tv = &tasks[i]
		}
	}
	if tv.Status != "accepted" {
		t.Errorf("after the accepted build, status = %q, want accepted", tv.Status)
	}
	if tv.TestReady {
		t.Error("a satisfied test still claims to await its build")
	}
}

// An aborted (or failed) TEST run blocks its task — but the retry it offers
// must be the chain, not the build: the definition of done never landed, so
// "run" first would build against nothing. The person aborted T-019's test,
// found it in Blocked, and the rail gave them no way to restart test+build.
func TestRejectingChainedTestDoesNotStartBuild(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	gitProject(t, dir)
	run := &runlog.Run{
		ID: "r-rejected-chain", ProjectID: id, TaskID: "T-001", Stage: "test",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate", StartedAt: time.Now().UTC().Format(time.RFC3339),
		ChainBuild: map[string]interface{}{"task_id": "T-001", "mode": "solo"},
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.RunReject(context.Background(), run.ID, "needs another test"); err != nil {
		t.Fatal(err)
	}
	runs, err := s.RunList(context.Background(), RunFilter{ProjectID: id})
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range runs {
		if got.TaskID == run.TaskID && got.Stage == "build" {
			t.Fatalf("rejected test started chained build %s", got.ID)
		}
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.ChainBuild != nil {
		t.Fatal("rejected test retained its build chain")
	}
}

func TestAFailedTestRunOffersTheChainAgain(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{
		"verify.mode": "tests", "verify.tests": "true",
	}); err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-ab", ProjectID: id, TaskID: "T-001", Stage: "test",
		Status: "failed", Verdict: "FAILED",
		StartedAt: "2026-08-06T15:24:00Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tv := range tasks {
		if tv.ID != "T-001" {
			continue
		}
		if tv.Status != "blocked" {
			t.Fatalf("status = %q, want blocked", tv.Status)
		}
		if len(tv.Next) == 0 || tv.Next[0] != "test_first" {
			t.Errorf("next = %v — the failed test's retry does not lead with the chain", tv.Next)
		}
	}
}

// The chain: a red test-first commits itself — pre-authorized by the click —
// and the build starts at once. Four interactions per task became one
// decision, at the build's gate.
func TestTheTddChainCommitsTheTestAndStartsTheBuild(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindPlan)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan),
		[]byte("## M-001 — Core\n\n### T-003 — Do a thing\n\nDo it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The test-first run, landed red (PASSED) and paused at its gate, with
	// the chain riding on the record — as TestStart writes it.
	run := &runlog.Run{
		ID: "r-tf", ProjectID: p.ID, TaskID: "T-003", Stage: "test",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		StartedAt:  "2026-08-06T12:00:00Z",
		ChainBuild: map[string]interface{}{"task_id": "T-003", "mode": "solo"},
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())
	s.runsMu.RLock()
	rs := s.runs["r-tf"]
	s.runsMu.RUnlock()
	if _, err := s.ensureWriter(rs); err != nil {
		t.Fatal(err)
	}

	s.chainBuild(context.Background(), rs, TestFirstRequest{
		TaskID: "T-003", ThenBuild: true,
		Build: RunRequest{Mode: "solo"},
	})

	got, _ := s.RunGet(context.Background(), "r-tf")
	if !got.Run.Accepted {
		t.Fatalf("the red test was not committed: %s %s", got.Run.Status, got.Run.Resolution)
	}
	if !strings.Contains(got.Run.Resolution, "auto:tdd") {
		t.Errorf("resolution = %q — the record must say the chain decided, not a person", got.Run.Resolution)
	}

	// And a build run exists for the task.
	runs, _ := s.RunList(context.Background(), RunFilter{ProjectID: p.ID})
	foundBuild := false
	for _, r := range runs {
		if r.TaskID == "T-003" && r.Stage == "build" {
			foundBuild = true
		}
	}
	if !foundBuild {
		t.Error("no build run was started after the commit")
	}
	s.runsMu.RLock()
	pending := make([]*runState, 0, len(s.runs))
	for _, rs := range s.runs {
		pending = append(pending, rs)
	}
	s.runsMu.RUnlock()
	for _, rs := range pending {
		select {
		case <-rs.done:
		case <-time.After(15 * time.Second):
			t.Fatal("chained run did not finish")
		}
	}
}

// A chain's build mode has the same provenance contract as an ordinary
// RunStart: absent means resolve settings; present means the person requested
// it. Check the persisted record because it is what later explains the run.
func TestChainedBuildModeRecordsResolutionOrExplicitRequest(t *testing.T) {
	for _, tc := range []struct {
		name, requested, wantMode, wantSource string
	}{
		{name: "unpicked build resolves the configured default", wantMode: "pair", wantSource: "settings"},
		{name: "picked build stays a request", requested: "solo", wantMode: "solo", wantSource: "request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno", "pato-dos")
			s.cfg.Defaults.BuildMode = "pair"
			dir := t.TempDir()
			p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindPlan)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan), []byte("## M-001 — Core\n\n### T-110 — Chain mode\n\nDo it.\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			chain := map[string]interface{}{"task_id": "T-110"}
			if tc.requested != "" {
				chain["mode"] = tc.requested
			}
			testRun := &runlog.Run{ID: "r-chain-mode", ProjectID: p.ID, TaskID: "T-110", Stage: "test", Status: "paused", Verdict: "PASSED", PendingKind: "gate", StartedAt: "2026-08-06T12:00:00Z", ChainBuild: chain}
			w, err := runlog.NewWriter(dir, testRun)
			if err != nil {
				t.Fatal(err)
			}
			w.Close()
			s.RecoverRuns(context.Background())
			if _, err := s.RunAccept(context.Background(), testRun.ID, "accept test"); err != nil {
				t.Fatal(err)
			}

			runs, err := s.RunList(context.Background(), RunFilter{ProjectID: p.ID})
			if err != nil {
				t.Fatal(err)
			}
			var build *runlog.Run
			for _, r := range runs {
				if r.Stage == "build" && r.TaskID == "T-110" {
					build = r
					break
				}
			}
			if build == nil {
				t.Fatal("accepting the test did not start the chained build")
			}
			// Check the returned record as well as state.json: mode provenance
			// is part of the chained run's public contract.
			if build.Mode != tc.wantMode {
				t.Errorf("chained build mode = %q, want %q", build.Mode, tc.wantMode)
			}
			if build.ModeSource != tc.wantSource {
				t.Errorf("chained build mode_source = %q, want %q", build.ModeSource, tc.wantSource)
			}
			state, err := os.ReadFile(filepath.Join(dir, ".ducklab", "runs", build.ID, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			var disk map[string]interface{}
			if err := json.Unmarshal(state, &disk); err != nil {
				t.Fatal(err)
			}
			if got, _ := disk["mode"].(string); got != tc.wantMode {
				t.Errorf("state.json mode = %q, want %q", got, tc.wantMode)
			}
			if got, _ := disk["mode_source"].(string); got != tc.wantSource {
				t.Errorf("state.json mode_source = %q, want %q", got, tc.wantSource)
			}
			s.runsMu.RLock()
			pending := make([]*runState, 0, len(s.runs))
			for _, rs := range s.runs {
				pending = append(pending, rs)
			}
			s.runsMu.RUnlock()
			for _, rs := range pending {
				select {
				case <-rs.done:
				case <-time.After(15 * time.Second):
					t.Fatal("chained run did not finish")
				}
			}
		})
	}
}

// The person's exact wound: an already-red suite makes the verdict
// UNVERIFIED, the run pauses, the person accepts by hand — and the promised
// build silently never came; the task fell to Todo and they re-picked it to
// click the Build they had already asked for. The chain lives on the record
// now, and a manual accept continues it.
func TestAManualAcceptContinuesThePausedChain(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindPlan)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan),
		[]byte("## M-001 — Core\n\n### T-012 — Scoring\n\nDo it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-unv", ProjectID: p.ID, TaskID: "T-012", Stage: "test",
		Status: "paused", Verdict: "UNVERIFIED", PendingKind: "gate",
		StartedAt:  "2026-08-06T05:42:00Z",
		ChainBuild: map[string]interface{}{"task_id": "T-012", "mode": "solo"},
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	// The person reads the UNVERIFIED result and accepts it by hand.
	if _, err := s.RunAccept(context.Background(), "r-unv", "the new test is fine"); err != nil {
		t.Fatal(err)
	}

	runs, _ := s.RunList(context.Background(), RunFilter{ProjectID: p.ID})
	foundBuild := false
	for _, r := range runs {
		if r.TaskID == "T-012" && r.Stage == "build" {
			foundBuild = true
		}
	}
	if !foundBuild {
		t.Error("the manual accept did not continue the chain")
	}
	got, _ := s.RunGet(context.Background(), "r-unv")
	if got.Run.ChainBuild != nil {
		t.Error("the chain was not consumed — a second accept would double-build")
	}
	s.runsMu.RLock()
	pending := make([]*runState, 0, len(s.runs))
	for _, rs := range s.runs {
		pending = append(pending, rs)
	}
	s.runsMu.RUnlock()
	for _, rs := range pending {
		select {
		case <-rs.done:
		case <-time.After(15 * time.Second):
			t.Fatal("chained run did not finish")
		}
	}
}

// The prompt licenses the test writer to ask; the harness must catch the
// question. It did not: executeTestFirst failed the run with "human input
// needed" and no question recorded — twice on one task — and even a correct
// pause would then have re-entered the BUILD strategy on answer, because
// RunResume knew only one way back in. This pins the way back.
func TestAnAnsweredTestRunResumesItsOwnStrategy(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"verify.mode": "tests", "verify.tests": "true",
	}); err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-q", ProjectID: p.ID, TaskID: "T-001", Stage: "test",
		Status: "paused", PendingKind: "question",
		Mode: "solo", Roster: map[string]string{"implementer": "pato-uno"},
		PendingData: map[string]interface{}{"question_id": "q-1", "question": "Monday or Sunday?"},
		StartedAt:   "2026-08-06T18:53:00Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	if err := s.RunAnswer(context.Background(), "r-q", "q-1", "ISO weeks, Monday"); err != nil {
		t.Fatalf("answering the test run's question was refused: %v", err)
	}
	s.runsMu.RLock()
	rs := s.runs["r-q"]
	s.runsMu.RUnlock()
	// The answer reached the record, and the run re-entered SOME strategy —
	// the refusal ("a test run cannot be resumed") was the bug.
	if rs.run.PendingKind == "question" {
		t.Error("the run is still waiting on the question it was answered")
	}
	select {
	case <-rs.done:
	case <-time.After(15 * time.Second):
		t.Fatal("the resumed test run never finished")
	}
	if rs.run.Stage != "test" {
		t.Errorf("stage = %q — the resume changed what kind of run this is", rs.run.Stage)
	}
}

// Acceptance is a fact with a commit behind it. Two later retries that found
// the work already in the tree — no_changes, worn as FAILED for honest
// pass-rates — pushed a DELIVERED task into Blocked, and the person hunted a
// bug that lived elsewhere while the board said their task needed retrying.
// The before gate is a launch-time measurement. If a writer pauses after
// creating its test, resuming must not measure that half-finished work as a new
// baseline: the original green-to-red transition is still the evidence.
func TestResumedTestFirstUsesItsLaunchBaseline(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	native := true
	for id, duck := range s.cfg.Ducklings {
		duck.Caps.NativeTools = &native
		s.cfg.Ducklings[id] = duck
	}
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if err := vcs.New(dir).Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), projectID, map[string]string{
		"verify.mode": "tests", "verify.tests": "test ! -f regression_test.go",
	}); err != nil {
		t.Fatal(err)
	}

	toolCall := func(name, args string) provider.ToolCall {
		call := provider.ToolCall{ID: "call-" + name, Type: "function"}
		call.Function.Name, call.Function.Arguments = name, args
		return call
	}
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(_ provider.ChatRequest, call int) *provider.ChatResponse {
		var message provider.Message
		switch call {
		case 1:
			message.ToolCalls = []provider.ToolCall{toolCall("fs_write", `{"path":"regression_test.go","content":"package fixture\n\nimport \"testing\"\n\nfunc TestRegression(t *testing.T) { t.Fatal(\"missing\") }\n"}`)}
		case 2:
			message.ToolCalls = []provider.ToolCall{toolCall("ask_human", `{"question":"Should the regression cover the legacy format too?"}`)}
		default:
			message.Content = "The failing regression test is complete."
		}
		finish := provider.FinishStop
		if len(message.ToolCalls) > 0 {
			finish = provider.FinishToolCalls
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: message, FinishReason: finish}}}
	}

	run, err := s.TestStart(context.Background(), projectID, TestFirstRequest{TaskID: "T-001", Duckling: "pato-uno"})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	select {
	case <-rs.done:
	case <-time.After(15 * time.Second):
		t.Fatal("test writer did not pause")
	}
	if rs.run.Status != "paused" || rs.run.PendingKind != "question" {
		t.Fatalf("state = %s/%s, want paused/question", rs.run.Status, rs.run.PendingKind)
	}
	if _, err := os.Stat(filepath.Join(rs.run.WorktreePath, "regression_test.go")); err != nil {
		t.Fatalf("the pause did not retain the already-written test: %v", err)
	}
	questionID, _ := rs.run.PendingData["question_id"].(string)
	if questionID == "" {
		t.Fatalf("paused question has no id: %#v", rs.run.PendingData)
	}
	if err := s.RunAnswer(context.Background(), run.ID, questionID, "yes"); err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs = s.runs[run.ID]
	s.runsMu.RUnlock()
	select {
	case <-rs.done:
	case <-time.After(15 * time.Second):
		t.Fatal("resumed test writer did not finish")
	}
	if rs.run.Verdict != "PASSED" {
		t.Fatalf("verdict = %q, want PASSED; detail: %#v", rs.run.Verdict, rs.run.PendingData)
	}
	detail, _ := rs.run.PendingData["detail"].(string)
	if strings.Contains(detail, "already red before this run") {
		t.Fatalf("resume judged the worked tree as its baseline: %q", detail)
	}
	events, err := runlog.ReadEvents(rs.runDir)
	if err != nil {
		t.Fatal(err)
	}
	beforeGates := 0
	for _, event := range events {
		if event.Type == "gate" && event.Data["phase"] == "before" {
			beforeGates++
		}
	}
	if beforeGates != 1 {
		t.Errorf("before-gate measurements = %d, want exactly one", beforeGates)
	}
}

func TestNoChangeRetriesDoNotUndeliverAnAcceptedTask(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	for _, r := range []*runlog.Run{
		{ID: "r-acc", ProjectID: id, TaskID: "T-001", Stage: "build",
			Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: "abc",
			StartedAt: "2026-08-10T01:37:00Z"},
		{ID: "r-nc1", ProjectID: id, TaskID: "T-001", Stage: "build",
			Status: "done", Verdict: "FAILED", NoChanges: true,
			StartedAt: "2026-08-10T11:18:00Z"},
		{ID: "r-nc2", ProjectID: id, TaskID: "T-001", Stage: "build",
			Status: "done", Verdict: "FAILED", NoChanges: true,
			StartedAt: "2026-08-10T11:40:00Z"},
	} {
		w, err := runlog.NewWriter(dir, r)
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
	}
	s.RecoverRuns(context.Background())
	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tv := range tasks {
		if tv.ID == "T-001" && tv.Status != "accepted" {
			t.Errorf("status = %q, want accepted — delivery is a fact", tv.Status)
		}
	}
}

// A conversation about a task is not an attempt at it: the chat run carries
// the task id for its dossier, and its "done, unaccepted" ending stamped
// "the last run done — retry" on a delivered task.
func TestAChatAboutATaskDoesNotFoldIntoItsStatus(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	for _, r := range []*runlog.Run{
		{ID: "r-acc2", ProjectID: id, TaskID: "T-001", Stage: "build",
			Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: "abc",
			StartedAt: "2026-08-10T01:37:00Z"},
		{ID: "r-chat", ProjectID: id, TaskID: "T-001", Stage: "chat",
			Status: "done", StartedAt: "2026-08-10T11:47:00Z"},
	} {
		w, err := runlog.NewWriter(dir, r)
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
	}
	s.RecoverRuns(context.Background())
	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tv := range tasks {
		if tv.ID != "T-001" {
			continue
		}
		if tv.Status != "accepted" {
			t.Errorf("status = %q, want accepted", tv.Status)
		}
		if tv.Blocked != "" {
			t.Errorf("a chat left a blocked message on the task: %q", tv.Blocked)
		}
	}
}

// "Retry with this note" launched the new test run and left the old FAILED
// one pausing the project: the retry sat queued behind a gate that would
// never move without a second human action nobody knew they owed (T-074).
// A FAILED verdict has nothing to accept — the relaunch IS the reject.
func TestAnExplicitRetryClosesTheFailedGateItRetries(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	g := gitProject(t, dir)
	_ = g
	failedRun := &runlog.Run{
		ID: "r-failed", ProjectID: id, TaskID: "T-001", Stage: "test", Mode: "solo",
		Status: "paused", PendingKind: "gate", Verdict: "FAILED",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, failedRun)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if held := s.projectHeld(id, "T-001"); held == "" {
		t.Fatal("fixture: the failed gate does not hold the project")
	}
	s.settleFailedGateForRetry(context.Background(), id, "T-001")
	detail, err := s.RunGet(context.Background(), "r-failed")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status != "done" || detail.Run.Verdict != "FAILED" {
		t.Errorf("failed run not closed: status=%q verdict=%q", detail.Run.Status, detail.Run.Verdict)
	}
	if held := s.projectHeld(id, "T-001"); held != "" {
		t.Errorf("project still held after the retry's reject: %q", held)
	}
	// A PASSED gate is never a side-effect casualty.
	passed := &runlog.Run{
		ID: "r-passed", ProjectID: id, TaskID: "T-002", Stage: "test", Mode: "solo",
		Status: "paused", PendingKind: "gate", Verdict: "PASSED",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w2, err := runlog.NewWriter(dir, passed)
	if err != nil {
		t.Fatal(err)
	}
	w2.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.settleFailedGateForRetry(context.Background(), id, "T-002")
	detail, _ = s.RunGet(context.Background(), "r-passed")
	if detail.Run.Status != "paused" {
		t.Errorf("a PASSED gate was discarded by a retry: %q", detail.Run.Status)
	}
}
