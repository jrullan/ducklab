package strategy

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
)

func TestPlanCriticFactsRejectInventedIDsAndExcludedWork(t *testing.T) {
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
		{"unknown spec", agent.Finding{Issue: "T-003 references nonexistent SPEC-009", Fix: "replace it"}, "not an accepted project id"},
		{"invented milestone", agent.Finding{Issue: "Missing M-07 for governance", Fix: "Add M-07"}, "cannot allocate topology ids"},
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
		KnownIDs:     map[string]bool{"SPEC-001": true, "SPEC-007": true},
		PriorityByID: map[string]string{"SPEC-007": "wont"},
		OnEvent: func(kind string, data map[string]interface{}) {
			if kind == "critic_findings_filtered" {
				events = append(events, data)
			}
		},
	}
	v := &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{
		{Severity: "critical", Issue: "SPEC-009 is absent", Fix: "Add a task"},
		{Severity: "major", Issue: "SPEC-007 needs coverage", Fix: "Add an acceptance slice"},
	}}
	outcome := &agent.Outcome{Parsed: v}
	filterPlanCriticOutcome(params, outcome, "## M-01 — Core\n", 2, 3)
	if v.Verdict != "approve" || len(v.Findings) != 0 {
		t.Fatalf("effective verdict = %s findings=%v", v.Verdict, v.Findings)
	}
	if len(events) != 1 || events[0]["rejected_count"] != 2 {
		t.Fatalf("events = %#v", events)
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
