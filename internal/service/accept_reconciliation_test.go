package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

func TestConcurrentAcceptsSettleOnceAndReturnTheSameCommit(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-double-accept")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "accepted.txt"), []byte("accepted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Queue both client calls behind the run's decision boundary, then release
	// them together. If RunAccept does not participate in this lock, either call
	// can mutate the worktree while the test still owns it.
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	if rs == nil {
		t.Fatal("recovered run is absent")
	}
	rs.decisionMu.Lock()

	type outcome struct {
		result *AcceptResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	accept := func() {
		result, err := s.RunAccept(context.Background(), run.ID, "")
		outcomes <- outcome{result: result, err: err}
	}
	go accept()
	go accept()
	select {
	case got := <-outcomes:
		rs.decisionMu.Unlock()
		t.Fatalf("accept bypassed the run decision lock: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	rs.decisionMu.Unlock()

	first, second := <-outcomes, <-outcomes
	for i, got := range []outcome{first, second} {
		if got.err != nil {
			t.Fatalf("accept %d failed: %v", i+1, got.err)
		}
		if got.result == nil || got.result.CommitSHA == "" {
			t.Fatalf("accept %d result = %+v", i+1, got.result)
		}
	}
	if first.result.CommitSHA != second.result.CommitSHA {
		t.Fatalf("accepts returned different commits: %s and %s", first.result.CommitSHA, second.result.CommitSHA)
	}
	if !strings.Contains(first.result.Info+second.result.Info, "already accepted") {
		t.Fatalf("neither result identified the idempotent decision: %#v / %#v", first.result, second.result)
	}

	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status != "done" || !detail.Run.Accepted || detail.Run.PendingKind != "" {
		t.Fatalf("settled run = status %q accepted %v pending %q", detail.Run.Status, detail.Run.Accepted, detail.Run.PendingKind)
	}
	accepts := 0
	for _, event := range detail.Events {
		if event.Type == "human" && event.Data["action"] == "accept" {
			accepts++
		}
		if event.Type == "accept_refused" {
			t.Fatalf("idempotent accept was recorded as a refusal: %+v", event.Data)
		}
	}
	if accepts != 1 {
		t.Fatalf("accept decisions recorded = %d, want exactly one", accepts)
	}
}

func TestRecoverRunsRepairsAcceptedCommitOnDefault(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "accepted-recovery")
	entry, err := s.registry.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	landingGit(t, entry.Path, "init")
	landingGit(t, entry.Path, "config", "user.name", "test")
	landingGit(t, entry.Path, "config", "user.email", "test@test")
	landingGit(t, entry.Path, "commit", "--allow-empty", "-m", "accepted work")
	sha := landingGit(t, entry.Path, "rev-parse", "HEAD")

	run := &runlog.Run{
		ID: "r-stale-accepted", ProjectID: projectID, TaskID: "T-052", Stage: "build",
		Status: "paused", Verdict: "PASSED", Accepted: true, CommitSHA: sha,
		Resolution: "accepted by human", PendingKind: "gate",
		PendingSince: time.Now().UTC().Format(time.RFC3339),
		PendingData:  map[string]interface{}{"detail": "rebase failed after the commit landed"},
		StartedAt:    time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(entry.Path, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status != "done" || detail.Run.PendingKind != "" || len(detail.Run.Next) != 0 {
		t.Fatalf("recovered run = status %q pending %q next %v", detail.Run.Status, detail.Run.PendingKind, detail.Run.Next)
	}
	if detail.Run.EndedAt == "" {
		t.Fatal("recovered accepted run has no end time")
	}
	// Recreate the historical split after startup. Every decision endpoint must
	// perform the same reconciliation, not rely solely on engine recovery.
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	rs.run.Status = "paused"
	rs.run.PendingKind = "gate"
	rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
	rs.run.PendingData = map[string]interface{}{"detail": "late client rewrote the accepted run"}
	writer, err := s.ensureWriter(rs)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteState(); err != nil {
		t.Fatal(err)
	}
	if err := s.RunReject(context.Background(), run.ID, "reject stale button"); err == nil || !strings.Contains(err.Error(), "nothing to reject") {
		t.Fatalf("reject accepted run error = %v, want nothing to reject", err)
	}
	persisted, err := runlog.ReadState(runlog.RunDirFor(entry.Path, run.ID))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "done" || persisted.PendingKind != "" || !persisted.Accepted {
		t.Fatalf("persisted repair = status %q pending %q accepted %v", persisted.Status, persisted.PendingKind, persisted.Accepted)
	}
}

func TestAcceptedRunNeverOffersAcceptOrReject(t *testing.T) {
	run := &runlog.Run{
		Stage: "build", Status: "paused", PendingKind: "gate", Verdict: "PASSED",
		Accepted: true, CommitSHA: "abc123",
	}
	if got := runNext(run); len(got) != 0 {
		t.Fatalf("accepted stale run next = %v, want no decision actions", got)
	}
}
