package service

import (
	"context"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
)

// A triage persisted its run_start before its writer reached the bus, so no
// subscriber heard it. The desktop makes a run's row from run_start and
// ignores events for runs it does not know: a new triage stayed invisible
// until something refetched the list (B-538).
func TestATriagesRunStartReachesTheBusFirst(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := calculatorProject(t, s)
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "pinch zoom scrolls the page", Severity: "normal"}); err != nil {
		t.Fatal(err)
	}
	triagerScript(t, s, b014Triage, b014Triage)
	sub, unsub := s.bus.Subscribe("b538", func(bus.Event) bool { return true })
	defer unsub()

	run, err := s.BugTriageWith(context.Background(), id, BugTriageRequest{Ducklings: []string{"pato-uno"}})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, run.ID)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-sub.Ch:
			if e.RunID != run.ID {
				continue
			}
			if e.Type != "run_start" {
				t.Fatalf("the triage's first event on the bus is %s, want run_start", e.Type)
			}
			if e.ProjectID != id || e.Seq != 1 || e.Data["stage"] != "triage" {
				t.Fatalf("run_start = project %q seq %d stage %v; want %q, 1, triage", e.ProjectID, e.Seq, e.Data["stage"], id)
			}
			return
		case <-deadline:
			t.Fatal("the triage's run_start never reached the bus")
		}
	}
}
