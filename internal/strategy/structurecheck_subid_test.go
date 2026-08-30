package strategy

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
)

// A sub-numbered heading inside a section is not a section: the spine does
// not see it, and the next seat searches the tree for an id that was never
// there (21 fs_search calls, Neocapture spec 2026-08-30).
func TestStructureCheckFlagsSubNumberedHeadings(t *testing.T) {
	cur := []agent.Section{
		{ID: "REQ-003", Title: "Functional", Body: "Intro.\n\n### REQ-003.1 — Full-screen capture\n\nBody.\n\n### REQ-003.2 — Region\n\nBody."},
		{ID: "REQ-004", Title: "Platform", Body: "Ubuntu.\n\n**Priority:** must"},
	}
	findings := structureFindings(nil, cur, "markdown_sections:REQ", nil, false, "")
	if len(findings) != 1 || !strings.Contains(findings[0], "REQ-003.1") || !strings.Contains(findings[0], "sub-numbered") {
		t.Fatalf("findings = %v, want one naming the sub-numbered heading", findings)
	}
}

// A plan's parsed sections are milestones holding their tasks: one
// Deliverables heading per task is correct, however many tasks a milestone
// has. Counted per milestone, every plan was sent back (benchmark run 2).
func TestOneDeliverablesPerTaskIsNotADuplicate(t *testing.T) {
	milestone := agent.Section{ID: "M-01", Title: "Core", Body: "### T-001 — Scaffold\n\n**Implements:** SPEC-001\n\n**Deliverables:**\n- a\n\n### T-002 — Shell\n\n**Implements:** SPEC-001\n\n**Deliverables:**\n- b\n"}
	if findings := structureFindings(nil, []agent.Section{milestone}, "markdown_sections:M", nil, false, ""); len(findings) != 0 {
		t.Fatalf("a well-formed milestone was flagged: %v", findings)
	}
	dup := agent.Section{ID: "M-01", Title: "Core", Body: "### T-001 — Scaffold\n\n**Implements:** SPEC-001\n\n**Deliverables:**\n- a\n\n**Deliverables:**\n- b\n"}
	findings := structureFindings(nil, []agent.Section{dup}, "markdown_sections:M", nil, false, "")
	if len(findings) != 1 || !strings.Contains(findings[0], "T-001 has 2") {
		t.Fatalf("a task with two Deliverables headings was not flagged: %v", findings)
	}
}
