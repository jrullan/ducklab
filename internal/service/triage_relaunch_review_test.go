package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// pausedTriage launches a triage of the first n of three new bugs and lets it
// cross the history threshold, so it pauses after its first bug — the run a
// person would relaunch from the escalation card.
func pausedTriage(t *testing.T, n int) (*Service, string, string, []string, *runState) {
	t.Helper()
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	id, root := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	ctx := context.Background()
	var ids []string
	for _, title := range []string{"vertex drag never starts", "edge label overlaps", "angle input loses focus"} {
		b, err := s.BugAdd(ctx, id, BugRequest{Title: title, Severity: "normal"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
	}
	seedTriageHistory(s, id, "pato-dos")
	l, err := s.prepareTriage(ctx, id, BugTriageRequest{BugIDs: ids[:n], Ducklings: []string{"pato-uno"}})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, l.rs.run.ID)
	overThreshold(l.rs)
	s.executeTriage(l.ctx, l.rs, l.root, l.todo, l.all, nil)
	if st := l.rs.snapshotRun(); st.Status != "paused" || st.PendingKind != "history_duration" {
		t.Fatalf("setup: triage did not pause: %s/%s", st.Status, st.PendingKind)
	}
	return s, id, root, ids, l.rs
}

func relaunchesOf(t *testing.T, root, original string) []*runlog.Run {
	t.Helper()
	ids, err := runlog.ListRuns(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []*runlog.Run
	for _, id := range ids {
		if run, err := runlog.ReadState(runlog.RunDirFor(root, id)); err == nil && run.RelaunchOf == original {
			out = append(out, run)
		}
	}
	return out
}

func originalUntouched(t *testing.T, s *Service, root, id string) {
	t.Helper()
	for _, run := range []*runlog.Run{mustRun(t, s, id), mustDisk(t, root, id)} {
		if run.Status != "paused" || run.PendingKind != "history_duration" || run.Verdict != "" || run.Resolution != "" {
			t.Fatalf("the original triage was consumed: %s/%s verdict=%q resolution=%q", run.Status, run.PendingKind, run.Verdict, run.Resolution)
		}
	}
}

func mustRun(t *testing.T, s *Service, id string) *runlog.Run {
	t.Helper()
	d, err := s.RunGet(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d.Run
}

func mustDisk(t *testing.T, root, id string) *runlog.Run {
	t.Helper()
	run, err := runlog.ReadState(runlog.RunDirFor(root, id))
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// Review of #176 (P1, 4381335984): the relaunch ended the original before
// the replacement's record existed. With the runs directory refusing the new
// record, the call failed, no replacement appeared, and the paused triage was
// destroyed. The original is now consumed only by a durable replacement.
func TestARelaunchWhoseReplacementCannotBeWrittenLeavesTheOriginal(t *testing.T) {
	s, id, root, _, original := pausedTriage(t, 2)
	runs := filepath.Join(root, ".ducklab", "runs")
	if err := os.Chmod(runs, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(runs, 0o755) })
	if _, err := s.BugTriageWith(context.Background(), id, BugTriageRequest{RelaunchOf: original.run.ID, Ducklings: []string{"pato-dos"}}); err == nil {
		t.Fatal("a relaunch with no record to write was accepted")
	}
	if err := os.Chmod(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	originalUntouched(t, s, root, original.run.ID)
	if got := relaunchesOf(t, root, original.run.ID); len(got) != 0 {
		t.Fatalf("a failed relaunch left %d replacement records", len(got))
	}
}

// crashRelaunchAt lets the engine "die" at a relaunch boundary: nothing after
// the point runs — no cleanup, no registration, no worker.
func crashRelaunchAt(t *testing.T, s *Service, projectID, original, point string) {
	t.Helper()
	triageRelaunchCrashPoint = func(at string) {
		if at == point {
			panic(engineDeath{})
		}
	}
	defer func() { triageRelaunchCrashPoint = func(string) {} }()
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(engineDeath); !ok {
					panic(r)
				}
			}
		}()
		_, _ = s.BugTriageWith(context.Background(), projectID, BugTriageRequest{RelaunchOf: original, Ducklings: []string{"pato-dos"}})
		t.Fatalf("the relaunch did not reach %s", point)
	}()
}

func TestACrashBeforeTheRelaunchClaimLeavesOnlyTheOriginal(t *testing.T) {
	s, id, root, _, original := pausedTriage(t, 2)
	crashRelaunchAt(t, s, id, original.run.ID, "before-claim")
	orphans := relaunchesOf(t, root, original.run.ID)
	if len(orphans) != 1 || orphans[0].Status != runStatusPrepared {
		t.Fatalf("pre-claim record = %+v, want one prepared replacement", orphans)
	}
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := relaunchesOf(t, root, original.run.ID); len(got) != 0 {
		t.Fatalf("an unclaimed replacement survived recovery as a second triage of the same bugs: %+v", got)
	}
	if _, err := s.RunGet(context.Background(), orphans[0].ID); err == nil {
		t.Fatal("recovery registered the unclaimed replacement")
	}
	originalUntouched(t, s, root, original.run.ID)
}

func TestACrashAfterTheRelaunchClaimFinalizesTheReplacementForResume(t *testing.T) {
	s, id, root, ids, original := pausedTriage(t, 2)
	crashRelaunchAt(t, s, id, original.run.ID, "after-claim")
	kids := relaunchesOf(t, root, original.run.ID)
	if len(kids) != 1 || kids[0].Status != runStatusPrepared {
		t.Fatalf("post-claim record = %+v, want one prepared replacement", kids)
	}
	replacement := kids[0].ID
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mustDisk(t, root, original.run.ID); got.Status != "failed" || got.Verdict != "ABORTED" || !strings.Contains(got.Resolution, replacement) {
		t.Fatalf("original after the claim = %s/%s %q, want ended and naming %s", got.Status, got.Verdict, got.Resolution, replacement)
	}
	run := mustRun(t, s, replacement)
	if run.Status != "paused" || !slices.Contains(runNext(run), "resume") {
		t.Fatalf("claimed replacement after recovery = %s/%s next %v, want resumable", run.Status, run.PendingKind, runNext(run))
	}
	if _, err := s.BugTriageWith(context.Background(), id, BugTriageRequest{RelaunchOf: original.run.ID, Ducklings: []string{"pato-dos"}}); err == nil {
		t.Fatal("the claimed original was relaunched a second time")
	}
	cleanupStartedRun(t, s, replacement)
	if _, err := s.RunResume(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, replacement, "paused", "gate")
	if got := triagedBugs(runEvents522(t, s, replacement)); !slices.Equal(got, ids[:2]) {
		t.Fatalf("the finalized replacement triaged %v, want %v", got, ids[:2])
	}
}

// Review of #176 (P2, 4381335996): explicit ids beside relaunch_of won
// silently — relaunching B-001's triage with bug_id=B-002 ended B-001's run
// and triaged B-002.
func TestARelaunchRefusesBugsThatAreNotItsOwn(t *testing.T) {
	s, id, root, ids, original := pausedTriage(t, 2)
	ctx := context.Background()
	for _, req := range []BugTriageRequest{
		{RelaunchOf: original.run.ID, BugID: ids[2], Ducklings: []string{"pato-dos"}},
		{RelaunchOf: original.run.ID, BugIDs: []string{ids[0]}, Ducklings: []string{"pato-dos"}},
		{RelaunchOf: original.run.ID, BugIDs: ids, Ducklings: []string{"pato-dos"}},
	} {
		if _, err := s.BugTriageWith(ctx, id, req); err == nil {
			t.Fatalf("relaunch with other bugs %v/%v was accepted", req.BugID, req.BugIDs)
		}
		originalUntouched(t, s, root, original.run.ID)
	}
	// Naming exactly its own bugs, in any order, is the same relaunch.
	again, err := s.BugTriageWith(ctx, id, BugTriageRequest{RelaunchOf: original.run.ID, BugIDs: []string{ids[1], ids[0]}, Ducklings: []string{"pato-dos"}})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, again.ID)
	waitForStatus(t, s, again.ID, "paused", "gate")
}

// Review of #176 (P1, 4381336004): resuming a triage built a fresh tracker,
// so the spent ledger went back to zero and the default ceilings came back —
// resume doubled the budget and undid a lift.
func TestAResumedTriageKeepsItsLedgerAndCeilings(t *testing.T) {
	s, _, _, _, original := pausedTriage(t, 3)
	ctx := context.Background()
	// What the first segment spent, as the live ledger holds it, and a
	// ceiling the person lifted while the run waited.
	original.tracker.Spend.AddTokens(1234)
	original.tracker.Spend.AddUSD(0.25)
	before := mustRun(t, s, original.run.ID)
	if _, err := s.RunBudgetLift(ctx, original.run.ID, "tokens"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunResume(ctx, original.run.ID); err != nil {
		t.Fatal(err)
	}
	run := waitForStatus(t, s, original.run.ID, "paused", "gate")
	if run.Budget.Tokens <= before.Budget.Tokens || run.Budget.Turns <= before.Budget.Turns || run.Budget.USD < before.Budget.USD {
		t.Fatalf("resume restarted the ledger: before tokens=%d turns=%d usd=%v, after tokens=%d turns=%d usd=%v",
			before.Budget.Tokens, before.Budget.Turns, before.Budget.USD, run.Budget.Tokens, run.Budget.Turns, run.Budget.USD)
	}
	if run.Budget.Limit.Tokens != 0 {
		t.Fatalf("resume put back the token ceiling the person lifted: %+v", run.Budget.Limit)
	}
}

// The claim itself can fail to persist: the original is then put back as it
// was, and the replacement is discarded — never an ended original with no
// successor.
func TestARelaunchWhoseClaimCannotPersistRestoresTheOriginal(t *testing.T) {
	s, id, root, _, original := pausedTriage(t, 2)
	dir := runlog.RunDirFor(root, original.run.ID)
	// The writer is open, as it is on a run a person is watching: the claim
	// gets as far as ending the run in memory before its write fails.
	if _, err := s.ensureWriter(original); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, err := s.BugTriageWith(context.Background(), id, BugTriageRequest{RelaunchOf: original.run.ID, Ducklings: []string{"pato-dos"}})
	if err == nil {
		t.Fatal("a relaunch whose claim did not persist was accepted")
	}
	t.Logf("refused: %v", err)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	originalUntouched(t, s, root, original.run.ID)
	if got := relaunchesOf(t, root, original.run.ID); len(got) != 0 {
		t.Fatalf("a refused claim left %d replacement records", len(got))
	}
	if !slices.Contains(runNext(mustRun(t, s, original.run.ID)), "resume") {
		t.Fatal("the restored original cannot be resumed")
	}
}

// The same class on the other resume door without a strategy: a chat whose
// engine restarted rebuilt its tracker from defaults at the next message.
func TestAChatAfterARestartKeepsItsLedger(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: t.TempDir(), Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.ChatStart(context.Background(), p.ID, ChatStartRequest{Duckling: "pato-dos", AboutKind: "project", AboutID: p.ID, Message: "what is here?"})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, run.ID)
	waitForChatPause(t, s, run.ID)
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	// What a restart leaves: the record, and no tracker in memory.
	rs.wmu.Lock()
	rs.tracker = nil
	rs.run.Budget.Tokens, rs.run.Budget.Turns = 5000, 7
	rs.run.Budget.Limit.Tokens = 0
	rs.run.Budget.Limit.Turns = 400
	rs.wmu.Unlock()
	if _, err := s.ChatSend(context.Background(), run.ID, "and then?"); err != nil {
		t.Fatal(err)
	}
	waitForChatPause(t, s, run.ID)
	got := rs.snapshotRun()
	if got.Budget.Tokens < 5000 || got.Budget.Turns < 7 {
		t.Fatalf("the chat's ledger restarted: tokens=%d turns=%d", got.Budget.Tokens, got.Budget.Turns)
	}
	if got.Budget.Limit.Turns != 400 {
		t.Fatalf("the chat's recorded ceiling was replaced: %+v", got.Budget.Limit)
	}
}
