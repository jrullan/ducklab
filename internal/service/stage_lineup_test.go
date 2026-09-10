package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
)

// Settings has let a person save a council line-up since mode line-ups existed,
// and nothing ever read it: ducklingsFor was wired into task runs, and council
// only ever runs as a stage. The person ticked their picks, saved, launched
// intake — and watched one model draft AND critique itself, the exact
// decorrelation failure line-ups exist to prevent.
func TestAStageHonoursItsModesLineUp(t *testing.T) {
	s := writableService(t, "pato-uno", "pato-dos", "pato-tres")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — A\n\n**Priority:** must\n",
	})
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		Ducklings:     map[string][]string{"council": {"pato-dos", "pato-tres"}},
	}); err != nil {
		t.Fatal(err)
	}

	run, err := s.StageStart(context.Background(), id, StageRequest{Stage: "spec"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(context.Background(), run.ID)

	d, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Run.Roster["architect"]; got != "pato-dos" {
		t.Errorf("architect = %q, want the line-up's first", got)
	}
	if got := d.Run.Roster["reviewer"]; got != "pato-tres" {
		t.Errorf("reviewer = %q, want the line-up's second", got)
	}
}

// The other half of the same screenshot: the budget meter sat at zero for the
// whole run, because the stage's log adapter carried neither the run record —
// so calls were attributed to nobody — nor the spend hook that moves the meter.
func TestAStageAttributesItsSpend(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — A\n\n**Priority:** must\n",
	})
	run, err := s.StageStart(context.Background(), id, StageRequest{Stage: "spec", Mode: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(context.Background(), run.ID)

	d, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Run.Spend) == 0 {
		t.Fatalf("the stage's calls were attributed to nobody: %+v", d.Run.Budget)
	}
	if _, ok := d.Run.Spend["pato-uno"]; !ok {
		t.Errorf("spend does not name the duckling that ran: %v", d.Run.Spend)
	}
}

// A one-entry line-up sets the architect without touching the reviewer, and an
// empty one changes nothing: the roster's answer stands where the person said
// nothing.
func TestAPartialLineUpLeavesTheRestAlone(t *testing.T) {
	r := map[config.Role]config.DucklingID{
		config.RoleArchitect: "pato-atom", config.RoleReviewer: "pato-local",
	}
	applyStageLineup(r, []string{"pato-sonnet"})
	if r[config.RoleArchitect] != "pato-sonnet" {
		t.Errorf("architect = %q", r[config.RoleArchitect])
	}
	if r[config.RoleReviewer] != "pato-local" {
		t.Errorf("reviewer = %q — a one-entry line-up must not clear the second seat", r[config.RoleReviewer])
	}
	applyStageLineup(r, nil)
	if r[config.RoleArchitect] != "pato-sonnet" {
		t.Error("an empty line-up changed the roster")
	}
}

// Fledge P4 explicitly seated two different OpenRouter ducklings, yet every
// stage warned that the configured local duckling reviewed itself. The stage
// had computed the warning before applying the requested line-up.
func TestAStageWarningUsesItsEffectiveLineUp(t *testing.T) {
	s := writableService(t, "pato-local", "pato-k3", "pato-glm")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — A\n\n**Priority:** must\n",
	})

	run, err := s.StageStart(context.Background(), id, StageRequest{
		Stage: "spec", Mode: "council", Ducklings: []string{"pato-k3", "pato-glm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(context.Background(), run.ID)

	d, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Run.Warning != "" {
		t.Fatalf("warning describes configured rather than effective seats: %q", d.Run.Warning)
	}
	if d.Run.Roster["architect"] != "pato-k3" || d.Run.Roster["reviewer"] != "pato-glm" {
		t.Fatalf("effective roster = %#v", d.Run.Roster)
	}
}

// B-372: plan_extend defaults to solo, but its composed candidate still gets
// an independent reviewer. With that seat empty, Ducklab used to spend the
// architect turn and only then fail while looking up duckling "".
func TestPlanExtendRefusesAnUnseatedCompositionReviewerBeforeCreatingARun(t *testing.T) {
	s := writableService(t, "pato-architect")
	s.cfg.Defaults.ModeSeats = map[string]map[string][]string{
		"council": {"architect": {"pato-architect"}},
	}
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindSpec: "## SPEC-001 — Existing behavior\n\nContract.\n",
		artifact.KindPlan: "## M-01 — Core\n\n### T-001 — Existing task\n\n**Implements:** SPEC-001\n",
	})

	_, err := s.StageStart(context.Background(), id, StageRequest{
		Stage: "plan", Extend: "add another task",
	})
	if err == nil || !strings.Contains(err.Error(), "no reviewer seated") || !strings.Contains(err.Error(), "independent review") {
		t.Fatalf("unseated composition reviewer was not rejected at launch: %v", err)
	}
	runs, listErr := s.RunList(context.Background(), RunFilter{ProjectID: id})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(runs) != 0 {
		t.Fatalf("failed preflight created a run record: %+v", runs)
	}
}

func TestPlanExtendCanSeatItsCompositionReviewerFromTheLaunchLineUp(t *testing.T) {
	s := writableService(t, "pato-architect", "pato-reviewer")
	s.cfg.Defaults.ModeSeats = map[string]map[string][]string{
		"council": {"architect": {"pato-architect"}},
	}
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindSpec: "## SPEC-001 — Existing behavior\n\nContract.\n",
		artifact.KindPlan: "## M-01 — Core\n\n### T-001 — Existing task\n\n**Implements:** SPEC-001\n",
	})

	entry, err := s.registry.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	projCfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	roster, _ := s.resolveRoster(projCfg, "solo")
	needsReviewer, err := stageNeedsReviewer(entry.Path, "plan", "solo", false)
	if err != nil {
		t.Fatal(err)
	}
	s.fillDocumentStageSeats(projCfg, roster, needsReviewer)
	applyStageLineup(roster, []string{"pato-architect", "pato-reviewer"})
	if roster[config.RoleArchitect] != "pato-architect" || roster[config.RoleReviewer] != "pato-reviewer" {
		t.Fatalf("effective stage roster = %#v", roster)
	}
}

func TestSoloAdoptDoesNotRequireACompositionReviewer(t *testing.T) {
	s := writableService(t, "pato-architect")
	_, root := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Existing requirement\n\n**Priority:** must\n",
	})
	needsReviewer, err := stageNeedsReviewer(root, "intake", "solo", true)
	if err != nil {
		t.Fatal(err)
	}
	if needsReviewer {
		t.Fatal("solo adoption required a reviewer although it does not run composition review")
	}
}
