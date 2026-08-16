package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// Redoing after a test-first acceptance must not strand the old failing test in
// the tree. The old promise is withdrawn before the new run is launched.
func TestRedoRetiresAnUnbuiltAcceptedTest(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindPlan)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan), []byte("## M-001 — Core\n\n### T-018 — Work\n\nDo it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := vcs.New(dir)
	if err := git.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit("baseline"); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(dir, "stale_test.py")
	if err := os.WriteFile(testFile, []byte("def test_stale(): assert False\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.AddAll(); err != nil {
		t.Fatal(err)
	}
	sha, err := git.Commit("ducklab: T-018 failing test")
	if err != nil {
		t.Fatal(err)
	}
	prior := &runlog.Run{ID: "r-stale", ProjectID: p.ID, TaskID: "T-018", Stage: "test", Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: sha, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	w, err := runlog.NewWriter(dir, prior)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{"verify.mode": "tests", "verify.tests": "true"}); err != nil {
		t.Fatal(err)
	}

	fresh, err := s.TestStart(context.Background(), p.ID, TestFirstRequest{TaskID: "T-018", Redo: true, Note: "the accepted test is stale"})
	if err != nil {
		t.Fatalf("redo was refused: %v", err)
	}
	t.Cleanup(func() { s.RunAbort(context.Background(), fresh.ID); s.waitForRun(context.Background(), fresh.ID) })

	// TestStart returns as soon as the fresh run is queued; cleanup must still
	// happen before that redo can edit the shared tree. Poll the persisted old
	// record rather than making the assertion depend on scheduler timing.
	var got *RunView
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err = s.RunGet(context.Background(), prior.ID)
		if err == nil && got.Run.RevertSHA != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if got.Run.RevertSHA == "" {
		t.Fatal("redo left the unbuilt accepted test without a revert SHA")
	}
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Fatalf("stale test remains after redo cleanup: %v", err)
	}
	if clean, err := git.IsClean(); err != nil || !clean {
		t.Fatalf("redo cleanup left the working tree dirty: %v", err)
	}
	if reason := s.projectHeld(p.ID, "T-018"); reason != "" {
		t.Fatalf("redo cleanup did not release the queue hold: %q", reason)
	}
}

// The redo budget is discovered from the shipped configuration rather than
// baking an arbitrary number into this test. Every redo through the boundary
// is accepted; the next one is refused and identifies the governing limit.
func TestRedoCommitsHaveAFinitePerTaskCap(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}, {"add", "-A"}, {"commit", "-q", "-m", "seed", "--allow-empty"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	prior := &runlog.Run{ID: "r-cap", ProjectID: id, TaskID: "T-001", Stage: "build", Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: "accepted-build", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	w, err := runlog.NewWriter(dir, prior)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	const safety = 100
	count := 0
	for count < safety {
		r, err := s.RunStart(context.Background(), id, RunRequest{TaskID: "T-001", Mode: "solo", Redo: true})
		if err != nil {
			if count == 0 {
				t.Fatalf("redo was refused before any commit: %v", err)
			}
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "redo") || (!strings.Contains(msg, "limit") && !strings.Contains(msg, "cap")) {
				t.Fatalf("redo refusal does not name its limit: %v", err)
			}
			return
		}
		count++
		s.RunAbort(context.Background(), r.ID)
		s.waitForRun(context.Background(), r.ID)
	}
	t.Fatalf("no finite redo limit was enforced after %d redo commits", safety)
}
