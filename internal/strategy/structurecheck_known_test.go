package strategy

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
)

// An Implements: target that is not a section of any project document is a
// dangling reference; eleven reached a plan's gate (benchmark run 3).
func TestStructureCheckFlagsImplementsOfUnknownIDs(t *testing.T) {
	known := map[string]bool{"SPEC-001": true, "REQ-001": true}
	cur := []agent.Section{{ID: "M-01", Title: "Core", Body: "### T-001 — Shell\n\n**Implements:** SPEC-001, SPEC-007\n\n**Deliverables:**\n- a\n"}}
	findings := structureFindings(nil, cur, "markdown_sections:M", known, false, "")
	if len(findings) != 1 || !strings.Contains(findings[0], "SPEC-007") || !strings.Contains(findings[0], "not a section") {
		t.Fatalf("findings = %v, want one naming SPEC-007", findings)
	}
	// Without a known set the check stays quiet: nothing to compare against.
	if findings := structureFindings(nil, cur, "markdown_sections:M", nil, false, ""); len(findings) != 0 {
		t.Fatalf("no known set, yet findings = %v", findings)
	}
}
