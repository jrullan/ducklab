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
	findings := structureFindings(nil, cur, "markdown_sections:REQ")
	if len(findings) != 1 || !strings.Contains(findings[0], "REQ-003.1") || !strings.Contains(findings[0], "sub-numbered") {
		t.Fatalf("findings = %v, want one naming the sub-numbered heading", findings)
	}
}
