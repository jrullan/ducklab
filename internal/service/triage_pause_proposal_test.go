package service

import (
	"context"
	"slices"
	"testing"
)

// B-522 × B-523: a triage paused at its safe point and resumed, or relaunched
// with another triager, still holds every bug to promote's rule — the repair
// turn runs once per bug (never redone for a bug finished before the pause)
// and the gate carries promote_blockers for the bugs on both sides of it.
func TestAPausedTriageStillChecksEveryProposalAgainstPromote(t *testing.T) {
	for _, relaunch := range []bool{false, true} {
		name := "resumed"
		if relaunch {
			name = "relaunched"
		}
		t.Run(name, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno", "pato-dos")
			id, root := calculatorProject(t, s)
			if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "pinch zoom scrolls the page", Severity: "normal"}); err != nil {
				t.Fatal(err)
			}
			repairs := triagerScript(t, s, b014Triage, b014Triage)
			seedTriageHistory(s, id, "pato-dos")
			l, err := s.prepareTriage(context.Background(), id, BugTriageRequest{Ducklings: []string{"pato-uno"}})
			if err != nil {
				t.Fatal(err)
			}
			cleanupStartedRun(t, s, l.rs.run.ID)
			overThreshold(l.rs)
			s.executeTriage(l.ctx, l.rs, l.root, l.todo, l.all, nil)
			if st := l.rs.snapshotRun(); st.Status != "paused" || st.PendingKind != "history_duration" {
				t.Fatalf("triage did not pause at its safe point: %s/%s", st.Status, st.PendingKind)
			}
			// The pause landed after the first bug's repair turn, not between
			// its answer and its repair.
			if got := triageEventsOf(t, root, l.rs.run.ID, "triage_repair"); len(got) != 1 {
				t.Fatalf("before the pause: %d repair records, want 1", len(got))
			}

			gateRun := l.rs.run.ID
			if relaunch {
				again, err := s.BugTriageWith(context.Background(), id, BugTriageRequest{RelaunchOf: l.rs.run.ID, Ducklings: []string{"pato-dos"}})
				if err != nil {
					t.Fatal(err)
				}
				gateRun = again.ID
				cleanupStartedRun(t, s, gateRun)
			} else if _, err := s.RunResume(context.Background(), gateRun); err != nil {
				t.Fatal(err)
			}
			run := waitForStatus(t, s, gateRun, "paused", "gate")

			var flagged []string
			for _, b := range triageProposalList(run.PendingData["promote_blockers"]) {
				if id, _ := b["bug"].(string); id != "" {
					flagged = append(flagged, id)
				}
			}
			slices.Sort(flagged)
			if !slices.Equal(flagged, []string{"B-001", "B-002"}) {
				t.Fatalf("gate promote_blockers name %v, want both bugs: %#v", flagged, run.PendingData["promote_blockers"])
			}
			want := 2 // one repair per bug in the resumed run: never redone
			if relaunch {
				want = 3 // the replaced run's one, then both again under the new seat
			}
			if got := int(repairs.Load()); got != want {
				t.Fatalf("triager was given %d repair turns, want %d", got, want)
			}
		})
	}
}
