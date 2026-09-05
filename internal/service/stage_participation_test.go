package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

func TestStageArtifactDoesNotClaimUnusedConfiguredSeats(t *testing.T) {
	s := serviceWithDucklings(t, "pato-used", "pato-reviewer", "pato-idle")
	if s.cfg.Defaults.ModeSeats == nil {
		s.cfg.Defaults.ModeSeats = map[string]map[string][]string{}
	}
	s.cfg.Defaults.ModeSeats[string(config.ModeSolo)] = map[string][]string{
		string(config.RoleArchitect): {"pato-used"},
		string(config.RoleReviewer):  {"pato-reviewer"},
		string(config.RoleAdvisor):   {"pato-idle"},
	}
	projectID, root := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Login\n\n**Priority:** must\n",
	})
	run, err := s.StageStart(context.Background(), projectID, StageRequest{Stage: "spec", Mode: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(context.Background(), run.ID)
	proposed, err := artifact.LoadProposed(root, artifact.KindSpec)
	if err != nil || proposed == nil {
		t.Fatalf("proposal = %v, err=%v", proposed, err)
	}
	if strings.Join(proposed.Front.Ducklings, ",") != "pato-used" {
		t.Errorf("artifact claims nonparticipants: %v", proposed.Front.Ducklings)
	}
	configured := strings.Join(proposed.Front.ConfiguredDucklings, ",")
	for _, want := range []string{"pato-used", "pato-reviewer", "pato-idle"} {
		if !strings.Contains(configured, want) {
			t.Errorf("configured roster lost %s: %v", want, proposed.Front.ConfiguredDucklings)
		}
	}
}

func TestParticipatingDucklingsAreStableAndCallDerived(t *testing.T) {
	run := &runlog.Run{Spend: map[string]runlog.DucklingSpend{
		"zeta": {Calls: 1}, "unused": {Calls: 0}, "alpha": {Calls: 2},
	}}
	if got := strings.Join(participatingDucklings(run), ","); got != "alpha,zeta" {
		t.Fatalf("participants = %q", got)
	}
}
