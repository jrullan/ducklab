package service

import (
	"context"
	"fmt"
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

// Redo is one safe door for an accepted test-first task: consent must explain
// the reason, the new run must remain a test-first chain, and the old
// acceptance must be attributable rather than silently overwritten.
func TestRedoAcceptedTestFirstRequiresNoteAndAuditsPriorAcceptance(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}, {"add", "-A"}, {"commit", "-q", "-m", "seed", "--allow-empty"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\\n%s", args, err, out)
		}
	}

	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"verify.mode": "tests", "verify.tests": "true"}); err != nil {
		t.Fatal(err)
	}

	prior := &runlog.Run{
		ID: "r-accepted-test", ProjectID: id, TaskID: "T-001", Stage: "test",
		Status: "done", Verdict: "PASSED", Accepted: true,
		CommitSHA: "prior-test-sha-123", StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, prior)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	if _, err := s.TestStart(context.Background(), id, TestFirstRequest{TaskID: "T-001", Redo: true}); err == nil {
		t.Fatal("redo of an accepted test-first task without a note was allowed")
	} else if !strings.Contains(strings.ToLower(err.Error()), "note") {
		t.Fatalf("missing-note refusal does not name the required reason: %v", err)
	}

	run, err := s.TestStart(context.Background(), id, TestFirstRequest{
		TaskID: "T-001", Redo: true, Note: "the accepted test covered the wrong boundary",
		ThenBuild: true, Build: RunRequest{Mode: "solo"},
	})
	if err != nil {
		t.Fatalf("redo with a reason was refused: %v", err)
	}
	t.Cleanup(func() {
		s.RunAbort(context.Background(), run.ID)
		s.waitForRun(context.Background(), run.ID)
	})

	if run.Stage != "test" {
		t.Fatalf("redo started as %q, want a fresh test phase", run.Stage)
	}
	if run.Note != "the accepted test covered the wrong boundary" {
		t.Errorf("run note = %q, want the human redo reason", run.Note)
	}
	if run.ChainBuild == nil {
		t.Fatal("redo lost the chained build configuration")
	}

	// Use the persisted record as the compatibility surface for the new
	// provenance fields: older clients can still inspect the run JSON.
	state, err := os.ReadFile(dir + "/.ducklab/runs/" + run.ID + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"prior-test-sha-123", "the accepted test covered the wrong boundary"} {
		if !strings.Contains(string(state), want) {
			t.Errorf("run state does not record %q: %s", want, state)
		}
	}

	audit, err := os.ReadFile(dir + "/.ducklab/bugs/audit.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	line := string(audit)
	for _, want := range []string{"T-001", "prior-test-sha-123", "the accepted test covered the wrong boundary", "human"} {
		if !strings.Contains(line, want) {
			t.Errorf("redo audit lacks %q: %s", want, line)
		}
	}
	if !strings.Contains(line, "redo") {
		t.Errorf("redo audit does not name its door: %s", line)
	}
}

// Redoing a task whose accepted test was never built must withdraw the stale
// test before opening the replacement, rather than leaving two failing promises
// in the tree and the queue held forever.
func TestRedoRetiresAnUnbuiltAcceptedTest(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	git := vcs.New(dir)
	if err := git.Init(); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, "baseline.txt"), []byte("baseline\n"), 0o644); err != nil { t.Fatal(err) }
	if err := git.AddAll(); err != nil { t.Fatal(err) }
	if _, err := git.Commit("baseline"); err != nil { t.Fatal(err) }
	stale := filepath.Join(dir, "stale_test.py")
	if err := os.WriteFile(stale, []byte("def test_stale(): assert False\n"), 0o644); err != nil { t.Fatal(err) }
	if err := git.AddAll(); err != nil { t.Fatal(err) }
	sha, err := git.Commit("ducklab: T-001 failing test")
	if err != nil { t.Fatal(err) }
	prior := &runlog.Run{ID: "r-stale", ProjectID: id, TaskID: "T-001", Stage: "test", Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: sha, StartedAt: "2026-08-01T00:00:00Z"}
	w, err := runlog.NewWriter(dir, prior); if err != nil { t.Fatal(err) }; w.Close()
	s.RecoverRuns(context.Background())
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"verify.mode": "tests", "verify.tests": "true"}); err != nil { t.Fatal(err) }

	run, err := s.TestStart(context.Background(), id, TestFirstRequest{TaskID: "T-001", Redo: true, Note: "replace the stale test"})
	if err != nil { t.Fatalf("redo was refused: %v", err) }
	s.RunAbort(context.Background(), run.ID); s.waitForRun(context.Background(), run.ID)
	got, err := s.RunGet(context.Background(), "r-stale"); if err != nil { t.Fatal(err) }
	if got.Run.RevertSHA == "" { t.Fatal("redo did not record a revert SHA on the stale test") }
	if _, err := os.Stat(stale); !os.IsNotExist(err) { t.Fatalf("stale test remains after redo cleanup: %v", err) }
	if reason := s.projectHeld(id, "T-019"); reason != "" { t.Fatalf("queue hold remained after redo cleanup: %q", reason) }
}

// Redo is deliberately finite per task. Drive accepted test-first history past
// the configured/default ceiling; the first refusal must identify the ceiling.
func TestRedoCommitCapRefusesAndNamesLimit(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	for i := 0; i < 32; i++ {
		r := &runlog.Run{ID: fmt.Sprintf("r-redo-%d", i), ProjectID: id, TaskID: "T-001", Stage: "test", Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: fmt.Sprintf("sha-%d", i), StartedAt: fmt.Sprintf("2026-08-01T00:00:%02dZ", i)}
		w, err := runlog.NewWriter(dir, r); if err != nil { t.Fatal(err) }; w.Close()
	}
	s.RecoverRuns(context.Background())
	for i := 0; i < 32; i++ {
		r, err := s.TestStart(context.Background(), id, TestFirstRequest{TaskID: "T-001", Redo: true, Note: "bounded retry"})
		if err != nil {
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "limit") && !strings.Contains(msg, "cap") { t.Fatalf("redo refusal does not name its limit: %v", err) }
			return
		}
		s.RunAbort(context.Background(), r.ID); s.waitForRun(context.Background(), r.ID)
	}
	t.Fatal("redo remained unbounded; no per-task cap refusal was observed")
}
