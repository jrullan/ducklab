package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/report"
	"github.com/jrullan/ducklab/internal/runlog"
)

// Scorecard measurement is sourced from the per-duckling report: only a model
// that actually made calls has evidence. A registered duckling without calls
// must not acquire a zero-valued measurement row.
func TestScorecardEvidenceUsesDucklingReportAndLeavesNoEvidenceAbsent(t *testing.T) {
	s := writableService(t, "measured", "unused")
	projectID, _ := projectWithConfig(t, s, "scorecard-evidence")
	s.runs["r-scorecard"] = &runState{run: &runlog.Run{
		ID:          "r-scorecard",
		ProjectID:   projectID,
		Stage:       "build",
		Verdict:     "PASSED",
		WallclockMs: 2_000,
		Spend: map[string]runlog.DucklingSpend{
			"measured": {Calls: 1, Tokens: 1_200, CostUSD: 0.75},
		},
	}}

	rep, err := s.Report(context.Background(), projectID, report.Options{By: "duckling"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("duckling report rows = %+v, want only measured duckling", rep.Rows)
	}
	row := rep.Rows[0]
	if row.Key != "measured" || row.Runs != 1 || row.PassRate() != 100 || row.AvgCost() != 0.75 {
		t.Errorf("measured evidence = %+v, want one passing $0.75 run", row)
	}
	for _, row := range rep.Rows {
		if row.Key == "unused" {
			t.Error("duckling with no runs received zero-valued evidence instead of absent evidence")
		}
	}
}
