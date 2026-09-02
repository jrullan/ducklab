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

func TestTopLevelTaskContractEnforcesAcceptanceSlicesV2(t *testing.T) {
	body := "**Implements:** SPEC-001\n\nWork unit: unbolded and therefore not the contract\n\n**Produces:** src/main.c\n\n**Consumes:** none\n\n**Verification:** `cc -fsyntax-only src/main.c`\n\n**Exercises:** src/main.c"
	got := strings.Join(structureFindings(nil, []agent.Section{{ID: "T-009", Title: "Save", Body: body}}, "markdown_sections:T", nil, true, ""), "\n")
	for _, want := range []string{"T-009 has no **Work unit:**", "T-009 has no top-level **Acceptance slices:**"} {
		if !strings.Contains(got, want) {
			t.Errorf("top-level task findings lack %q:\n%s", want, got)
		}
	}
}

func TestIsolatedArchitectOutcomeDropsSiblingTasks(t *testing.T) {
	raw := "## T-008 — CLI\n\n**Implements:** SPEC-001\n\nright\n\n## T-009 — Lifecycle\n\n**Implements:** SPEC-001\n\nwrong sibling"
	parsed, err := agent.ParseContract("markdown_sections:T", raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scopeArchitectSection(&agent.Outcome{Text: raw, Parsed: parsed}, "markdown_sections:T", "T-008")
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
	got, err := scopeArchitectSection(&agent.Outcome{Text: raw, Parsed: parsed}, "markdown_sections:T", "T-008")
	if err != nil {
		t.Fatal(err)
	}
	if len(sectionsOf(got)) != 1 || sectionsOf(got)[0].ID != "T-008" || strings.Contains(got.Text, "T-010") {
		t.Fatalf("normalized outcome = %#v", got)
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
