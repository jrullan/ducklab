package service

import (
	"context"
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
