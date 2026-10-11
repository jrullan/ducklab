package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

func settled(t *testing.T, s *Service, runID string) {
	t.Helper()
	cleanupStartedRun(t, s, runID)
	s.runsMu.RLock()
	rs := s.runs[runID]
	s.runsMu.RUnlock()
	<-rs.done
}

// Review of #175 (P1): an old tab still showing r0 asks for changes after a
// newer revision already answered r0. The request must be refused — it used
// to pass, revise whatever proposal was newest on disk, and claim r0 as its
// parent.
func TestAStaleTabCannotReviseAnAlreadyRevisedRun(t *testing.T) {
	s, projectID, root, guide := revisionChainProject(t)
	r0 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Mode: "solo", Refs: []string{guide}, From: "Fill the gap."})
	r1 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", RevisesRun: r0.ID, Revise: "first correction"})

	_, err := s.StageStart(context.Background(), projectID, StageRequest{Stage: "intake", RevisesRun: r0.ID, Revise: "from the old tab"})
	if !errors.Is(err, ErrLaunchRefused) || !strings.Contains(err.Error(), "already revised by "+r1.ID) {
		t.Fatalf("stale revision of %s = %v, want a refusal naming %s", r0.ID, err, r1.ID)
	}
	intent, _ := artifact.Load(root, artifact.KindIntent)
	if got := intent.Sections[0].Field("revisions"); got != r1.ID {
		t.Fatalf("revisions = %q, want only %s", got, r1.ID)
	}
	detail, _ := s.RunGet(context.Background(), r0.ID)
	if !strings.Contains(detail.Run.Resolution, "revised by "+r1.ID) {
		t.Fatalf("r0 resolution = %q, want it to name its revision", detail.Run.Resolution)
	}
}

// The draft on the table belongs to another run: revising r0 by name would
// edit that run's proposal while claiming r0. Refused, naming the owner.
func TestRevisingARunWhoseDraftIsNoLongerOnTheTableIsRefused(t *testing.T) {
	s, projectID, _, _ := revisionChainProject(t)
	r0 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Mode: "solo", From: "Fill the gap."})
	rB := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Mode: "solo", From: "A different brief."})
	_, err := s.StageStart(context.Background(), projectID, StageRequest{Stage: "intake", RevisesRun: r0.ID, Revise: "fix it"})
	if !errors.Is(err, ErrLaunchRefused) || !strings.Contains(err.Error(), rB.ID+"'s is") {
		t.Fatalf("revision of an off-table draft = %v, want a refusal naming %s", err, rB.ID)
	}
}

// A double-submit of the same revision: exactly one claims the gate; the
// other is refused before any run exists, and the journal holds one revision
// numbered 1.
func TestADoubleSubmittedRevisionClaimsTheGateOnce(t *testing.T) {
	s, projectID, root, _ := revisionChainProject(t)
	r0 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Mode: "solo", From: "Fill the gap."})
	s.runsMu.RLock()
	before := len(s.runs)
	s.runsMu.RUnlock()

	var wg sync.WaitGroup
	results := make([]*runlog.Run, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = s.StageStart(context.Background(), projectID, StageRequest{Stage: "intake", RevisesRun: r0.ID, Revise: "fix it"})
		}(i)
	}
	close(start)
	wg.Wait()

	var won *runlog.Run
	refused := 0
	for i := range results {
		switch {
		case errs[i] == nil:
			if won != nil {
				t.Fatalf("both submissions started a revision: %s and %s", won.ID, results[i].ID)
			}
			won = results[i]
		case errors.Is(errs[i], ErrLaunchRefused):
			refused++
		default:
			t.Fatalf("unexpected error: %v", errs[i])
		}
	}
	if won == nil || refused != 1 {
		t.Fatalf("winner %v, refused %d; want one of each", won, refused)
	}
	settled(t, s, won.ID)
	s.runsMu.RLock()
	after := len(s.runs)
	s.runsMu.RUnlock()
	if after != before+1 {
		t.Fatalf("runs grew by %d, want 1: the refused submission left a record", after-before)
	}
	intent, _ := artifact.Load(root, artifact.KindIntent)
	sec := intent.Sections[0]
	if sec.Field("revisions") != won.ID || strings.Count(sec.Body, "### Revision") != 1 {
		t.Fatalf("journal after double-submit: revisions %q\n%s", sec.Field("revisions"), sec.Body)
	}
	detail, _ := s.RunGet(context.Background(), won.ID)
	if detail.Run.Revision != 1 {
		t.Fatalf("revision number = %d, want 1", detail.Run.Revision)
	}
}

// Review of #175 (P2): discarding a requirements proposal closes its intent
// as not accepted. When recording that fails, the discard is undone and the
// error returned — the proposal is the only handle left to retry from.
func TestDiscardRestoresTheProposalWhenTheIntentCannotBeResolved(t *testing.T) {
	s, projectID, root, _ := revisionChainProject(t)
	r0 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Mode: "solo", From: "Fill the gap."})
	proposal := artifact.ProposedPath(root, artifact.KindRequirements)
	want, err := os.ReadFile(proposal)
	if err != nil {
		t.Fatal(err)
	}
	// Make the journal unreadable and unwritable: a directory where the
	// file was.
	intentPath := artifact.Path(root, artifact.KindIntent)
	if err := os.Remove(intentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(intentPath+"/blocked", 0o755); err != nil {
		t.Fatal(err)
	}
	err = s.ArtifactDiscard(context.Background(), projectID, "requirements")
	if err == nil || !strings.Contains(err.Error(), "record the intent's outcome") {
		t.Fatalf("discard = %v, want the intent failure", err)
	}
	got, readErr := os.ReadFile(proposal)
	if readErr != nil || string(got) != string(want) {
		t.Fatalf("proposal of %s was not restored: %v", r0.ID, readErr)
	}
}

// And when the journal is writable, discarding closes the intent.
func TestDiscardClosesTheIntentAsNotAccepted(t *testing.T) {
	s, projectID, root, _ := revisionChainProject(t)
	startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Mode: "solo", From: "Fill the gap."})
	if err := s.ArtifactDiscard(context.Background(), projectID, "requirements"); err != nil {
		t.Fatal(err)
	}
	intent, _ := artifact.Load(root, artifact.KindIntent)
	if got := intent.Sections[0].Field("outcome"); got != "not accepted" {
		t.Fatalf("outcome after discard = %q", got)
	}
	if _, err := os.Stat(artifact.ProposedPath(root, artifact.KindRequirements)); !os.IsNotExist(err) {
		t.Fatalf("proposal still on disk: %v", err)
	}
}
