package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// Adoption is refused where it would lie: on a stage that reads documents
// intake produces, and on a project whose requirements already exist — those
// grow through the extension flow, where the approved document is the single
// ground truth.
func TestAdoptIsAnIntakeOnlyDoorForUndocumentedProjects(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — A\n\n**Priority:** must\n",
	})
	if _, err := s.StageStart(context.Background(), id, StageRequest{Stage: "spec", Adopt: true}); err == nil {
		t.Error("adopt was accepted on a stage other than intake")
	}
	_, err := s.StageStart(context.Background(), id, StageRequest{Stage: "intake", Adopt: true})
	if err == nil {
		t.Error("adopt was accepted over existing requirements")
	} else if !strings.Contains(err.Error(), "brief") {
		t.Errorf("the refusal does not point at the extension flow: %v", err)
	}
}

// The surveyed document says on its face that it was derived, not decided.
func TestAnAdoptedProposalCarriesItsOrigin(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{})
	run, err := s.StageStart(context.Background(), id, StageRequest{Stage: "intake", Mode: "solo", Adopt: true})
	if err != nil {
		t.Fatal(err)
	}
	if final, wErr := s.waitForRun(context.Background(), run.ID); wErr == nil && final.Status == "failed" {
		t.Fatalf("the survey run failed: %s", final.Failure)
	}

	proposed, err := artifact.LoadProposed(dir, artifact.KindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if proposed == nil {
		t.Fatal("the survey left no proposal")
	}
	if proposed.Front.Origin != "adopted" {
		t.Errorf("origin = %q, want adopted", proposed.Front.Origin)
	}
}

// Committed files beyond the harness's own are what make a project a
// codebase; a fresh init with only .ducklab and a .gitignore is a greenfield.
func TestHasCodeSeesCommittedFilesBeyondTheHarness(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	empty := t.TempDir()
	if _, err := s.ProjectInit(context.Background(), InitRequest{Path: empty, Name: "Empty", GitInit: true}); err != nil {
		t.Fatal(err)
	}
	if projectHasCode(empty) {
		t.Error("a fresh init counts as a codebase")
	}
}
