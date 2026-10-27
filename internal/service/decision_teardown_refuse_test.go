package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// heldRun leaves a triage run paused with its worker held in teardown — after
// publishing its pause, before closing its log. For "resume" the pause is a
// history-duration safe point (resumable); for the gate doors it is the gate.
// release lets the worker finish.
func heldRun(t *testing.T, door string) (s *Service, root, runID string, release func()) {
	t.Helper()
	s = serviceWithDucklings(t, "pato-uno", "pato-dos")
	id, root := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	ctx := context.Background()
	for _, title := range []string{"vertex drag never starts", "edge label overlaps"} {
		if _, err := s.BugAdd(ctx, id, BugRequest{Title: title, Severity: "normal"}); err != nil {
			t.Fatal(err)
		}
	}
	held, gate := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	workerTeardown = func(*runState) {
		once.Do(func() { close(held) })
		<-gate
	}
	release = func() { releaseOnce.Do(func() { close(gate) }) }
	t.Cleanup(func() { workerTeardown = func(*runState) {} })
	if door == "resume" {
		seedTriageHistory(s, id, "pato-dos")
		l, err := s.prepareTriage(ctx, id, BugTriageRequest{Ducklings: []string{"pato-uno"}})
		if err != nil {
			t.Fatal(err)
		}
		runID = l.rs.run.ID
		overThreshold(l.rs)
		go s.executeTriage(l.ctx, l.rs, l.root, l.todo, l.all, nil)
	} else {
		run, err := s.BugTriage(ctx, id, "")
		if err != nil {
			t.Fatal(err)
		}
		runID = run.ID
	}
	cleanupStartedRun(t, s, runID)
	// Registered after the run's cleanup so it runs first: the cleanup's
	// abort waits for workers, and a held worker must be let go before it.
	t.Cleanup(release)
	select {
	case <-held:
	case <-time.After(20 * time.Second):
		t.Fatal("the worker never reached its teardown")
	}
	return s, root, runID, release
}

func runDoor(s *Service, ctx context.Context, door, runID string) error {
	switch door {
	case "resume":
		_, err := s.RunResume(ctx, runID)
		return err
	case "accept":
		_, err := s.RunAccept(ctx, runID, "")
		return err
	case "reject":
		return s.RunReject(ctx, runID, "no")
	default:
		return s.RunAbort(ctx, runID)
	}
}

// Review of #176 (4387975214): settleWorker's wait ended on a cancelled
// request or its bound as if the worker had finished. A resume then started a
// second worker while the first still owned the log, and the first's
// teardown could close the log under it. While the worker is alive every door
// refuses with a retryable error, having written nothing; once it has
// returned the same call goes through.
func TestADoorRefusesWhileThePausedWorkerIsStillFinishing(t *testing.T) {
	for _, how := range []string{"cancelled", "bound"} {
		for _, door := range []string{"resume", "accept", "reject", "abort"} {
			t.Run(how+"/"+door, func(t *testing.T) {
				s, root, runID, release := heldRun(t, door)
				want := "gate"
				if door == "resume" {
					want = "history_duration"
				}
				if got := mustRun(t, s, runID); got.Status != "paused" || got.PendingKind != want {
					t.Fatalf("setup: held worker published %s/%s, want paused/%s", got.Status, got.PendingKind, want)
				}
				dir := runlog.RunDirFor(root, runID)
				stateBefore, _ := os.ReadFile(filepath.Join(dir, "state.json"))
				eventsBefore, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))

				ctx := context.Background()
				if how == "cancelled" {
					cancelled, cancel := context.WithCancel(ctx)
					cancel()
					ctx = cancelled
				} else {
					previous := settleWorkerLimit
					settleWorkerLimit = 50 * time.Millisecond
					defer func() { settleWorkerLimit = previous }()
				}
				result := make(chan error, 1)
				go func() { result <- runDoor(s, ctx, door, runID) }()
				var err error
				select {
				case err = <-result:
				case <-time.After(20 * time.Second):
					t.Fatalf("%s hung behind the live worker instead of refusing", door)
				}
				var finishing *RunFinishingError
				if !errors.As(err, &finishing) {
					t.Fatalf("%s while the worker is alive = %v, want a retryable RunFinishingError", door, err)
				}
				stateAfter, _ := os.ReadFile(filepath.Join(dir, "state.json"))
				eventsAfter, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
				if !bytes.Equal(stateBefore, stateAfter) || !bytes.Equal(eventsBefore, eventsAfter) {
					t.Fatalf("a refused %s wrote to the run", door)
				}
				if got := mustRun(t, s, runID); got.Status != "paused" || got.PendingKind != want {
					t.Fatalf("a refused %s moved the run to %s/%s — a second worker was born", door, got.Status, got.PendingKind)
				}

				release()
				settleWorkerLimit = 30 * time.Second
				if err := runDoor(s, context.Background(), door, runID); err != nil {
					t.Fatalf("%s after the worker returned: %v", door, err)
				}
				if door == "resume" {
					waitForStatus(t, s, runID, "paused", "gate")
				} else if got := mustDisk(t, root, runID); got.Status == "paused" {
					t.Fatalf("%s after release did not land: %s/%s", door, got.Status, got.PendingKind)
				}
			})
		}
	}
}
