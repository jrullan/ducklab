package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// A committed failing test is a promise with two exits: build until green, or
// withdraw it. Retiring is the second — git's own inverse patch, no model —
// and it must leave the task a clean todo and the record saying what happened.
func TestRetiringATestRevertsItsCommitAndFreesTheTask(t *testing.T) {
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
		[]byte("## M-001 — Core\n\n### T-022 — Sessions\n\nDo it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Baseline first: ProjectInit's own files (.gitignore, project.toml, the
	// plan) belong to the project, not to the test commit. Without this the
	// revert also removed .gitignore, un-ignoring .ducklab — a tangle no real
	// accepted test commit has, because accept only ever finds the run's own
	// changes in the tree.
	git := vcs.New(dir)
	if err := git.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit("baseline"); err != nil {
		t.Fatal(err)
	}

	// The committed failing test, exactly as an accepted test-first leaves it.
	testFile := filepath.Join(dir, "session_test.py")
	if err := os.WriteFile(testFile, []byte("def test_sessions(): assert False\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.AddAll(); err != nil {
		t.Fatal(err)
	}
	sha, err := git.Commit("ducklab: T-022 failing test")
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-t22", ProjectID: p.ID, TaskID: "T-022", Stage: "test",
		Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: sha,
		Resolution: "accepted by auto:tdd",
		StartedAt:  "2026-08-06T15:14:00Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	got, err := s.TestRetire(context.Background(), p.ID, "T-022")
	if err != nil {
		t.Fatal(err)
	}
	if got.RevertSHA == "" {
		t.Error("the record does not carry the revert commit")
	}
	if !strings.Contains(got.Resolution, "retired") {
		t.Errorf("the resolution does not tell the story: %q", got.Resolution)
	}
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("the reverted test file is still in the tree")
	}
	if clean, _ := git.IsClean(); !clean {
		t.Error("the revert left the tree dirty")
	}

	// The task is a clean todo again — no "build it to make it pass", no
	// hold on the project's queue, and the fold does not read the retired
	// run's acceptance as a finished task.
	tasks, err := s.TaskList(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tv := range tasks {
		if tv.ID != "T-022" {
			continue
		}
		if tv.Status != "todo" {
			t.Errorf("status = %q, want todo", tv.Status)
		}
		if tv.TestReady {
			t.Error("a retired test still claims to await its build")
		}
	}
	if reason := s.projectHeld(p.ID, "T-019"); reason != "" {
		t.Errorf("the project is still held after the retire: %q", reason)
	}

	// Retiring twice is refused: the promise was already withdrawn.
	if _, err := s.TestRetire(context.Background(), p.ID, "T-022"); err == nil {
		t.Error("a second retire found something to revert")
	}
}

// The refusal answers the click: it says the verdict first and names the
// files in the way. "The working tree has uncommitted changes" alone left
// the person unsure whether the retire happened, was pending, or was refused
// — and sent them to a terminal to find out which files.
func TestARefusedRetireSaysSoAndNamesTheDirt(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-t", ProjectID: p.ID, TaskID: "T-022", Stage: "test",
		Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: "abc",
		StartedAt: "2026-08-06T10:00:00Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.TestRetire(context.Background(), p.ID, "T-022")
	if err == nil {
		t.Fatal("a dirty tree was reverted over")
	}
	if !strings.HasPrefix(err.Error(), "not retired") {
		t.Errorf("the refusal does not lead with the verdict: %v", err)
	}
	if !strings.Contains(err.Error(), "stray.txt") {
		t.Errorf("the refusal does not name the offending file: %v", err)
	}
}

// The exits are exclusive: once the build landed, the test is accepted work.
func TestRetiringABuiltTestIsRefused(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*runlog.Run{
		{ID: "r-t", ProjectID: p.ID, TaskID: "T-001", Stage: "test",
			Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: "abc",
			StartedAt: "2026-08-06T10:00:00Z"},
		{ID: "r-b", ProjectID: p.ID, TaskID: "T-001", Stage: "build",
			Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: "def",
			StartedAt: "2026-08-06T11:00:00Z"},
	} {
		w, err := runlog.NewWriter(dir, r)
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
	}
	s.RecoverRuns(context.Background())

	_, err = s.TestRetire(context.Background(), p.ID, "T-001")
	if err == nil {
		t.Fatal("a satisfied test was retired")
	}
	if !strings.Contains(err.Error(), "accepted work") {
		t.Errorf("the refusal does not explain itself: %v", err)
	}
}

// The action travels the contract: a test-ready task offers retire_test, so
// the board renders the exit next to "build it" instead of hiding it in an
// API only I know about.
func TestATestReadyTaskOffersTheRetireAction(t *testing.T) {
	next := taskNextActions("todo", "tests", true, false, true, false)
	found := false
	for _, a := range next {
		if a == "retire_test" {
			found = true
		}
	}
	if !found {
		t.Errorf("next = %v — the promise has no withdraw button", next)
	}
	// And a task with no outstanding test has nothing to retire.
	for _, a := range taskNextActions("todo", "tests", true, false, false, false) {
		if a == "retire_test" {
			t.Error("retire_test offered with no committed test outstanding")
		}
	}
}
