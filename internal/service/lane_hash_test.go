package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// laneTaskPlan is a one-task plan whose task owns the given lane.
func laneTaskPlan(substance, owns string) string {
	return "## M-001 — Core\n\n### T-004 — Fix the legends\n\n" + laneTaskBody(substance, owns) + "\n"
}

func laneTaskBody(substance, owns string) string {
	return "Fixes B-001.\n\n**Acceptance slices:**\n- " + substance + "\n\n**Owns:** " + owns
}

func laneGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

// commitPlan writes the plan and commits it, as every engine plan edit does.
func commitPlan(t *testing.T, dir, plan string) {
	t.Helper()
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	laneGit(t, dir, "add", "-A")
	laneGit(t, dir, "commit", "-q", "-m", "plan")
}

func laneGitProject(t *testing.T, s *Service) (id, dir string) {
	t.Helper()
	id, dir = projectWithDocs(t, s, nil)
	laneGit(t, dir, "init", "-q", "-b", "main")
	laneGit(t, dir, "config", "user.email", "t@t")
	laneGit(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".ducklab/runs/\n.ducklab/ducklab.db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return id, dir
}

func writeAcceptedTaskRun(t *testing.T, dir, projectID, runID, stage, hash string) {
	t.Helper()
	run := &runlog.Run{
		ID: runID, ProjectID: projectID, TaskID: "T-004", Stage: stage,
		Status: "done", Verdict: "PASSED", Accepted: true,
		TaskBodyHash: hash, StartedAt: "2026-10-27T19:44:56Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
}

func taskStatus(t *testing.T, s *Service, projectID, taskID string) string {
	t.Helper()
	tasks, err := s.TaskList(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.ID == taskID {
			return task.Status
		}
	}
	t.Fatalf("task %s not listed", taskID)
	return ""
}

// The lane is where the work may write, not what it is: widening it — which
// the engine itself does when a person approves a lane at accept time — must
// not detach the run that asked for it (B-536).
func TestAWidenedLaneKeepsTheTasksAcceptedRun(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := laneGitProject(t, s)
	for _, f := range []string{"keys.js", "test/legends.cjs"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
		os.WriteFile(filepath.Join(dir, f), []byte("x\n"), 0o644)
	}
	commitPlan(t, dir, laneTaskPlan("legends read base n", "keys.js"))
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeAcceptedTaskRun(t, dir, id, "r-build", "build", taskBodyHashForTask(context.Background(), s, id, "T-004"))
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := taskStatus(t, s, id, "T-004"); got != "accepted" {
		t.Fatalf("before widening: T-004 is %s, want accepted", got)
	}

	if _, err := widenTaskLane(dir, "T-004", []string{"test/legends.cjs"}); err != nil {
		t.Fatal(err)
	}
	if got := taskStatus(t, s, id, "T-004"); got != "accepted" {
		t.Fatalf("after the engine widened the lane: T-004 is %s, want accepted — the widening detached its own run", got)
	}

	// The boundary still holds for a real change of meaning.
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan),
		[]byte(laneTaskPlan("legends are deleted", "keys.js, test/legends.cjs")), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := taskStatus(t, s, id, "T-004"); got == "accepted" {
		t.Fatal("a rewritten task kept the accepted history of its old meaning")
	}
}

// Runs recorded before the fix hashed the Owns line in. T-004's test and
// build runs were each recorded under a lane two widenings later rewrote; no
// spelling of today's body matches them. Recovery finds the body they ran
// against in the plan's history and rebinds them, because it differs from
// today's only in its lane (B-536).
func TestRecoveryRebindsRunsRecordedBeforeTheirLaneWasWidened(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := laneGitProject(t, s)
	legacy := func(substance, owns string) string {
		return rawTaskBodyHash(legacyNormalizeTaskBody(laneTaskBody(substance, owns)))
	}

	commitPlan(t, dir, laneTaskPlan("legends read base n", "keys.js"))
	writeAcceptedTaskRun(t, dir, id, "r-test", "test", legacy("legends read base n", "keys.js"))
	commitPlan(t, dir, laneTaskPlan("legends read base n", "keys.js, test/legends.cjs"))
	writeAcceptedTaskRun(t, dir, id, "r-build", "build", legacy("legends read base n", "keys.js, test/legends.cjs"))
	commitPlan(t, dir, laneTaskPlan("legends read base n", "keys.js, test/legends.cjs, .ducklab/docs/plan.md"))

	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := taskStatus(t, s, id, "T-004"); got != "accepted" {
		t.Fatalf("T-004 is %s after recovery, want accepted", got)
	}

	// Rebinding is in memory: a project that tracks state.json must not
	// find every finished run rewritten by a restart.
	onDisk, err := runlog.ReadState(runlog.RunDirFor(dir, "r-build"))
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.TaskBodyHash != legacy("legends read base n", "keys.js, test/legends.cjs") {
		t.Fatal("recovery rewrote the run's recorded hash on disk")
	}
}

// History rebinds only a lane change. A task id the plan recycled for other
// work keeps its old runs detached, however its lane moved.
func TestRecoveryLeavesRunsOfARewrittenTaskDetached(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := laneGitProject(t, s)

	commitPlan(t, dir, laneTaskPlan("legends read base n", "keys.js"))
	writeAcceptedTaskRun(t, dir, id, "r-build", "build",
		rawTaskBodyHash(legacyNormalizeTaskBody(laneTaskBody("legends read base n", "keys.js"))))
	commitPlan(t, dir, laneTaskPlan("the keypad gains a shift layer", "keys.js, app.js"))

	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := taskStatus(t, s, id, "T-004"); got == "accepted" {
		t.Fatal("a recycled task id inherited the accepted run of its old meaning")
	}
}

// A run that matched through an older spelling is rebound too, so the next
// widening of its lane cannot detach it before the engine restarts again.
func TestRecoveryRebindsLegacySpellingsSoALaterWideningKeepsThem(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := laneGitProject(t, s)
	os.WriteFile(filepath.Join(dir, "keys.js"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "app.js"), []byte("x\n"), 0o644)
	commitPlan(t, dir, laneTaskPlan("legends read base n", "keys.js"))
	writeAcceptedTaskRun(t, dir, id, "r-build", "build",
		rawTaskBodyHash(legacyNormalizeTaskBody(laneTaskBody("legends read base n", "keys.js"))))
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := widenTaskLane(dir, "T-004", []string{"app.js"}); err != nil {
		t.Fatal(err)
	}
	if got := taskStatus(t, s, id, "T-004"); got != "accepted" {
		t.Fatalf("T-004 is %s after a live widening, want accepted", got)
	}
}

// Without git there is no plan history to consult: a run recorded before
// the fix still counts while its task is unchanged, through its legacy
// spelling.
func TestARunRecordedBeforeTheFixCountsInAProjectWithoutGit(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	// An Implements line, so the raw spelling differs from the legacy one.
	body := "**Implements:** SPEC-001\n\n" + laneTaskBody("legends read base n", "keys.js")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-001 — Core\n\n### T-004 — Fix the legends\n\n" + body + "\n",
	})
	writeAcceptedTaskRun(t, dir, id, "r-build", "build", rawTaskBodyHash(legacyNormalizeTaskBody(body)))
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := taskStatus(t, s, id, "T-004"); got != "accepted" {
		t.Fatalf("T-004 is %s, want accepted", got)
	}
}
