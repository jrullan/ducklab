package strategy

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

func TestPlanSeedUsesAcceptedInScopeSpecPartition(t *testing.T) {
	seed, err := seedPlanManifest([]PlanSeedSpec{
		{ID: "SPEC-001", Title: "Core"},
		{ID: "SPEC-002", Title: "Deferred", Priority: "could"},
		{ID: "SPEC-003", Title: "Excluded", Priority: "wont"},
		{ID: "SPEC-004", Title: "Governance", Priority: "must"},
		{ID: "SPEC-005", Title: "Existing", AsBuilt: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seed.Milestones) != 1 || len(seed.Milestones[0].Tasks) != 0 {
		t.Fatalf("seed = %#v, want one empty topology and no inferred tasks", seed)
	}
	if got := missingPlanSeedCoverage(seed, []string{"SPEC-001", "SPEC-004"}); strings.Join(got, ",") != "SPEC-001,SPEC-004" {
		t.Fatalf("coverage ledger = %v", got)
	}
}

func TestPlanSeedDoesNotConfuseCoverageCountWithTaskBoundary(t *testing.T) {
	specs := make([]PlanSeedSpec, 0, agent.MaxPlanManifestTasks+1)
	for i := 1; i <= agent.MaxPlanManifestTasks+1; i++ {
		specs = append(specs, PlanSeedSpec{ID: fmt.Sprintf("SPEC-%03d", i), Title: "Capability"})
	}
	seed, err := seedPlanManifest(specs)
	if err != nil {
		t.Fatalf("coverage slots were treated as tasks: %v", err)
	}
	if len(seed.Milestones[0].Tasks) != 0 {
		t.Fatalf("coverage count invented tasks: %#v", seed.Milestones[0].Tasks)
	}
}

func TestPlanSeedCoverageCannotDisappearThroughARepair(t *testing.T) {
	seed, err := seedPlanManifest([]PlanSeedSpec{{ID: "SPEC-001"}, {ID: "SPEC-002"}})
	if err != nil {
		t.Fatal(err)
	}
	seed.Milestones[0].Tasks = append(seed.Milestones[0].Tasks, agent.ManifestTask{
		ID: "T-001", Implements: []string{"SPEC-001"},
	})
	if got := missingPlanSeedCoverage(seed, []string{"SPEC-001", "SPEC-002"}); strings.Join(got, ",") != "SPEC-002" {
		t.Fatalf("missing coverage = %v", got)
	}
}

func TestFirstPlanStartsAsAFocalPatchOverTheEngineSeed(t *testing.T) {
	patchText := `{"operations":[{"op":"add_task","task_id":"T-001","milestone_id":"M-01","task":{"id":"T-001","title":"Build core","implements":["SPEC-001"],"work_unit":"build the core","acceptance_slices":["the core builds"],"acceptance_probes":["true"],"produces":["capability:core"],"consumes":[],"verification":"true"}}]}`
	parsedPatch, err := agent.ParseContract("json:plan_manifest_patch", patchText)
	if err != nil {
		t.Fatal(err)
	}
	planText := "## M-01 — Specification coverage\n\n### T-001 — Build core\n\nBuild it.\n\n**Implements:** SPEC-001\n\n**Produces:** capability:core\n\n**Consumes:** none\n\n**Verification:** `true`"
	parsedPlan, err := agent.ParseContract("markdown_sections:M", planText)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	params := councilParams(rec,
		&agent.Outcome{Text: patchText, Parsed: parsedPatch},
		verdictOutcome("approve"),
		&agent.Outcome{Text: planText, Parsed: parsedPlan},
		verdictOutcome("approve"),
	)
	params.KnownIDs = map[string]bool{"SPEC-001": true}
	params.PlanSeed = []PlanSeedSpec{{ID: "SPEC-001", Title: "Core"}}
	var events []string
	params.OnEvent = func(kind string, _ map[string]interface{}) { events = append(events, kind) }

	res, err := ExecuteScript(context.Background(), CouncilScript("M", nil), params)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.contracts) < 1 || rec.contracts[0] != "json:plan_manifest_patch" {
		t.Fatalf("first contract = %v, want seeded patch", rec.contracts)
	}
	for _, want := range []string{"Canonical plan manifest", "Engine-owned coverage slots", "SPEC-001", "not provisional tasks", "Use add_task"} {
		if !strings.Contains(rec.prompts[0], want) {
			t.Errorf("seeded architect prompt lacks %q:\n%s", want, rec.prompts[0])
		}
	}
	if !containsString(events, "plan_manifest_seeded") || !containsString(events, "plan_manifest_patched") {
		t.Fatalf("seed evidence events = %v", events)
	}
	if !strings.Contains(res.Text, "### T-001") {
		t.Fatalf("seeded plan result = %s", res.Text)
	}
	if rec.roles[0] != config.RoleArchitect || rec.roles[1] != config.RoleReviewer {
		t.Fatalf("seeded roles = %v", rec.roles)
	}
}

func TestUnassignedCoverageCannotBeApprovedByAReviewer(t *testing.T) {
	firstRaw := `{"operations":[{"op":"add_task","task_id":"T-001","milestone_id":"M-01","task":{"id":"T-001","title":"Build core","implements":["SPEC-001"],"work_unit":"build the core","acceptance_slices":["the core builds"],"acceptance_probes":["true"],"produces":["capability:core"],"consumes":[],"verification":"true"}}]}`
	firstPatch, err := agent.ParseContract("json:plan_manifest_patch", firstRaw)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRaw := `{"operations":[{"op":"add_task","task_id":"T-002","milestone_id":"M-01","task":{"id":"T-002","title":"Ship core","implements":["SPEC-002"],"work_unit":"ship the core","acceptance_slices":["the core ships"],"acceptance_probes":["true"],"produces":["capability:shipping"],"consumes":["capability:core"],"verification":"true"}}]}`
	resolvedPatch, err := agent.ParseContract("json:plan_manifest_patch", resolvedRaw)
	if err != nil {
		t.Fatal(err)
	}
	planText := "## M-01 — Implementation\n\n### T-001 — Build core\n\nBuild it.\n\n**Implements:** SPEC-001\n\n**Produces:** capability:core\n\n**Consumes:** none\n\n**Verification:** `true`\n\n### T-002 — Ship core\n\nShip it.\n\n**Implements:** SPEC-002\n\n**Produces:** capability:shipping\n\n**Consumes:** capability:core\n\n**Verification:** `true`"
	parsedPlan, err := agent.ParseContract("markdown_sections:M", planText)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	params := councilParams(rec,
		&agent.Outcome{Text: firstRaw, Parsed: firstPatch},
		verdictOutcome("approve"),
		&agent.Outcome{Text: resolvedRaw, Parsed: resolvedPatch},
		verdictOutcome("approve"),
		&agent.Outcome{Text: planText, Parsed: parsedPlan},
		verdictOutcome("approve"),
	)
	params.KnownIDs = map[string]bool{"SPEC-001": true, "SPEC-002": true}
	params.PlanSeed = []PlanSeedSpec{{ID: "SPEC-001", Title: "Core"}, {ID: "SPEC-002", Title: "Delivery"}}
	var events []string
	params.OnEvent = func(kind string, _ map[string]interface{}) { events = append(events, kind) }

	if _, err := ExecuteScript(context.Background(), CouncilScript("M", nil), params); err != nil {
		t.Fatal(err)
	}
	if !containsString(events, "plan_manifest_coverage_unassigned") {
		t.Fatalf("reviewer approval did not expose unassigned coverage: %v", events)
	}
	if len(rec.contracts) < 3 || rec.contracts[2] != "json:plan_manifest_patch" {
		t.Fatalf("unassigned coverage did not trigger a second focal patch: %v", rec.contracts)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
