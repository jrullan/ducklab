package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

type engineDeath struct{}

// crashRevisionAt starts a revision of parent and lets the engine "die" at
// the named boundary: the launch stops there with whatever is on disk, and
// nothing after it runs — no cleanup, no registration, no queue.
func crashRevisionAt(t *testing.T, s *Service, projectID, parent, point string) {
	t.Helper()
	revisionCrashPoint = func(at string) {
		if at == point {
			panic(engineDeath{})
		}
	}
	defer func() { revisionCrashPoint = func(string) {} }()
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(engineDeath); !ok {
					panic(r)
				}
			}
		}()
		_, _ = s.StageStart(context.Background(), projectID, StageRequest{Stage: "intake", RevisesRun: parent, Revise: "fix it"})
		t.Fatalf("the launch did not reach %s", point)
	}()
}

// childrenOf lists the recorded runs, on disk, that revise parent.
func childrenOf(t *testing.T, root, parent string) []*runlog.Run {
	t.Helper()
	ids, err := runlog.ListRuns(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []*runlog.Run
	for _, id := range ids {
		if run, err := runlog.ReadState(runlog.RunDirFor(root, id)); err == nil && run.RevisesRun == parent {
			out = append(out, run)
		}
	}
	return out
}

// Review of #175 (P1, 4243343843): the engine dies after the revision's
// record exists but before it claims its parent. Recovery used to turn that
// child into a resumable paused run while the parent stayed revisable, so a
// second request yielded two live revisions of one gate. The child is
// written prepared; recovery discards one its parent does not name.
func TestACrashBeforeTheClaimLeavesExactlyOneRevisionAfterRecovery(t *testing.T) {
	s, projectID, root, _ := revisionChainProject(t)
	r0 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Mode: "solo", From: "Fill the gap."})

	crashRevisionAt(t, s, projectID, r0.ID, "before-claim")
	orphans := childrenOf(t, root, r0.ID)
	if len(orphans) != 1 || orphans[0].Status != runStatusPrepared {
		t.Fatalf("pre-claim record = %+v, want one prepared child", orphans)
	}
	orphan := orphans[0].ID

	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runlog.RunDirFor(root, orphan)); !os.IsNotExist(err) {
		t.Fatalf("the unclaimed child survived recovery: %v", err)
	}
	if _, err := s.RunGet(context.Background(), orphan); err == nil {
		t.Fatal("recovery registered the unclaimed child")
	}
	parentStillAnswerable(t, s, r0.ID)

	child := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", RevisesRun: r0.ID, Revise: "fix it"})
	if kids := childrenOf(t, root, r0.ID); len(kids) != 1 || kids[0].ID != child.ID {
		t.Fatalf("revisions of %s after recovery = %d, want only %s", r0.ID, len(kids), child.ID)
	}
	if child.Revision != 1 {
		t.Fatalf("revision number = %d, want 1", child.Revision)
	}
}

// The engine dies after the claim but before the child is marked running:
// the parent names it, so recovery finalizes it as a resumable run and the
// parent cannot be revised again.
func TestACrashAfterTheClaimFinalizesTheRevisionForResume(t *testing.T) {
	s, projectID, root, _ := revisionChainProject(t)
	r0 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Mode: "solo", From: "Fill the gap."})

	crashRevisionAt(t, s, projectID, r0.ID, "after-claim")
	kids := childrenOf(t, root, r0.ID)
	if len(kids) != 1 || kids[0].Status != runStatusPrepared {
		t.Fatalf("post-claim record = %+v, want one prepared child", kids)
	}
	child := kids[0].ID

	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	detail, err := s.RunGet(context.Background(), child)
	if err != nil {
		t.Fatalf("the claimed child was lost: %v", err)
	}
	if detail.Run.Status != "paused" || detail.Run.PendingKind != "engine_restart" || !slices.Contains(runNext(detail.Run), "resume") {
		t.Fatalf("claimed child after recovery = %s/%s next %v, want resumable", detail.Run.Status, detail.Run.PendingKind, runNext(detail.Run))
	}
	if _, err := s.StageStart(context.Background(), projectID, StageRequest{Stage: "intake", RevisesRun: r0.ID, Revise: "again"}); err == nil {
		t.Fatal("the claimed parent was revised a second time")
	}
	if _, err := os.Stat(filepath.Join(runlog.RunDirFor(root, child), stageRequestFile)); err != nil {
		t.Fatalf("the finalized child cannot resume without its request: %v", err)
	}
	// Review of #176: a child recovered before it ever ran resumed with an
	// all-zero Limit, which budget reads as unlimited. Its record carries the
	// project's ceilings from birth, and resume keeps them.
	assertProjectLimits(t, s, projectID, mustDisk(t, root, child), "recovered revision record")
	cleanupStartedRun(t, s, child)
	if _, err := s.RunResume(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	assertProjectLimits(t, s, projectID, waitForStatus(t, s, child, "paused", "gate"), "resumed revision")
}
