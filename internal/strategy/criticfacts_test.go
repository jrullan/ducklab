package strategy

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
)

func TestPlanCriticFactsRejectOnlyExcludedWork(t *testing.T) {
	params := &ExecuteParams{
		KnownIDs:     map[string]bool{"SPEC-001": true, "SPEC-006": true, "SPEC-007": true},
		PriorityByID: map[string]string{"SPEC-006": "could", "SPEC-007": "wont"},
	}
	candidate := "## M-01 — Core\n\n### T-001 — Build\n\n**Implements:** SPEC-001\n"
	tests := []struct {
		name    string
		finding agent.Finding
		want    string
	}{
		{"wont as work", agent.Finding{Issue: "SPEC-007 is not covered", Fix: "Add an acceptance slice implementing it"}, "is wont"},
		{"unselected could", agent.Finding{Issue: "SPEC-006 lacks live probes", Fix: "Add a task to implement live probes"}, "is could"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := invalidPlanCriticFinding(params, tc.finding, candidate); !strings.Contains(got, tc.want) {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlanCriticFactsRetainIssueWhileSanitizingBadCoordinatesAndFixes(t *testing.T) {
	params := &ExecuteParams{KnownIDs: map[string]bool{"SPEC-001": true}}
	candidate := "## M-01 — Core\n\n### T-008 — Gate\n"
	finding := agent.Finding{
		Issue: "SPEC-018 obligation requires provenance evidence for the authority boundary",
		Fix:   "Add T-009 to enforce the boundary",
	}
	got, reasons := sanitizePlanCriticFinding(params, finding, candidate)
	if len(reasons) != 2 || !strings.Contains(got.Issue, "provenance evidence") || strings.Contains(got.Issue, "SPEC-018") {
		t.Fatalf("sanitized issue=%q reasons=%v", got.Issue, reasons)
	}
	if strings.Contains(got.Fix, "T-009") || !strings.Contains(got.Fix, "existing candidate topology") {
		t.Fatalf("unsafe fix survived: %q", got.Fix)
	}
	if reason := invalidPlanCriticFinding(params, got, candidate); reason != "" {
		t.Fatalf("retained semantic issue was rejected: %s", reason)
	}
}

func TestPlanCriticFactsKeepSelectedCouldAndExistingTopology(t *testing.T) {
	params := &ExecuteParams{
		KnownIDs:     map[string]bool{"SPEC-006": true},
		PriorityByID: map[string]string{"SPEC-006": "could"},
	}
	candidate := "## M-07 — Optional\n\n### T-006 — Probe\n\n**Implements:** SPEC-006\n"
	finding := agent.Finding{Issue: "SPEC-006 selected behavior is incomplete in M-07", Fix: "Implement the selected behavior in T-006"}
	if got := invalidPlanCriticFinding(params, finding, candidate); got != "" {
		t.Fatalf("selected could finding was rejected: %s", got)
	}
}

func TestPlanCriticFactsKeepRemovalOfForbiddenWontBehavior(t *testing.T) {
	params := &ExecuteParams{
		KnownIDs:     map[string]bool{"SPEC-007": true},
		PriorityByID: map[string]string{"SPEC-007": "wont"},
	}
	finding := agent.Finding{Issue: "SPEC-007 is implemented as an activation decision", Fix: "Remove the activation decision from T-001"}
	if got := invalidPlanCriticFinding(params, finding, "## M-01 — Core\n\n### T-001 — Activate\n"); got != "" {
		t.Fatalf("valid removal of wont behavior was rejected: %s", got)
	}
}

func TestPlanCriticFactsRespectNamedCouldInsideMixedSpec(t *testing.T) {
	params := &ExecuteParams{
		KnownIDs:       map[string]bool{"SPEC-006": true},
		PriorityByName: map[string]string{"toolchain probes (out of scope)": "could", "live probes": "could"},
	}
	candidate := "## M-01 — Conformance\n\n### T-001 — Fixtures\n\n**Implements:** SPEC-006\n\n**Work unit:** Run isolated fixtures.\n\n**Acceptance slices:**\n- Fixtures are deterministic.\n"
	finding := agent.Finding{Issue: "SPEC-006 Live probes are not exercised", Fix: "Add an acceptance slice implementing live probes"}
	if got := invalidPlanCriticFinding(params, finding, candidate); !strings.Contains(got, "could obligation") {
		t.Fatalf("mixed-spec deferred obligation was not rejected: %q", got)
	}
}

func TestPlanCriticFactsFilterBeforeRepairAndRecordWhy(t *testing.T) {
	var events []map[string]interface{}
	params := &ExecuteParams{
		KnownIDs:     map[string]bool{"SPEC-001": true, "SPEC-006": true, "SPEC-007": true},
		PriorityByID: map[string]string{"SPEC-006": "could", "SPEC-007": "wont"},
		OnEvent: func(kind string, data map[string]interface{}) {
			if kind == "critic_findings_filtered" {
				events = append(events, data)
			}
		},
	}
	v := &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{
		{Severity: "critical", Issue: "SPEC-006 is absent", Fix: "Add a task to implement it"},
		{Severity: "major", Issue: "SPEC-007 needs coverage", Fix: "Add an acceptance slice"},
	}}
	outcome := &agent.Outcome{Parsed: v}
	filterPlanCriticOutcome(params, outcome, "## M-01 — Core\n", 2, 3)
	if v.Verdict != "request-changes" || len(v.Findings) != 1 || !strings.Contains(v.Findings[0].Issue, "review is inconclusive") {
		t.Fatalf("effective verdict = %s findings=%v", v.Verdict, v.Findings)
	}
	if len(events) != 1 || events[0]["rejected_count"] != 2 {
		t.Fatalf("events = %#v", events)
	}
}

func TestPlanCriticFilterPreservesH1iIssueWhenCoordinateAndFixAreWrong(t *testing.T) {
	params := &ExecuteParams{KnownIDs: map[string]bool{"SPEC-001": true}}
	v := &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{{
		Severity: "critical",
		Issue:    "SPEC-018 obligation not covered: validate provenance and executable fixtures and return evidence",
		Fix:      "Add T-009 for this obligation",
	}}}
	filterPlanCriticOutcome(params, &agent.Outcome{Parsed: v}, "## M-01 — Core\n\n### T-008 — Gate\n", 1, 2)
	if v.Verdict != "request-changes" || len(v.Findings) != 1 {
		t.Fatalf("valid issue was filtered: verdict=%s findings=%v", v.Verdict, v.Findings)
	}
	if !strings.Contains(v.Findings[0].Issue, "validate provenance") || strings.Contains(v.Findings[0].Issue, "SPEC-018") || strings.Contains(v.Findings[0].Fix, "T-009") {
		t.Fatalf("issue and fix were not separated: %+v", v.Findings[0])
	}
}

func TestPlanCriticFixCannotAppendFourthSliceForSmallSeat(t *testing.T) {
	params := &ExecuteParams{SmallSeat: true}
	candidate := "## M-01 — Core\n\n### T-001 — Build\n\n**Acceptance slices:**\n- One.\n- Two.\n- Three.\n"
	finding := agent.Finding{Issue: "T-001 lacks another outcome", Fix: "Add an Acceptance slice to T-001"}
	got := safePlanCriticFix(params, finding, candidate)
	if !strings.Contains(got, "without adding a fourth") {
		t.Fatalf("unsafe fix was retained: %q", got)
	}
}
