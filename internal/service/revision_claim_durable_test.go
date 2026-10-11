package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission-based failure injection needs a non-root user")
	}
}

// parentStillAnswerable checks the parent kept its decision: paused at its
// gate, offering request_changes, with no revision named.
func parentStillAnswerable(t *testing.T, s *Service, id string) {
	t.Helper()
	detail, err := s.RunGet(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	r := detail.Run
	if r.Status != "paused" || r.PendingKind != "gate" || !slices.Contains(runNext(r), "request_changes") || revisedByPattern.MatchString(r.Resolution) {
		t.Fatalf("parent %s was consumed: %s/%s next %v resolution %q", id, r.Status, r.PendingKind, runNext(r), r.Resolution)
	}
}

func runRecordCount(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".ducklab", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// retryOnce: after the failure is cleared, the revision starts exactly once.
func retryOnce(t *testing.T, s *Service, projectID, parent string) {
	t.Helper()
	child := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", RevisesRun: parent, Revise: "fix it"})
	if child.RevisesRun != parent {
		t.Fatalf("retry revises %q, want %s", child.RevisesRun, parent)
	}
	if _, err := s.StageStart(context.Background(), projectID, StageRequest{Stage: "intake", RevisesRun: parent, Revise: "fix it"}); err == nil {
		t.Fatal("the parent was revised twice")
	}
}

// Review of #175 (P1, 4242862477): the parent's log cannot be reopened, so
// superseding it cannot be recorded. The claim used to fail silently and the
// revision started while the parent stayed revisable.
func TestARevisionWhoseParentCannotBeClaimedDoesNotStart(t *testing.T) {
	skipIfRoot(t)
	s, projectID, root, _ := revisionChainProject(t)
	r0 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Mode: "solo", From: "Fill the gap."})
	s.runsMu.RLock()
	rs := s.runs[r0.ID]
	s.runsMu.RUnlock()
	rs.wmu.Lock()
	if rs.writer != nil {
		_ = rs.writer.Close()
	}
	rs.wmu.Unlock()
	events := filepath.Join(root, ".ducklab", "runs", r0.ID, "events.jsonl")
	if err := os.Chmod(events, 0o444); err != nil {
		t.Fatal(err)
	}
	before := runRecordCount(t, root)

	if _, err := s.StageStart(context.Background(), projectID, StageRequest{Stage: "intake", RevisesRun: r0.ID, Revise: "fix it"}); err == nil {
		t.Fatal("a revision started although its parent could not be claimed")
	}
	parentStillAnswerable(t, s, r0.ID)
	if after := runRecordCount(t, root); after != before {
		t.Fatalf("the refused revision left %d run record(s)", after-before)
	}

	if err := os.Chmod(events, 0o644); err != nil {
		t.Fatal(err)
	}
	retryOnce(t, s, projectID, r0.ID)
}

// The parent's state cannot be written although its log is open: the
// in-memory resolution is rolled back with the failed write.
func TestAParentWhoseStateCannotBeWrittenIsNotConsumed(t *testing.T) {
	skipIfRoot(t)
	s, projectID, root, _ := revisionChainProject(t)
	r0 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Mode: "solo", From: "Fill the gap."})
	dir := filepath.Join(root, ".ducklab", "runs", r0.ID)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := s.StageStart(context.Background(), projectID, StageRequest{Stage: "intake", RevisesRun: r0.ID, Revise: "fix it"}); err == nil {
		t.Fatal("a revision started although its parent's state could not be written")
	}
	parentStillAnswerable(t, s, r0.ID)

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	retryOnce(t, s, projectID, r0.ID)
}

// Review of #175 (P1, 4242862547): the revision's own record cannot be
// created. The claim used to happen first, leaving the parent "revised by" a
// run that never existed.
func TestARevisionThatCannotCreateItsRunLeavesTheParentAnswerable(t *testing.T) {
	skipIfRoot(t)
	s, projectID, root, _ := revisionChainProject(t)
	r0 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Mode: "solo", From: "Fill the gap."})
	runs := filepath.Join(root, ".ducklab", "runs")
	if err := os.Chmod(runs, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(runs, 0o755) })

	if _, err := s.StageStart(context.Background(), projectID, StageRequest{Stage: "intake", RevisesRun: r0.ID, Revise: "fix it"}); err == nil {
		t.Fatal("a revision started without a run record")
	}
	parentStillAnswerable(t, s, r0.ID)

	if err := os.Chmod(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	retryOnce(t, s, projectID, r0.ID)
}
