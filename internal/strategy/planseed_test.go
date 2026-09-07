package strategy

import (
	"context"
	"encoding/json"
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
	if len(seed.Milestones) != 1 || len(seed.Milestones[0].Tasks) != 2 {
		t.Fatalf("seed = %#v, want one milestone and two in-scope tasks", seed)
	}
	tasks := seed.Milestones[0].Tasks
	if tasks[0].ID != "T-001" || tasks[0].Implements[0] != "SPEC-001" ||
		tasks[1].ID != "T-002" || tasks[1].Implements[0] != "SPEC-004" {
		t.Fatalf("seeded identities = %#v", tasks)
	}
	if got := unresolvedPlanSeedTasks(seed); strings.Join(got, ",") != "T-001,T-002" {
		t.Fatalf("unresolved tasks = %v", got)
	}
	raw, marshalErr := json.Marshal(seed)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if _, parseErr := agent.ParseContract("json:plan_manifest", string(raw)); parseErr != nil {
		t.Fatalf("engine seed must remain structurally parseable: %v", parseErr)
	}
}

func TestPlanSeedRefusesToInventGroupingBeyondTaskBoundary(t *testing.T) {
	specs := make([]PlanSeedSpec, 0, agent.MaxPlanManifestTasks+1)
	for i := 1; i <= agent.MaxPlanManifestTasks+1; i++ {
		specs = append(specs, PlanSeedSpec{ID: fmt.Sprintf("SPEC-%03d", i), Title: "Capability"})
	}
	_, err := seedPlanManifest(specs)
	if err == nil || !strings.Contains(err.Error(), "11 in-scope SPEC sections exceed the 10-task boundary") {
		t.Fatalf("oversized seed error = %v", err)
	}
}

func TestPlanSeedCoverageCannotDisappearThroughARepair(t *testing.T) {
	seed, err := seedPlanManifest([]PlanSeedSpec{{ID: "SPEC-001"}, {ID: "SPEC-002"}})
	if err != nil {
		t.Fatal(err)
	}
	seed.Milestones[0].Tasks = seed.Milestones[0].Tasks[:1]
	if got := missingPlanSeedCoverage(seed, []string{"SPEC-001", "SPEC-002"}); strings.Join(got, ",") != "SPEC-002" {
		t.Fatalf("missing coverage = %v", got)
	}
}

func TestFirstPlanStartsAsAFocalPatchOverTheEngineSeed(t *testing.T) {
	patchText := `{"operations":[{"op":"replace_task","task_id":"T-001","milestone_id":"M-01","task":{"id":"T-001","title":"Build core","implements":["SPEC-001"],"work_unit":"build the core","acceptance_slices":["the core builds"],"acceptance_probes":["true"],"produces":["capability:core"],"consumes":[],"verification":"true"}}]}`
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
	for _, want := range []string{"Canonical plan manifest", "Seeded checkpoint", "T-001", "SPEC-001"} {
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

func TestUnresolvedSeedCannotBeApprovedByAReviewer(t *testing.T) {
	seed, err := seedPlanManifest([]PlanSeedSpec{{ID: "SPEC-001", Title: "Core"}})
	if err != nil {
		t.Fatal(err)
	}
	provisional := seed.Milestones[0].Tasks[0]
	firstPatch := &agent.PlanManifestPatch{Operations: []agent.PlanManifestPatchOperation{{
		Op: "replace_task", TaskID: provisional.ID, MilestoneID: "M-01", Task: &provisional,
	}}}
	firstRaw, _ := json.Marshal(firstPatch)
	resolvedRaw := `{"operations":[{"op":"replace_task","task_id":"T-001","milestone_id":"M-01","task":{"id":"T-001","title":"Build core","implements":["SPEC-001"],"work_unit":"build the core","acceptance_slices":["the core builds"],"acceptance_probes":["true"],"produces":["capability:core"],"consumes":[],"verification":"true"}}]}`
	resolvedPatch, err := agent.ParseContract("json:plan_manifest_patch", resolvedRaw)
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
		&agent.Outcome{Text: string(firstRaw), Parsed: firstPatch},
		verdictOutcome("approve"),
		&agent.Outcome{Text: resolvedRaw, Parsed: resolvedPatch},
		verdictOutcome("approve"),
		&agent.Outcome{Text: planText, Parsed: parsedPlan},
		verdictOutcome("approve"),
	)
	params.KnownIDs = map[string]bool{"SPEC-001": true}
	params.PlanSeed = []PlanSeedSpec{{ID: "SPEC-001", Title: "Core"}}
	var events []string
	params.OnEvent = func(kind string, _ map[string]interface{}) { events = append(events, kind) }

	if _, err := ExecuteScript(context.Background(), CouncilScript("M", nil), params); err != nil {
		t.Fatal(err)
	}
	if !containsString(events, "plan_manifest_seed_unresolved") {
		t.Fatalf("reviewer approval did not expose unresolved seed: %v", events)
	}
	if len(rec.contracts) < 3 || rec.contracts[2] != "json:plan_manifest_patch" {
		t.Fatalf("unresolved seed did not trigger a second focal patch: %v", rec.contracts)
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
