package strategy

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
)

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

func TestSmallPlanV2CapsAtomicAcceptanceSlices(t *testing.T) {
	body := "### T-001 — Save capture\n\n**Implements:** SPEC-001\n\n**Work unit:** Persist one completed capture\n\n**Acceptance slices:**\n- Opens a save destination\n- Writes a valid PNG\n- Reports success\n- Reports failure\n\n**Produces:** src/save.c\n\n**Consumes:** none\n\n**Verification:** `cc -fsyntax-only src/save.c`\n\n**Exercises:** src/save.c"
	cur := []agent.Section{{ID: "M-01", Title: "Save", Body: body}}
	joined := strings.Join(structureFindings(nil, cur, "markdown_sections:M", map[string]bool{"SPEC-001": true}, true, "## M-01 — Save\n\n"+body), "\n")
	if !strings.Contains(joined, "4 top-level **Acceptance slices:**") {
		t.Fatalf("findings = %s", joined)
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
	for _, want := range []string{"invalid_work_unit", "field `Work unit`", "invalid_acceptance_slices", "field `Acceptance slices`", "1-3 top-level Markdown list items"} {
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
