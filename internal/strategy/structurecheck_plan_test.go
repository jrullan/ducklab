package strategy

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
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
	for _, want := range []string{"T-002 has no **Implements:** line", "T-001 has 4 top-level **Deliverables:** bullets", "T-001 has no **Verification:** line", "lane collision"} {
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
