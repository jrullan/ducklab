package strategy

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
)

func TestPlanManifestSliceLimitBelongsToSmallSupportProfile(t *testing.T) {
	manifest := &agent.PlanManifest{Milestones: []agent.ManifestMilestone{{
		ID: "M-01", Title: "Core", Tasks: []agent.ManifestTask{{
			ID: "T-001", AcceptanceSlices: []string{"one", "two", "three", "four"},
		}},
	}}}
	if err := validatePlanManifestSupportProfile(manifest, false); err != nil {
		t.Fatalf("standard profile inherited small-seat slice limit: %v", err)
	}
	if err := validatePlanManifestSupportProfile(manifest, true); err == nil || !strings.Contains(err.Error(), "want at most 3 for a small seat") {
		t.Fatalf("small profile slice limit error = %v", err)
	}
}

func TestPlanManifestReviewGuidanceRespectsSupportProfile(t *testing.T) {
	small := planManifestSemanticReviewFor(true)
	standard := planManifestSemanticReviewFor(false)
	if !strings.Contains(small, "1–3 independently observable slices") {
		t.Fatalf("small review guidance lost slice ceiling:\n%s", small)
	}
	if strings.Contains(standard, "1–3 independently observable slices") || strings.Contains(standard, "fit in three bullets") {
		t.Fatalf("standard review guidance inherited small-seat slice rule:\n%s", standard)
	}
	if !strings.Contains(standard, "Do not split or merge tasks solely because of their slice count") {
		t.Fatalf("standard review guidance lacks cohesion rule:\n%s", standard)
	}
	for _, want := range []string{"Trace every produced source artifact", "independently discoverable test", "final integration", "Tests and fixtures are evidence", "does not make earlier", "verdict cannot be approve"} {
		if !strings.Contains(standard, want) {
			t.Fatalf("standard review guidance lacks reachability rule %q:\n%s", want, standard)
		}
	}
}

func TestPlanCoverageReviewSeparatesEvidenceFromImplementation(t *testing.T) {
	for _, want := range []string{
		"Tests and fixtures can prove behavior but cannot be its only implementation",
		"production source, library, executable or capability",
		"Any unresolved gap you identify in your analysis must remain a finding",
	} {
		if !strings.Contains(planCoverageReview, want) {
			t.Fatalf("plan coverage guidance lacks %q:\n%s", want, planCoverageReview)
		}
	}
}

func TestApplySupportProfileCarriesPolicyOnScheduledTurn(t *testing.T) {
	turn := &Turn{Persona: PersonaPlanManifest, Toolbelt: "none"}
	applySupportProfile(turn, true)
	if !turn.SmallSeat {
		t.Fatal("small support profile was not carried on the scheduled turn")
	}
	applySupportProfile(turn, false)
	if turn.SmallSeat {
		t.Fatal("standard support profile retained stale small-seat policy")
	}
}

func TestAgentTurnCarriesScheduledSupportPolicy(t *testing.T) {
	cache := &agent.ManifestAuditCache{}
	called := false
	turn := &Turn{Role: config.RoleArchitect, Persona: PersonaPlanManifest, Contract: "json:plan_manifest", SmallSeat: true, Images: []string{"image"},
		ManifestAuditInputs: map[string]json.RawMessage{"T-001": json.RawMessage(`{"task":"one"}`)}, ManifestAuditPolicy: "policy", ManifestAuditCache: cache,
		OnManifestAuditCacheHit: func(_, _ string) { called = true }}
	agentTurn := turn.AgentTurn("duck", "prompt", []string{"fs_read"}, 2, 3)
	if !agentTurn.SmallSeat || agentTurn.Persona != PersonaPlanManifest || agentTurn.Round != 2 || agentTurn.Index != 3 || len(agentTurn.Images) != 1 ||
		agentTurn.ManifestAuditCache != cache || agentTurn.ManifestAuditPolicy != "policy" || len(agentTurn.ManifestAuditInputs) != 1 {
		t.Fatalf("agent turn lost scheduled policy: %+v", agentTurn)
	}
	agentTurn.OnManifestAuditCacheHit("T-001", "key")
	if !called {
		t.Fatal("agent turn lost manifest cache event callback")
	}
}

// Plan rules are checked at task granularity: every task names what it
// implements, a small seat's task carries at most three deliverables, and
// milestone lanes do not overlap. The reviewer caught all three on
// benchmark run 4; the harness did not.
func TestPlanStructureIsCheckedPerTask(t *testing.T) {
	raw := "## M-01 — Core\n\n**Owns:** src/\n\n### T-001 — Scaffold\n\n**Implements:** SPEC-001\n\n**Deliverables:**\n- a\n- b\n- c\n- d\n\n### T-002 — Shell\n\n**Deliverables:**\n- a\n\n## M-02 — UI\n\n**Owns:** src/ui/\n\n### T-003 — Window\n\n**Implements:** SPEC-001\n\n**Deliverables:**\n- a\n"
	cur := []agent.Section{
		{ID: "M-01", Title: "Core", Body: "**Owns:** src/\n\n### T-001 — Scaffold\n\n**Implements:** SPEC-001\n\n**Deliverables:**\n- a\n- b\n- c\n- d\n\n### T-002 — Shell\n\n**Deliverables:**\n- a\n"},
		{ID: "M-02", Title: "UI", Body: "**Owns:** src/ui/\n\n### T-003 — Window\n\n**Implements:** SPEC-001\n\n**Deliverables:**\n- a\n"},
	}
	findings := structureFindings(nil, cur, "markdown_sections:M", map[string]bool{"SPEC-001": true}, true, raw)
	joined := strings.Join(findings, "\n")
	for _, want := range []string{"T-002 has no **Implements:** line", "T-001 uses legacy **Deliverables:**", "T-001 has no top-level **Acceptance slices:**", "T-001 has no **Verification:** line", "lane collision"} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings lack %q:\n%s", want, joined)
		}
	}
	// A full seat is not portioned.
	big := structureFindings(nil, cur, "markdown_sections:M", map[string]bool{"SPEC-001": true}, false, raw)
	if strings.Contains(strings.Join(big, "\n"), "top-level **Deliverables:**") {
		t.Fatalf("a full seat's plan was portioned: %v", big)
	}
}

func TestPlanStructureRejectsWorkspaceRootOwnership(t *testing.T) {
	raw := "## M-01 — Root\n\n**Owns:** .\n\n### T-001 — Build\n\n**Implements:** SPEC-001\n\n**Work unit:** build\n\n**Acceptance slices:**\n- builds\n\n**Acceptance probes:**\n1. `true`\n\n**Produces:** file:Cargo.toml\n\n**Consumes:** none\n\n**Verification:** `true`\n\n**Exercises:** file:Cargo.toml"
	parsed, err := agent.ParseContract("markdown_sections:M", raw)
	if err != nil {
		t.Fatal(err)
	}
	findings := structureFindings(nil, parsed.([]agent.Section), "markdown_sections:M", map[string]bool{"SPEC-001": true}, true, raw)
	if !slices.ContainsFunc(findings, func(f string) bool { return strings.Contains(f, "cannot claim workspace root") }) {
		t.Fatalf("root Owns lane passed: %v", findings)
	}
}

func TestSmallPlanV2CapsAtomicAcceptanceSlices(t *testing.T) {
	body := "### T-001 — Save capture\n\n**Implements:** SPEC-001\n\n**Work unit:** Persist one completed capture\n\n**Acceptance slices:**\n- Opens a save destination\n- Writes a valid PNG\n- Reports success\n- Reports failure\n\n**Produces:** src/save.c\n\n**Consumes:** none\n\n**Verification:** `cc -fsyntax-only src/save.c`\n\n**Exercises:** src/save.c"
	cur := []agent.Section{{ID: "M-01", Title: "Save", Body: body}}
	joined := strings.Join(structureFindings(nil, cur, "markdown_sections:M", map[string]bool{"SPEC-001": true}, true, "## M-01 — Save\n\n"+body), "\n")
	if !strings.Contains(joined, "4 top-level **Acceptance slices:**") {
		t.Fatalf("findings = %s", joined)
	}
}

func TestAcceptanceProbesMapOneCommandToEachSlice(t *testing.T) {
	body := "**Implements:** SPEC-001\n\n**Work unit:** Parse CLI options\n\n" +
		"**Acceptance slices:**\n- --help exits zero\n- --save without a path fails\n\n" +
		"**Acceptance probes:**\n1. `./app --help`\n2. prose without a command\n\n" +
		"**Produces:** file:src/cli/parser.c\n\n**Consumes:** none\n\n" +
		"**Verification:** `cc -fsyntax-only src/cli/parser.c`\n\n**Exercises:** file:src/cli/parser.c"
	findings := structureFindings(nil, []agent.Section{{ID: "T-010", Body: body}}, "markdown_sections:T", map[string]bool{"SPEC-001": true}, true, body)
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "Acceptance probes") || !strings.Contains(joined, "one backtick command") {
		t.Fatalf("invalid probe mapping was accepted: %v", findings)
	}

	valid := strings.Replace(body, "2. prose without a command", "2. `! ./app --save`", 1)
	findings = structureFindings(nil, []agent.Section{{ID: "T-010", Body: valid}}, "markdown_sections:T", map[string]bool{"SPEC-001": true}, true, valid)
	if slices.ContainsFunc(findings, func(f string) bool { return strings.Contains(f, "Acceptance probes") }) {
		t.Fatalf("valid probe mapping was rejected: %v", findings)
	}
}

func TestPlanFieldNamesMentionedInlineAreNotParsedAsFields(t *testing.T) {
	body := "**Implements:** SPEC-001\n\n**Work unit:** Migrate one task format\n\n**Acceptance slices:**\n- The text `**Deliverables:**` is absent as a field marker\n\n**Produces:** plan.md\n\n**Consumes:** none\n\n**Verification:** `grep -q 'Work unit' plan.md`\n\n**Exercises:** plan.md"
	got := strings.Join(structureFindings(nil, []agent.Section{{ID: "T-900", Body: body}}, "markdown_sections:T", map[string]bool{"SPEC-001": true}, true, ""), "\n")
	if strings.Contains(got, "uses legacy **Deliverables:**") {
		t.Fatalf("inline documentation was mistaken for a structural field:\n%s", got)
	}
}

func TestAcceptanceSlicesMayUseAnOrderedMarkdownList(t *testing.T) {
	body := "**Acceptance slices:**\n1. Opens the dialog\n2. Writes the file\n3. Reports completion\n  1. nested explanation"
	if got := topLevelChecklistItems(body, "Acceptance slices"); got != 3 {
		t.Fatalf("ordered acceptance slices = %d, want 3", got)
	}
	if got := nestedChecklistItems(body, "Acceptance slices"); got != 1 {
		t.Fatalf("nested acceptance slices = %d, want 1", got)
	}
}

func TestPlanRejectsNestedAndCompressedAcceptanceSlices(t *testing.T) {
	previous := "**Implements:** SPEC-001\n\n**Work unit:** Persist capture\n\n**Acceptance slices:**\n- Opens a destination\n- Writes a PNG\n- Reports completion\n\n**Acceptance probes:**\n1. `test-dialog`\n2. `test-write`\n3. `test-complete`\n\n**Produces:** file:save.c\n\n**Consumes:** none\n\n**Verification:** `test-all`\n\n**Exercises:** file:save.c"
	current := strings.Replace(previous, "- Opens a destination\n- Writes a PNG\n- Reports completion", "- Persists a capture\n  - opens a destination\n  - writes a PNG\n  - reports completion", 1)
	findings := structureFindings([]agent.Section{{ID: "T-001", Body: previous}}, []agent.Section{{ID: "T-001", Body: current}}, "markdown_sections:T", map[string]bool{"SPEC-001": true}, true, current)
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "must be a flat list") || !strings.Contains(joined, "reduced **Acceptance slices:** from 3 to 1") {
		t.Fatalf("compressed nested slices escaped validation:\n%s", joined)
	}
}

func TestTopLevelTaskContractEnforcesAcceptanceSlicesV2(t *testing.T) {
	body := "**Implements:** SPEC-001\n\nWork unit: unbolded and therefore not the contract\n\n**Produces:** src/main.c\n\n**Consumes:** none\n\n**Verification:** `cc -fsyntax-only src/main.c`\n\n**Exercises:** src/main.c"
	got := strings.Join(structureFindings(nil, []agent.Section{{ID: "T-009", Title: "Save", Body: body}}, "markdown_sections:T", nil, true, ""), "\n")
	for _, want := range []string{"T-009 has no **Work unit:**", "T-009 has no top-level **Acceptance slices:**"} {
		if !strings.Contains(got, want) {
			t.Errorf("top-level task findings lack %q:\n%s", want, got)
		}
	}
}

func TestTopLevelTaskDoesNotDuplicateMissingImplementsFinding(t *testing.T) {
	body := "**Work unit:** Save one file\n\n**Acceptance slices:**\n- File is saved"
	got := strings.Join(structureFindings(nil, []agent.Section{{ID: "T-900", Body: body}}, "markdown_sections:T", nil, true, ""), "\n")
	if strings.Count(got, "T-900 has no **Implements:** line") != 1 {
		t.Fatalf("missing Implements finding duplicated:\n%s", got)
	}
}

func TestPlanTaskMustImplementASpecificationSection(t *testing.T) {
	body := "**Implements:** REQ-001\n\n**Work unit:** Save one file\n\n**Acceptance slices:**\n- File is saved\n\n**Produces:** src/save.c\n\n**Consumes:** none\n\n**Verification:** `cc -fsyntax-only src/save.c`\n\n**Exercises:** src/save.c"
	got := strings.Join(structureFindings(nil, []agent.Section{{ID: "T-010", Body: body}}, "markdown_sections:T", map[string]bool{"REQ-001": true}, true, ""), "\n")
	if !strings.Contains(got, "names no SPEC-NNN section") {
		t.Fatalf("requirements-only Implements passed a plan task:\n%s", got)
	}
}

func TestPlanUnknownReferenceFindingTargetsNestedTask(t *testing.T) {
	body := "### T-002 — Registry\n\n**Implements:** SPEC-002, SPEC-010\n\n**Work unit:** Register providers\n\n**Acceptance slices:**\n- Duplicates fail\n\n**Produces:** src/registry.rs\n\n**Consumes:** none\n\n**Verification:** `cargo check`\n\n**Exercises:** src/registry.rs"
	findings := structureFindings(nil, []agent.Section{{ID: "M-01", Title: "Core", Body: body}}, "markdown_sections:M", map[string]bool{"SPEC-002": true}, true, "## M-01 — Core\n\n"+body)
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "T-002 implements SPEC-010") || strings.Contains(joined, "M-01 implements SPEC-010") {
		t.Fatalf("dangling reference lost task attribution:\n%s", joined)
	}
}

func TestPlanManifestReferencesNormalizeBeforeFreeze(t *testing.T) {
	manifest := &agent.PlanManifest{Milestones: []agent.ManifestMilestone{{
		ID: "M-01", Title: "Core", Tasks: []agent.ManifestTask{{
			ID: "T-002", Implements: []string{"SPEC-002", "SPEC-010"}, AcceptanceProbes: []string{"cargo test"},
		}},
	}}}
	removed, err := normalizePlanManifestReferences(manifest, map[string]bool{"SPEC-002": true})
	if err != nil || removed != 1 || !slices.Equal(manifest.Milestones[0].Tasks[0].Implements, []string{"SPEC-002"}) {
		t.Fatalf("normalize removed=%d err=%v manifest=%+v", removed, err, manifest)
	}
	manifest.Milestones[0].Tasks[0].Implements = []string{"SPEC-010"}
	if _, err := normalizePlanManifestReferences(manifest, map[string]bool{"SPEC-002": true}); err == nil || !strings.Contains(err.Error(), "T-002") {
		t.Fatalf("all-invalid manifest error = %v", err)
	}
}

func TestPlanManifestPatchPreservesUnmentionedTasksAndRevalidatesWholeGraph(t *testing.T) {
	baseText := `{"milestones":[{"id":"M-01","title":"Core","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build","acceptance_slices":["builds"],"acceptance_probes":["cargo check"],"produces":["file:Cargo.toml"],"consumes":[],"verification":"cargo check"},{"id":"T-002","title":"Govern","implements":["SPEC-001"],"work_unit":"validate provenance","acceptance_slices":["invalid provenance is rejected"],"acceptance_probes":["cargo test provenance"],"produces":["file:src/govern.rs"],"consumes":["file:Cargo.toml"],"verification":"cargo test provenance"}]}]}`
	parsedBase, err := agent.ParseContract("json:plan_manifest", baseText)
	if err != nil {
		t.Fatal(err)
	}
	patchText := `{"operations":[{"op":"replace_task","task_id":"T-002","milestone_id":"M-01","task":{"id":"T-002","title":"Govern installation","implements":["SPEC-001"],"work_unit":"return provenance and fixture evidence without installing","acceptance_slices":["Ducklab receives evidence and remains the installation authority"],"acceptance_probes":["cargo test installation_authority"],"produces":["file:src/govern.rs"],"consumes":["file:Cargo.toml"],"verification":"cargo test installation_authority"}}]}`
	parsedPatch, err := agent.ParseContract("json:plan_manifest_patch", patchText)
	if err != nil {
		t.Fatal(err)
	}
	got, operations, err := applyPlanManifestPatch(parsedBase.(*agent.PlanManifest), parsedPatch.(*agent.PlanManifestPatch))
	if err != nil {
		t.Fatal(err)
	}
	if operations != 1 || len(got.Milestones) != 1 || len(got.Milestones[0].Tasks) != 2 {
		t.Fatalf("patched manifest = %#v", got)
	}
	if got.Milestones[0].Tasks[0].Title != parsedBase.(*agent.PlanManifest).Milestones[0].Tasks[0].Title ||
		got.Milestones[0].Tasks[0].WorkUnit != parsedBase.(*agent.PlanManifest).Milestones[0].Tasks[0].WorkUnit ||
		!slices.Equal(got.Milestones[0].Tasks[0].Produces, parsedBase.(*agent.PlanManifest).Milestones[0].Tasks[0].Produces) {
		t.Error("unmentioned T-001 changed")
	}
	if got.Milestones[0].Tasks[1].Title != "Govern installation" {
		t.Errorf("replacement was not applied: %#v", got.Milestones[0].Tasks[1])
	}

	deleteEverything := &agent.PlanManifestPatch{Operations: []agent.PlanManifestPatchOperation{{
		Op: "delete_task", TaskID: "T-001",
	}, {
		Op: "delete_task", TaskID: "T-002",
	}}}
	if _, _, err := applyPlanManifestPatch(parsedBase.(*agent.PlanManifest), deleteEverything); err == nil || !strings.Contains(err.Error(), "patched manifest is invalid") {
		t.Fatalf("globally invalid patch error = %v", err)
	}
	if len(parsedBase.(*agent.PlanManifest).Milestones[0].Tasks) != 2 {
		t.Fatal("a rejected patch mutated the canonical base manifest")
	}
}

func TestPlanManifestPatchReportsIndependentFieldErrorsTogether(t *testing.T) {
	baseText := `{"milestones":[{"id":"M-01","title":"Core","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build","acceptance_slices":["builds"],"acceptance_probes":["cargo check"],"produces":["file:Cargo.toml"],"consumes":[],"verification":"cargo check"}]}]}`
	parsedBase, err := agent.ParseContract("json:plan_manifest", baseText)
	if err != nil {
		t.Fatal(err)
	}
	patch := &agent.PlanManifestPatch{Operations: []agent.PlanManifestPatchOperation{{
		Op:          "replace_task",
		TaskID:      "T-001",
		MilestoneID: "M-01",
		Task: &agent.ManifestTask{
			ID:               "T-001",
			Title:            "",
			Implements:       []string{"REQ-001"},
			WorkUnit:         "",
			AcceptanceSlices: []string{"builds", "installs"},
			AcceptanceProbes: []string{"cargo check"},
			Produces:         []string{"Cargo.toml"},
			Verification:     "",
		},
	}}}
	_, _, err = applyPlanManifestPatch(parsedBase.(*agent.PlanManifest), patch)
	if err == nil {
		t.Fatal("invalid patch unexpectedly passed")
	}
	message := err.Error()
	for _, want := range []string{
		"T-001 title must not be empty",
		"T-001 work_unit must not be empty",
		"T-001 acceptance_probes has 1 items, want 2",
		`T-001 implements invalid specification id "REQ-001"`,
		`T-001 produced artifact "Cargo.toml"`,
		"T-001 verification must not be empty",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("patch error lacks %q:\n%s", want, message)
		}
	}
}

func TestPlanManifestPatchAddsMilestoneAndTaskInOneTransaction(t *testing.T) {
	baseText := `{"milestones":[{"id":"M-01","title":"Core","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build","acceptance_slices":["builds"],"acceptance_probes":["true"],"produces":["capability:core"],"consumes":[],"verification":"true"}]}]}`
	baseParsed, err := agent.ParseContract("json:plan_manifest", baseText)
	if err != nil {
		t.Fatal(err)
	}
	patchText := `{"operations":[{"op":"add_task","task_id":"T-002","milestone_id":"M-02","task":{"id":"T-002","title":"Run","implements":["SPEC-002"],"work_unit":"run","acceptance_slices":["runs"],"acceptance_probes":["true"],"produces":["capability:runtime"],"consumes":["capability:core"],"verification":"true"}},{"op":"add_milestone","milestone_id":"M-02","milestone_title":"Runtime"}]}`
	patchParsed, err := agent.ParseContract("json:plan_manifest_patch", patchText)
	if err != nil {
		t.Fatal(err)
	}
	got, operations, err := applyPlanManifestPatch(baseParsed.(*agent.PlanManifest), patchParsed.(*agent.PlanManifestPatch))
	if err != nil {
		t.Fatal(err)
	}
	if operations != 2 || len(got.Milestones) != 2 || got.Milestones[1].ID != "M-02" || len(got.Milestones[1].Tasks) != 1 {
		t.Fatalf("patched manifest = %#v", got)
	}
}

func TestStructureRepairExplainsExecutableVerificationAndArtifactExercises(t *testing.T) {
	findings := []string{
		"T-900 **Verification:** must put the executable command in backticks; prose is never executed",
		"T-900 **Exercises:** none of its **Produces:** artifacts",
	}
	note, _ := structureRepairInstruction(findings, []agent.Section{{ID: "T-900", Body: "**Produces:** src/main.c, capability:main-loop"}})
	for _, want := range []string{"field `Verification`", "ONLY one executable shell command", "`cc -fsyntax-only src/main.c`", "field Exercises", "copied exactly from allowed_values", `"src/main.c"`, `"capability:main-loop"`} {
		if !strings.Contains(note, want) {
			t.Errorf("repair prompt lacks %q:\n%s", want, note)
		}
	}
}

// Neocapture corrida r-20260903-205418-nlqc reached task 6/6, but four
// structure attempts kept replacing Exercises with plausible filenames that
// did not literally occur in Produces. Exercise membership is closed-world,
// so its exact set must travel with the tool-less repair contract.
func TestNestedPlanExercisesRepairCarriesExactProducedValues(t *testing.T) {
	sections := []agent.Section{{
		ID:   "M-06",
		Body: "### T-009 — Lifecycle\n\n**Produces:** capability:managed-main-loop, file:src/app/lifecycle.c\n\n**Exercises:** lifecycle_handler.c",
	}}
	note, ids := structureRepairInstruction(
		[]string{"T-009 **Exercises:** none of its **Produces:** artifacts"},
		sections,
	)
	if len(ids) != 1 || ids[0] != "M-06" {
		t.Fatalf("repair assignment = %v, want [M-06]", ids)
	}
	for _, want := range []string{
		`"field":"Exercises","value":"capability:managed-main-loop"`,
		`"allowed_values"`,
		`"capability:managed-main-loop"`,
		`"file:src/app/lifecycle.c"`,
		"do not invent aliases",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("repair prompt lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, `"lifecycle_handler.c"`) {
		t.Fatalf("invalid current Exercises value was advertised as admissible:\n%s", note)
	}
}

// Frozen Neocapture attempts 2/3 and 3/3 taught the bounded repair to copy
// REQ-001 from its schema example even though plan tasks may only implement
// accepted SPEC sections. A tool-less repair must receive the closed set.
func TestPlanRepairCarriesAllowedSpecificationIDs(t *testing.T) {
	findings := []string{"T-900 **Implements:** names no SPEC-NNN section — plan tasks implement accepted specification contracts, not requirements or milestones"}
	note, _ := structureRepairInstruction(findings, []agent.Section{{ID: "T-900", Body: "**Implements:** REQ-001"}}, structureRepairContext{
		Contract: "markdown_sections:T",
		KnownIDs: map[string]bool{
			"REQ-001":  true,
			"SPEC-001": true,
			"SPEC-005": true,
			"SPEC-006": false,
		},
	})
	for _, want := range []string{`"field":"Implements","value":"SPEC-001"`, `"code": "invalid_implements_reference"`, `"allowed_values"`, `"SPEC-001"`, `"SPEC-005"`, "do not invent an ID"} {
		if !strings.Contains(note, want) {
			t.Errorf("repair prompt lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, `"value":"REQ-001"`) || strings.Contains(note, `"SPEC-006"`) {
		t.Fatalf("repair prompt teaches a forbidden or nonexistent reference:\n%s", note)
	}
}

// Frozen attempt 1/3 stalled because these v2 fields had findings but no
// operation-level recipe. Both are expressible as bounded set_field patches.
func TestPlanRepairExplainsAcceptanceSliceV2Fields(t *testing.T) {
	findings := []string{
		"T-008 has no **Work unit:** — name exactly one cohesive capability or concern; split independent concerns into separate tasks",
		"T-008 has no top-level **Acceptance slices:** bullets — name observable outcomes of its single Work unit",
	}
	note, _ := structureRepairInstruction(findings, []agent.Section{{ID: "T-008", Body: "**Implements:** SPEC-001"}}, structureRepairContext{
		Contract: "markdown_sections:T",
		KnownIDs: map[string]bool{"SPEC-001": true},
	})
	for _, want := range []string{"invalid_work_unit", "field `Work unit`", "invalid_acceptance_slices", "field `Acceptance slices`", "1-3 flat, top-level Markdown list items"} {
		if !strings.Contains(note, want) {
			t.Errorf("repair prompt lacks %q:\n%s", want, note)
		}
	}
}

func TestEveryStructureFindingHasAToollessRecipe(t *testing.T) {
	findings := []string{
		"T-001 has no **Produces:** artifacts",
		"T-001 consumes file:x produced by T-002 but has no **Depends on:** T-002",
		"M-01: lane collision — src overlaps src/ui",
		"T-003 appears twice",
		"SPEC-002 was in your previous draft and is gone",
		"custom project inspector finding",
	}
	for _, finding := range findings {
		descriptor := describeStructureRepairFinding(finding, structureRepairContext{})
		if descriptor.Code == "" || descriptor.Recipe == "" {
			t.Errorf("finding has no repair contract: %+v", descriptor)
		}
	}
}

func TestAcceptanceSlicesCanBeSetWithOnePatchOperation(t *testing.T) {
	baseText := "## T-008 — Overlay\n\n**Implements:** SPEC-001"
	baseParsed, err := agent.ParseContract("markdown_sections:T", baseText)
	if err != nil {
		t.Fatal(err)
	}
	base := &agent.Outcome{Text: baseText, Parsed: baseParsed}
	patch := &agent.Outcome{Parsed: map[string]interface{}{
		"sections": []interface{}{"T-008"},
		"operations": []interface{}{map[string]interface{}{
			"op": "set_field", "target": "T-008", "field": "Acceptance slices", "value": "\n- Overlay opens\n- Escape closes it",
		}},
	}}
	merged, err := applyStructurePatch(base, patch, "markdown_sections:T", []string{"T-008"})
	if err != nil {
		t.Fatal(err)
	}
	if got := topLevelChecklistItems(sectionsOf(merged)[0].Body, "Acceptance slices"); got != 2 {
		t.Fatalf("patched acceptance slices = %d, want 2:\n%s", got, merged.Text)
	}
}

func TestIsolatedArchitectOutcomeDropsSiblingTasks(t *testing.T) {
	raw := "## T-008 — CLI\n\n**Implements:** SPEC-001\n\nright\n\n## T-009 — Lifecycle\n\n**Implements:** SPEC-001\n\nwrong sibling"
	parsed, err := agent.ParseContract("markdown_sections:T", raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scopeArchitectSection(&agent.Outcome{Text: raw, Parsed: parsed}, "markdown_sections:T", "T-008", "CLI")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Text, "T-009") || len(sectionsOf(got)) != 1 || sectionsOf(got)[0].ID != "T-008" {
		t.Fatalf("scoped outcome = %#v", got)
	}
}

func TestIsolatedArchitectOutcomeRestoresEngineOwnedID(t *testing.T) {
	raw := "## T-010 — Lifecycle\n\n**Implements:** SPEC-001\n\nrenumbered by bad review advice"
	parsed, err := agent.ParseContract("markdown_sections:T", raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scopeArchitectSection(&agent.Outcome{Text: raw, Parsed: parsed}, "markdown_sections:T", "T-008", "Lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	if len(sectionsOf(got)) != 1 || sectionsOf(got)[0].ID != "T-008" || strings.Contains(got.Text, "T-010") {
		t.Fatalf("normalized outcome = %#v", got)
	}
}

func TestIsolatedNewTaskFallsBackToItsUniqueAssignedTitle(t *testing.T) {
	raw := "## T-006 — GtkApplication Initialization\n\n**Implements:** SPEC-001\n\nright\n\n## T-007 — CLI Parsing\n\n**Implements:** SPEC-001\n\nwrong"
	parsed, err := agent.ParseContract("markdown_sections:T", raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scopeArchitectSection(&agent.Outcome{Text: raw, Parsed: parsed}, "markdown_sections:T", "T-900", "GtkApplication Initialization")
	if err != nil {
		t.Fatal(err)
	}
	if len(sectionsOf(got)) != 1 || sectionsOf(got)[0].ID != "T-900" || !strings.Contains(got.Text, "right") || strings.Contains(got.Text, "wrong") {
		t.Fatalf("title-scoped new task = %#v", got)
	}
}

func TestTaskGraphRequiresDependencyAndVerificationCoverage(t *testing.T) {
	blocks := []taskBlock{
		{id: "T-001", body: "**Produces:** meson.build\n**Consumes:** src/main.c\n**Depends on:** none\n**Exercises:** meson.build\n"},
		{id: "T-002", body: "**Produces:** src/main.c\n**Exercises:** something-else\n"},
	}
	joined := strings.Join(taskGraphFindings(blocks), "\n")
	if !strings.Contains(joined, "T-001 consumes src/main.c produced by T-002") {
		t.Fatalf("task graph did not require the producer dependency: %s", joined)
	}
	if itemsOverlap(taskFieldItems(blocks[1].body, "Produces"), taskFieldItems(blocks[1].body, "Exercises")) {
		t.Fatal("unrelated Exercises artifact was treated as coverage")
	}
}

func TestPlanRejectsSingleOutputCompileWithMultipleInputs(t *testing.T) {
	bad := "gcc -c src/backend/x11_capture.c src/backend/capture_backend.h -o /dev/null"
	if !invalidSingleOutputCompile(bad) {
		t.Fatal("known-invalid gcc command was accepted")
	}
	for _, good := range []string{
		"gcc -c src/backend/x11_capture.c -o /dev/null",
		"gcc -c src/backend/x11_capture.c src/backend/other.c",
		"cc -fsyntax-only src/backend/x11_capture.c",
	} {
		if invalidSingleOutputCompile(good) {
			t.Errorf("valid command was rejected: %s", good)
		}
	}

	doc := &artifact.Document{Front: artifact.Frontmatter{Kind: artifact.KindPlan}, Sections: []artifact.Section{{
		ID: "M-001", Children: []artifact.Section{{
			ID: "T-004", Body: "**Verification:** `" + bad + "`",
		}},
	}}}
	joined := strings.Join(ProposalStructureFindings(doc), "\n")
	if !strings.Contains(joined, "T-004") || !strings.Contains(joined, "multiple input files") {
		t.Fatalf("materialized proposal did not block invalid verification: %s", joined)
	}
}

func TestPlanAmendmentStructureTreatsInheritedDefectsAsNotices(t *testing.T) {
	legacyTask := artifact.Section{
		ID: "T-001", Title: "Legacy build", Body: "**Deliverables:** file:legacy.go",
		FieldErrors: []artifact.FieldError{{ID: "T-001", Key: "Deliverables", Token: "Deliverables"}},
	}
	base := &artifact.Document{
		Front:       artifact.Frontmatter{Kind: artifact.KindPlan, Grammar: artifact.CurrentGrammar},
		Sections:    []artifact.Section{{ID: "M-001", Title: "Core", Children: []artifact.Section{legacyTask}}},
		FieldErrors: append([]artifact.FieldError(nil), legacyTask.FieldErrors...),
	}
	added := artifact.Section{
		ID: "T-002", Title: "New work",
		Body: "**Acceptance slices:**\n- one\n  - hidden two\n",
	}
	proposed := &artifact.Document{
		Front:       base.Front,
		Sections:    []artifact.Section{{ID: "M-001", Title: "Core", Children: []artifact.Section{legacyTask, added}}},
		FieldErrors: append([]artifact.FieldError(nil), legacyTask.FieldErrors...),
	}

	blockers, notices := ProposalStructureFindingsForAmendment(base, proposed)
	if joined := strings.Join(blockers, "\n"); strings.Contains(joined, "Deliverables") || !strings.Contains(joined, "T-002") {
		t.Fatalf("amendment blockers = %v, want only newly introduced defects", blockers)
	}
	if joined := strings.Join(notices, "\n"); !strings.Contains(joined, "T-001") || !strings.Contains(joined, "Deliverables") {
		t.Fatalf("amendment notices lost inherited legacy defect: %v", notices)
	}
}

func TestManifestRendersDistinctAuthoredProbesWithoutCloningVerification(t *testing.T) {
	manifest := &agent.PlanManifest{Milestones: []agent.ManifestMilestone{{ID: "M-01", Title: "Core", Tasks: []agent.ManifestTask{{
		ID: "T-001", Title: "Compose", Implements: []string{"SPEC-001"}, WorkUnit: "Compose results",
		AcceptanceSlices: []string{"Orders results", "Normalizes errors", "Chooses a gate"},
		AcceptanceProbes: []string{"cargo test ordering", "cargo test errors", "cargo test gate"},
		Produces:         []string{"file:src/composition.rs"}, Verification: "cargo test composition",
	}}}}}
	raw := "## M-01 — Core\n\n### T-001 — Compose\n\nDraft prose."
	parsed, err := agent.ParseContract("markdown_sections:M", raw)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := reconcilePlanManifest(&agent.Outcome{Text: raw, Parsed: parsed}, manifest, "markdown_sections:M")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range manifest.Milestones[0].Tasks[0].AcceptanceProbes {
		if strings.Count(got.Text, "`"+command+"`") != 1 {
			t.Fatalf("authored probe %q was not rendered exactly once:\n%s", command, got.Text)
		}
	}
	if strings.Count(got.Text, "`cargo test composition`") != 1 {
		t.Fatalf("verification was cloned into probes:\n%s", got.Text)
	}
}
