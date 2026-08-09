package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
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
