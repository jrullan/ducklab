package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// gitProject turns a bare test dir into a repo with one committed file.
func gitProject(t *testing.T, dir string) *vcs.Git {
	t.Helper()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	g := vcs.New(dir)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".ducklab/runs/\n.ducklab/ducklab.db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("init"); err != nil {
		t.Fatal(err)
	}
	return g
}

// Reject meant "no, but keep everything anyway": the record said FAILED while
// the half-made edits stayed in the tree. The next attempt of the same task
// found them and said, in its thinking, that somebody had already fixed it —
// and the person watching knew nobody had accepted anything.
func TestRejectPutsTheTreeBack(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	g := gitProject(t, dir)

	snap, err := g.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-1", ProjectID: id, TaskID: "T-001", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		TreeSnapshot: snap,
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	// The run's half-made work, sitting uncommitted.
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("half-made fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.RunReject(context.Background(), "r-1", "not what I asked for"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original\n" {
		t.Errorf("the rejected run's edits are still in the tree: %q", got)
	}
}

// A reject cleans up a live shared tree, but it is never allowed to make the
// worktree older than HEAD. Landed commits stand; the run's own residue is
// what gets undone (B-074 made this surgical — the old outright refusal left
// a gated run reject-undecidable the moment anyone else committed).
func TestRejectRestoresAroundCommitsLandedSinceSnapshot(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	g := gitProject(t, dir)

	snap, err := g.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-head-advanced", ProjectID: id, TaskID: "T-001", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		TreeSnapshot: snap, TreeSnapshotHead: mustHead(t, g), StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	// These are the three commits landed while this run was awaiting a decision.
	// The first represents its legitimate work being landed by hand; the other
	// two make the reported commit count observable to the operator.
	landed := map[string]string{
		"index.html":                  "legitimate run work landed by hand\n",
		"committed-after-run-test.go": "package project\n",
		"classifier-fix.go":           "package project\n",
	}
	for path, contents := range landed {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := g.Add(path); err != nil {
			t.Fatal(err)
		}
		if _, err := g.Commit("land " + path); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.RunReject(context.Background(), run.ID, "not what I asked for"); err != nil {
		t.Fatalf("reject refused despite nothing of the run's left to rewind: %v", err)
	}
	for path, want := range landed {
		got, readErr := os.ReadFile(filepath.Join(dir, path))
		if readErr != nil || string(got) != want {
			t.Errorf("reject changed committed %s: got %q, err %v; want %q", path, got, readErr, want)
		}
	}

	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status != "done" || detail.Run.Verdict != "FAILED" {
		t.Errorf("reject did not close the run: status=%q verdict=%q", detail.Run.Status, detail.Run.Verdict)
	}
}

func mustHead(t *testing.T, g *vcs.Git) string {
	t.Helper()
	head, err := g.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// A run's snapshot is custody for its recorded worktree, never permission to
// rewind a same-named file in the registered human checkout.
func TestWorktreeRestoreDoesNotRestoreSameNamedHumanFile(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	mainGit := gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-worktree-snapshot-root")

	worktreeSnapshot, err := vcs.New(run.WorktreePath).SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	if rs == nil {
		t.Fatal("recovered worktree run is absent")
	}
	rs.run.TreeSnapshot = worktreeSnapshot
	rs.run.TreeSnapshotHead = mustHead(t, mainGit)

	// The model's run-local edit has the same path as a person changing their
	// registered checkout while the run waits at its gate.
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "index.html"), []byte("run worktree edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("human checkout edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := restoreAfterUnaccepted(rs); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "human checkout edit\n" {
		t.Errorf("worktree cleanup restored the registered checkout from the run snapshot: got %q", got)
	}
}

func TestAcceptDoesNotRestore(t *testing.T) {
	rs := &runState{run: &runlog.Run{ID: "r-2", TreeSnapshot: "abc", Accepted: true}}
	// Must be a no-op — reaching for git with a fake path would error loudly.
	restoreAfterUnaccepted(rs)
}

// Aborting is the cleanup path for a build that stopped before acceptance. It
// must restore both kinds of model residue, including when no run goroutine is
// left to unwind (the paused case), so the next test-first/retire action sees
// the same tree the build started with.
func TestAbortRestoresPausedBuildTreeAndAllowsCleanRecovery(t *testing.T) {
	for _, status := range []string{"paused", "failed"} {
		t.Run(status, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			id, dir := projectWithDocs(t, s, nil)
			g := gitProject(t, dir)

			snap, err := g.SnapshotTree()
			if err != nil {
				t.Fatal(err)
			}
			run := &runlog.Run{
				ID: "r-abort-" + status, ProjectID: id, TaskID: "T-001", Stage: "build",
				Status: status, Verdict: "PASSED", PendingKind: "gate",
				TreeSnapshot: snap, StartedAt: time.Now().UTC().Format(time.RFC3339),
			}
			w, err := runlog.NewWriter(dir, run)
			if err != nil {
				t.Fatal(err)
			}
			w.Close()
			s.RecoverRuns(context.Background())

			if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("model edit\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			created := filepath.Join(dir, "created-by-model.txt")
			if err := os.WriteFile(created, []byte("model file\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			if err := s.RunAbort(context.Background(), run.ID); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(dir, "index.html")); err != nil || string(got) != "original\n" {
				t.Errorf("tracked model edit survived abort: %q, %v", got, err)
			}
			if _, err := os.Stat(created); !os.IsNotExist(err) {
				t.Errorf("untracked model file survived abort: %v", err)
			}
			if dirty := g.DirtyPaths(); len(dirty) != 0 {
				for _, path := range dirty {
					if path != ".ducklab" && !strings.HasPrefix(path, ".ducklab/") {
						t.Errorf("abort left task files dirty: %v", dirty)
						break
					}
				}
			}
		})
	}
}
