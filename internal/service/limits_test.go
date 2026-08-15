package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

// The limits were recorded at one of the six places a tracker is created, so
// five kinds of run — stages, review, release, triage, test-first — wrote none
// at all and the desktop drew their meters as 0 / 0.
//
// A meter against a ceiling of zero is worse than no meter: it reads as a run
// that has already spent everything it was allowed.
func TestProjectBudgetOverridesGlobalDefaultsForRuns(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithConfig(t, s, "budgeted")

	project, err := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	project.Budget = config.Budget{MaxUSD: 7, MaxTokens: 25000000, MaxWallclockS: 7200, MaxTurns: 99}
	if err := config.SaveProject(filepath.Join(dir, ".ducklab", "project.toml"), project); err != nil {
		t.Fatal(err)
	}

	run, err := s.StageStart(context.Background(), id, StageRequest{Stage: "spec"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(context.Background(), run.ID)
	got, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := runlog.BudgetLimits{USD: 7, Tokens: 25000000, WallclockS: 7200, Turns: 99}
	if got.Run.Budget.Limit != want {
		t.Fatalf("run budget = %+v, want project budget %+v", got.Run.Budget.Limit, want)
	}
}

func TestEveryKindOfRunRecordsItsCeilings(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — A\n\n**Priority:** must\n",
		artifact.KindSpec:         "## SPEC-001 — A\n\n**Implements:** REQ-001\n",
		artifact.KindPlan:         planDoc,
	})

	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "x", Severity: "low"}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		start func() (string, error)
	}{
		{"triage", func() (string, error) {
			r, err := s.BugTriage(context.Background(), id, "")
			if err != nil {
				return "", err
			}
			return r.ID, nil
		}},
		{"stage", func() (string, error) {
			r, err := s.StageStart(context.Background(), id, StageRequest{Stage: "spec"})
			if err != nil {
				return "", err
			}
			return r.ID, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runID, err := tc.start()
			if err != nil {
				t.Fatal(err)
			}
			_, _ = s.waitForRun(context.Background(), runID)
			d, err := s.RunGet(context.Background(), runID)
			if err != nil {
				t.Fatal(err)
			}
			lim := d.Run.Budget.Limit
			if lim.Tokens <= 0 || lim.Turns <= 0 || lim.WallclockS <= 0 {
				t.Errorf("%s recorded no ceilings: %+v", tc.name, lim)
			}
		})
	}
}
