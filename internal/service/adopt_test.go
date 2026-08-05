package service

import (
	"context"
	"os"
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

// T-022 was removed cleanly and T-023 kept depending on it — "depends on a
// task that does not exist, so it can never start", a dead end no button
// could fix, because tasks have no dependency editor. The removal now cleans
// up the references it dangles.
func TestRemovingATaskCleansTheReferencesToIt(t *testing.T) {
	s := writableService(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-001 — First\n\n" +
			"### T-022 — Browser tests\n\nTest it.\n\n" +
			"### T-023 — Only dep\n\n**Depends on:** T-022\n\nAfter.\n\n" +
			"### T-024 — Multi dep\n\n**Depends on:** T-022, T-023\n\nLater.\n",
	})
	out, err := s.TaskRemove(context.Background(), id, "T-022")
	if err != nil {
		t.Fatal(err)
	}
	cleaned, _ := out["dependencies_cleaned"].([]string)
	if len(cleaned) != 2 {
		t.Errorf("dependencies_cleaned = %v, want T-023 and T-024", out["dependencies_cleaned"])
	}
	plan, _ := artifact.Load(dir, artifact.KindPlan)
	for _, m := range plan.Sections {
		for _, c := range m.Children {
			if strings.Contains(c.Body, "T-022") {
				t.Errorf("%s still references the removed task:\n%s", c.ID, c.Body)
			}
			if c.ID == "T-024" && !strings.Contains(c.Body, "**Depends on:** T-023") {
				t.Errorf("T-024 lost its OTHER dependency:\n%s", c.Body)
			}
			if c.ID == "T-023" && strings.Contains(strings.ToLower(c.Body), "depends on") {
				t.Errorf("T-023's emptied depends line survived:\n%s", c.Body)
			}
		}
	}
}

// A first plan over a fully as-built spec has nothing to plan; a model would
// invent tasks to build what is built. The plan grows from briefs and bugs.
func TestPlanRefusesAFullyAsBuiltSpec(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Engine\n\n**Priority:** must\n\nRuns.\n",
		artifact.KindSpec: "## SPEC-001 — Engine\n\n**Implements:** REQ-001\n**As-built:** yes\n\nRuns.\n\n" +
			"## SPEC-002 — Excluded\n\n**Implements:** REQ-001\n**Priority:** wont\n\nNo.\n",
	})
	_, err := s.StageStart(context.Background(), id, StageRequest{Stage: "plan"})
	if err == nil {
		t.Fatal("a plan run started over a fully as-built spec")
	}
	if !strings.Contains(err.Error(), "nothing to plan") {
		t.Errorf("the refusal does not explain itself: %v", err)
	}

	// One genuine gap unlocks planning.
	spec := "## SPEC-001 — Engine\n\n**Implements:** REQ-001\n**As-built:** yes\n\nRuns.\n\n" +
		"## SPEC-003 — Missing piece\n\n**Implements:** REQ-001\n\nNot built.\n"
	if err := os.WriteFile(artifact.Path(dir, artifact.KindSpec), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StageStart(context.Background(), id, StageRequest{Stage: "plan", Mode: "solo"}); err != nil {
		t.Errorf("a spec with a real gap was refused: %v", err)
	}
}
