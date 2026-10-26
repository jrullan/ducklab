package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

// Review of #176 (4383362596): reject and then abort both succeeded, and the
// terminal record went from done/FAILED to failed/ABORTED. The whole matrix
// of decision doors × decided states, so no pair is left: once a decision has
// consumed a triage, no later door changes its record. A refused door errors;
// the two idempotent doors (accept on an accepted run returns the first
// result, abort on a run already ended returns nil) change nothing either.
func TestASecondDecisionNeverRewritesTheFirst(t *testing.T) {
	type door struct {
		name string
		do   func(s *Service, projectID, runID string) error
	}
	doors := []door{
		{"accept", func(s *Service, _, id string) error { _, err := s.RunAccept(context.Background(), id, ""); return err }},
		{"reject", func(s *Service, _, id string) error { return s.RunReject(context.Background(), id, "late") }},
		{"abort", func(s *Service, _, id string) error { return s.RunAbort(context.Background(), id) }},
		{"relaunch", func(s *Service, p, id string) error {
			_, err := s.BugTriageWith(context.Background(), p, BugTriageRequest{RelaunchOf: id, Ducklings: []string{"pato-dos"}})
			return err
		}},
	}
	states := []struct {
		name   string
		decide func(t *testing.T, s *Service, projectID, runID string)
	}{
		{"accepted", func(t *testing.T, s *Service, _, id string) {
			if _, err := s.RunAccept(context.Background(), id, ""); err != nil {
				t.Fatal(err)
			}
		}},
		{"rejected", func(t *testing.T, s *Service, _, id string) {
			if err := s.RunReject(context.Background(), id, "no"); err != nil {
				t.Fatal(err)
			}
		}},
		{"aborted", func(t *testing.T, s *Service, _, id string) {
			if err := s.RunAbort(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		}},
		{"relaunched", func(t *testing.T, s *Service, p, id string) {
			again, err := s.BugTriageWith(context.Background(), p, BugTriageRequest{RelaunchOf: id, Ducklings: []string{"pato-dos"}})
			if err != nil {
				t.Fatal(err)
			}
			cleanupStartedRun(t, s, again.ID)
			waitForStatus(t, s, again.ID, "paused", "gate")
		}},
		{"finished", func(t *testing.T, s *Service, _, id string) {
			// A run that ended done without a gate decision.
			s.runsMu.RLock()
			rs := s.runs[id]
			s.runsMu.RUnlock()
			rs.wmu.Lock()
			setRunStatus(rs.run, "done", time.Now())
			rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
			clearPending(rs.run)
			rs.wmu.Unlock()
			w, err := s.ensureWriter(rs)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.WriteState(); err != nil {
				t.Fatal(err)
			}
		}},
	}
	// The idempotent pairs; every other pair is refused with an error.
	allowed := map[string]bool{
		"accepted/accept": true,
		"rejected/abort":  true, "aborted/abort": true, "finished/abort": true,
	}
	for _, st := range states {
		for _, d := range doors {
			t.Run(st.name+"/"+d.name, func(t *testing.T) {
				s, projectID, root, runID := gatedTriage(t)
				st.decide(t, s, projectID, runID)
				statePath := filepath.Join(runlog.RunDirFor(root, runID), "state.json")
				before, err := os.ReadFile(statePath)
				if err != nil {
					t.Fatal(err)
				}
				kids := len(relaunchesOf(t, root, runID))
				err = d.do(s, projectID, runID)
				if ok := allowed[st.name+"/"+d.name]; ok && err != nil {
					t.Fatalf("%s on a %s run should be a harmless repeat, got %v", d.name, st.name, err)
				} else if !ok && err == nil {
					t.Fatalf("%s on a %s run succeeded — two decisions won", d.name, st.name)
				}
				after, err := os.ReadFile(statePath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatalf("%s on a %s run rewrote its terminal record:\nbefore %s\nafter  %s", d.name, st.name, before, after)
				}
				if got := len(relaunchesOf(t, root, runID)); got != kids {
					t.Fatalf("%s on a %s run left %d new replacement records", d.name, st.name, got-kids)
				}
				if mem := mustRun(t, s, runID); mem.Status != mustDisk(t, root, runID).Status {
					t.Fatalf("memory and disk disagree after %s on a %s run: %s vs %s", d.name, st.name, mem.Status, mustDisk(t, root, runID).Status)
				}
			})
		}
	}
}

// A repeat abort is a no-op only without words: a reason given with it would
// describe a decision that did not happen, so it is refused rather than
// dropped (B-524) — and the record still does not move.
func TestARepeatAbortWithAReasonIsRefusedNotDropped(t *testing.T) {
	s, _, root, runID := gatedTriage(t)
	if err := s.RunAbort(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(runlog.RunDirFor(root, runID), "state.json")
	before, _ := os.ReadFile(statePath)
	if err := s.RunAbortAs(context.Background(), runID, "stop it again", "mcp:test"); err == nil {
		t.Fatal("a repeat abort dropped its reason")
	}
	if after, _ := os.ReadFile(statePath); !bytes.Equal(before, after) {
		t.Fatal("a refused repeat abort rewrote the record")
	}
}
