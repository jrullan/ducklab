package service

import (
	"context"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// A decision that arrives while the paused worker is still tearing down took
// the still-open writer; the worker then closed it, and the decision failed
// with "run log is closed" (a flaky TestASecondDecisionNeverRewritesTheFirst,
// review of #176). Decisions and resumes now wait for the worker's teardown,
// then reopen the log. Each door is driven into that window deterministically:
// the worker is held after publishing its gate, before closing its log.
func TestADecisionInTheWorkersTeardownWaitsAndLands(t *testing.T) {
	for _, door := range []string{"reject", "accept", "abort"} {
		t.Run(door, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			id, root := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
			ctx := context.Background()
			if _, err := s.BugAdd(ctx, id, BugRequest{Title: "vertex drag never starts", Severity: "normal"}); err != nil {
				t.Fatal(err)
			}
			atGate, release := make(chan struct{}), make(chan struct{})
			workerTeardown = func(*runState) {
				close(atGate)
				<-release
			}
			defer func() { workerTeardown = func(*runState) {} }()
			run, err := s.BugTriage(ctx, id, "")
			if err != nil {
				t.Fatal(err)
			}
			cleanupStartedRun(t, s, run.ID)
			select {
			case <-atGate:
			case <-time.After(20 * time.Second):
				t.Fatal("the triage never reached its gate")
			}
			if got := mustRun(t, s, run.ID); got.Status != "paused" || got.PendingKind != "gate" {
				t.Fatalf("setup: worker held at %s/%s, want its published gate", got.Status, got.PendingKind)
			}
			decided := make(chan error, 1)
			go func() {
				switch door {
				case "reject":
					decided <- s.RunReject(ctx, run.ID, "no")
				case "accept":
					_, err := s.RunAccept(ctx, run.ID, "")
					decided <- err
				default:
					decided <- s.RunAbort(ctx, run.ID)
				}
			}()
			// The decision must not take the writer the held worker is about to
			// close. It may only finish once the worker has.
			select {
			case err := <-decided:
				t.Fatalf("%s finished inside the worker's teardown (err=%v)", door, err)
			case <-time.After(300 * time.Millisecond):
			}
			close(release)
			if err := <-decided; err != nil {
				t.Fatalf("%s after the teardown: %v", door, err)
			}
			events, err := runlog.ReadEvents(runlog.RunDirFor(root, run.ID))
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"reject": "run_end", "abort": "run_end", "accept": "triage_applied"}[door]
			if lastOfType(events, want) == nil {
				t.Fatalf("%s's own record never reached the log (no %s event)", door, want)
			}
			disk := mustDisk(t, root, run.ID)
			if disk.Status == "paused" {
				t.Fatalf("%s's decision did not reach state.json: %s/%s", door, disk.Status, disk.PendingKind)
			}
		})
	}
}
