package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
)

// gatedTriage runs a triage of two new bugs to its gate.
func gatedTriage(t *testing.T) (*Service, string, string, string) {
	t.Helper()
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	id, root := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	ctx := context.Background()
	for _, title := range []string{"vertex drag never starts", "edge label overlaps"} {
		if _, err := s.BugAdd(ctx, id, BugRequest{Title: title, Severity: "normal"}); err != nil {
			t.Fatal(err)
		}
	}
	run, err := s.BugTriageWith(ctx, id, BugTriageRequest{Ducklings: []string{"pato-uno"}})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, run.ID)
	waitForStatus(t, s, run.ID, "paused", "gate")
	return s, id, root, run.ID
}

// Review of #176 (P1, 4381761698): the claim checked only that no other
// relaunch had won. An accept that took the original's lock between the
// replacement's prepare and its claim lost to the claim: both calls
// succeeded, the accepted run's resolution became "relaunched as …", and a
// second triage started on bugs whose proposals were already applied.
// Exactly one decision wins; the loser errors, and a losing replacement
// leaves nothing behind.
func TestAnAcceptBetweenPrepareAndClaimWinsAndTheRelaunchLeavesNothing(t *testing.T) {
	s, id, root, original := gatedTriage(t)
	reached, release := make(chan struct{}), make(chan struct{})
	triageRelaunchCrashPoint = func(at string) {
		if at == "before-claim" {
			close(reached)
			<-release
		}
	}
	defer func() { triageRelaunchCrashPoint = func(string) {} }()
	relaunchErr := make(chan error, 1)
	go func() {
		_, err := s.BugTriageWith(context.Background(), id, BugTriageRequest{RelaunchOf: original, Ducklings: []string{"pato-dos"}})
		relaunchErr <- err
	}()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the relaunch never reached its claim")
	}
	if _, err := s.RunAccept(context.Background(), original, ""); err != nil {
		t.Fatalf("the accept that took the lock first must win: %v", err)
	}
	close(release)
	err := <-relaunchErr
	if err == nil || !strings.Contains(err.Error(), "accepted") {
		t.Fatalf("relaunch after the accept = %v, want refused as accepted", err)
	}
	got := mustRun(t, s, original)
	if !got.Accepted || strings.Contains(got.Resolution, "relaunched") {
		t.Fatalf("original after the race: accepted=%v resolution=%q", got.Accepted, got.Resolution)
	}
	if kids := relaunchesOf(t, root, original); len(kids) != 0 {
		t.Fatalf("the losing relaunch left %d records behind", len(kids))
	}
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if kids := relaunchesOf(t, root, original); len(kids) != 0 {
		t.Fatalf("recovery found a resumable orphan of the losing relaunch: %+v", kids)
	}
}

// The reverse order: once the relaunch claimed the original, no later
// decision — accept, reject, abort — may consume it again.
func TestNoDecisionLandsOnATriageAfterItsRelaunchClaimedIt(t *testing.T) {
	s, id, root, original := gatedTriage(t)
	again, err := s.BugTriageWith(context.Background(), id, BugTriageRequest{RelaunchOf: original, Ducklings: []string{"pato-dos"}})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, again.ID)
	ctx := context.Background()
	if _, err := s.RunAccept(ctx, original, ""); err == nil {
		t.Fatal("an accept landed on a relaunched triage")
	}
	if err := s.RunReject(ctx, original, "late"); err == nil {
		t.Fatal("a reject landed on a relaunched triage")
	}
	if err := s.RunAbort(ctx, original); err == nil {
		t.Fatal("an abort landed on a relaunched triage")
	}
	got := mustDisk(t, root, original)
	if got.Accepted || got.Verdict != "ABORTED" || !strings.Contains(got.Resolution, "relaunched as "+again.ID) {
		t.Fatalf("original after late decisions = accepted=%v %s %q", got.Accepted, got.Verdict, got.Resolution)
	}
	waitForStatus(t, s, again.ID, "paused", "gate")
}

// Every other decision that consumed the original refuses the claim too.
func TestARelaunchOfADecidedTriageIsRefused(t *testing.T) {
	for _, decide := range []string{"reject", "abort"} {
		t.Run(decide, func(t *testing.T) {
			s, id, root, original := gatedTriage(t)
			ctx := context.Background()
			var err error
			if decide == "reject" {
				err = s.RunReject(ctx, original, "no")
			} else {
				err = s.RunAbort(ctx, original)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.BugTriageWith(ctx, id, BugTriageRequest{RelaunchOf: original, Ducklings: []string{"pato-dos"}}); err == nil {
				t.Fatalf("a %sed triage was relaunched", decide)
			}
			if kids := relaunchesOf(t, root, original); len(kids) != 0 {
				t.Fatalf("the refused relaunch left %d records", len(kids))
			}
		})
	}
}
