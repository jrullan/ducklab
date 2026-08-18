package service

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

func ctx(n int) *int { return &n }

// The catalogue of fixtures the ranking tests share. PassRate is a percentage
// (report.Row.PassRate). MeasuredByRole is what the seat rule reads for
// pass_rate; Measured is the overall.
func rankingFixture() []Scorecard {
	return []Scorecard{
		{ID: "coder", Locality: "remote", Cost: config.Cost{InputPerMTok: 1, OutputPerMTok: 6},
			Measured: &MeasuredEvidence{Runs: 20, PassRate: 60, AvgCostPerRun: 0.40, AvgWallclock: 300},
			MeasuredByRole: map[string]MeasuredEvidence{"implementer": {Runs: 12, PassRate: 75, AvgCostPerRun: 0.5}, "reviewer": {Runs: 8, PassRate: 40}},
			Index: &config.ExternalIndex{CodingScore: 76.7, IntelligenceScore: 56.6, AgenticScore: 50.2, Source: "aa", AsOf: "2026-08-18"}},
		{ID: "cheap", Locality: "remote", Cost: config.Cost{InputPerMTok: 0.07, OutputPerMTok: 0.22},
			Measured: &MeasuredEvidence{Runs: 30, PassRate: 68, AvgCostPerRun: 0.21, AvgWallclock: 650},
			MeasuredByRole: map[string]MeasuredEvidence{"reviewer": {Runs: 25, PassRate: 70, AvgCostPerRun: 0.2}, "implementer": {Runs: 5, PassRate: 20}},
			Index: &config.ExternalIndex{CodingScore: 68.8, IntelligenceScore: 52.6, AgenticScore: 45.7}},
		{ID: "quick", Locality: "remote", Cost: config.Cost{InputPerMTok: 0.1, OutputPerMTok: 0.6},
			Measured: &MeasuredEvidence{Runs: 40, PassRate: 63, AvgCostPerRun: 0.02, AvgWallclock: 100},
			MeasuredByRole: map[string]MeasuredEvidence{"implementer": {Runs: 30, PassRate: 65, AvgCostPerRun: 0.02}},
			Index: &config.ExternalIndex{CodingScore: 71.4, AgenticScore: 46.9}},
		// A person's statement: implementer only.
		{ID: "impl-only", Roles: []string{"implementer"}, Locality: "remote",
			Measured:       &MeasuredEvidence{Runs: 10, PassRate: 90, AvgCostPerRun: 0.30},
			MeasuredByRole: map[string]MeasuredEvidence{"reviewer": {Runs: 10, PassRate: 90}},
			Index:          &config.ExternalIndex{CodingScore: 90}},
		// Local: free, fast, well-measured — must not win on price.
		{ID: "local", Locality: "local", Measured: &MeasuredEvidence{Runs: 10, PassRate: 30, AvgCostPerRun: 0, AvgWallclock: 500},
			MeasuredByRole: map[string]MeasuredEvidence{"implementer": {Runs: 10, PassRate: 30}}},
		// Nothing known at all.
		{ID: "blank", Locality: "remote", Caps: config.Caps{ContextTokens: ctx(0)}},
	}
}

func candIDs(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func TestDefaultCriteriaOrderBySeat(t *testing.T) {
	cards := rankingFixture()
	// implementer: coding index first → impl-only (90) is declared for the
	// seat and leads; coder 76.7, quick 71.4. cheap (68.8) drops off the top
	// three; local has no index and no chance.
	if got := candIDs(RankCandidates("implementer", cards, CriteriaFor(nil, "implementer"))); strings.Join(got, ",") != "impl-only,coder,quick" {
		t.Errorf("implementer = %v", got)
	}
	// reviewer: pass rate IN THE SEAT first → cheap 70%, coder 40%; impl-only
	// (90% as reviewer!) is declared implementer-only and is left out; quick
	// has never reviewed → no in-seat value, sorts after those with one,
	// then by intelligence (none) then cost.
	if got := candIDs(RankCandidates("reviewer", cards, CriteriaFor(nil, "reviewer"))); strings.Join(got, ",") != "cheap,coder,quick" {
		t.Errorf("reviewer = %v", got)
	}
	// advisor: cost then wallclock → quick $0.02, cheap $0.21, coder $0.40.
	// local's $0 is not a price and does not place.
	if got := candIDs(RankCandidates("advisor", cards, CriteriaFor(nil, "advisor"))); strings.Join(got, ",") != "quick,cheap,coder" {
		t.Errorf("advisor = %v", got)
	}
	for _, role := range []string{"triager", "scribe", "human"} {
		if got := RankCandidates(role, cards, CriteriaFor(nil, role)); len(got) != 0 {
			t.Errorf("%s ranked: %v", role, got)
		}
	}
	// The why names the criteria that placed the duckling, in order.
	first := RankCandidates("implementer", cards, CriteriaFor(nil, "implementer"))[1]
	if first.Why != "coding 76.7 · 75% over 12 runs as implementer · $0.40/run" {
		t.Errorf("why = %q", first.Why)
	}
}

// The person's criteria replace the default: cost first puts quick ahead of
// everyone; a single criterion is enough; an empty list turns the seat off.
func TestConfiguredCriteriaReorder(t *testing.T) {
	cards := rankingFixture()
	cfg := map[string][]string{"implementer": {"cost_per_run", "coding_index"}, "reviewer": {}}
	if got := candIDs(RankCandidates("implementer", cards, CriteriaFor(cfg, "implementer"))); strings.Join(got, ",") != "quick,cheap,impl-only" {
		t.Errorf("cost-first implementer = %v", got)
	}
	if got := RankCandidates("reviewer", cards, CriteriaFor(cfg, "reviewer")); len(got) != 0 {
		t.Errorf("reviewer suggestions were switched off, got %v", got)
	}
	// A role not mentioned keeps its default.
	if got := candIDs(RankCandidates("advisor", cards, CriteriaFor(cfg, "advisor"))); got[0] != "quick" {
		t.Errorf("advisor default lost: %v", got)
	}
	// Unknowns sort last at every level, but the ordering continues below
	// them: with input price first, blank (no price) trails coder/cheap/quick.
	if got := candIDs(RankCandidates("implementer", cards, []string{"input_cost"})); strings.Join(got, ",") != "cheap,quick,coder" {
		t.Errorf("input-price implementer = %v", got)
	}
}

func TestCriteriaValidation(t *testing.T) {
	if err := ValidateCriteria(map[string][]string{"implementer": {"coding_index", "nope"}}); err == nil || !strings.Contains(err.Error(), `unknown criterion "nope"`) {
		t.Errorf("unknown key accepted: %v", err)
	}
	if err := ValidateCriteria(map[string][]string{"wizard": {"coding_index"}}); err == nil {
		t.Error("unknown role accepted")
	}
	if err := ValidateCriteria(map[string][]string{"reviewer": {}}); err != nil {
		t.Errorf("empty list refused: %v", err)
	}
	keys := map[string]bool{}
	for _, c := range CriterionCatalog() {
		if c.Direction != "asc" && c.Direction != "desc" {
			t.Errorf("%s has no direction", c.Key)
		}
		keys[c.Key] = true
	}
	for role, list := range DefaultCriteria() {
		for _, k := range list {
			if !keys[k] {
				t.Errorf("default for %s names %q, not in the catalog", role, k)
			}
		}
	}
}
