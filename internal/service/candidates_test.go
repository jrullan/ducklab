package service

import "testing"

// RankCandidates is the one seat-aware ordering rule; every client (desktop,
// MCP) shows what it returns. Pin the shape a small operator relies on: the
// seat decides the metric, evidence is required, three at most, why is text.
func TestRankCandidatesFollowsTheSeat(t *testing.T) {
	cards := []Scorecard{
		{ID: "bench-best", Measured: &MeasuredEvidence{Runs: 14, PassRate: .92, AvgCostPerRun: .31, AvgWallclock: 90}, Bench: map[string]BenchEvidence{"suite": {Score: .91}}},
		{ID: "pass-cheap", Measured: &MeasuredEvidence{Runs: 14, PassRate: .92, AvgCostPerRun: .20, AvgWallclock: 40}},
		{ID: "pass-slow", Measured: &MeasuredEvidence{Runs: 9, PassRate: .95, AvgCostPerRun: .50, AvgWallclock: 300}},
		{ID: "no-runs", Measured: &MeasuredEvidence{Runs: 0}},
		{ID: "unmeasured"},
	}
	first := func(role string) string {
		got := RankCandidates(role, cards)
		if len(got) == 0 {
			t.Fatalf("%s: no candidates", role)
		}
		if len(got) > 3 {
			t.Errorf("%s: %d candidates, want at most 3", role, len(got))
		}
		for _, c := range got {
			if c.Why == "" {
				t.Errorf("%s: candidate %s has no why", role, c.ID)
			}
			if c.ID == "no-runs" || c.ID == "unmeasured" {
				t.Errorf("%s: %s has no evidence and was suggested", role, c.ID)
			}
		}
		return got[0].ID
	}
	if got := first("implementer"); got != "bench-best" {
		t.Errorf("implementer first = %s, want bench-best (bench-first)", got)
	}
	if got := first("architect"); got != "bench-best" {
		t.Errorf("architect first = %s, want bench-best (bench-first)", got)
	}
	if got := first("reviewer"); got != "pass-slow" {
		t.Errorf("reviewer first = %s, want pass-slow (pass rate first)", got)
	}
	if got := first("advisor"); got != "pass-cheap" {
		t.Errorf("advisor first = %s, want pass-cheap (cost then wallclock)", got)
	}
	for _, role := range []string{"triager", "scribe", "human"} {
		if got := RankCandidates(role, cards); len(got) != 0 {
			t.Errorf("%s: %v, want none — those seats are not ranked by evidence", role, got)
		}
	}
}
