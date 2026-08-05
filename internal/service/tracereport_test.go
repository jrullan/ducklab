package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// The development report is the spine made readable: the software in the
// approved requirements' own words, then every requirement traced to spec to
// tasks with their real statuses — and the breaks, because a report that
// hides them is marketing.
func TestTheDevelopmentReportTracesTheSpine(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "---\nkind: requirements\nversion: 2\napproved_by: human\n---\n\n" +
			"A single-file triangle calculator.\n\n" +
			"## REQ-001 — Interactive triangle\n\n**Priority:** must\n\nThe app shall draw a draggable triangle.\n\n" +
			"## REQ-002 — Nothing mobile\n\n**Priority:** wont\n\nOut of scope.\n",
		artifact.KindSpec: "## SPEC-001 — Canvas rendering\n\n**Implements:** REQ-001\n\nDraw on canvas.\n",
		artifact.KindPlan: "## M-001 — Core\n\n### T-001 — Render triangle\n\n**Implements:** SPEC-001\n\nDraw it.\n\n" +
			"### T-046 — Fix labels\n\nFixes B-005.\n\nLabels missing.\n",
	})

	rendered, err := s.TraceReport(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		"## Traceability matrix",
		"| Requirement | Priority | Spec section | Tasks (status) |",
		"| REQ-001 Interactive triangle | must | SPEC-001 Canvas rendering | T-001 (todo) |",
		"| REQ-002 Nothing mobile | wont | excluded | — |",
		"development report",
		"requirements v2 (approved by human)",
		"A single-file triangle calculator.",          // the narrative preamble
		"REQ-001 — Interactive triangle",              // the matrix root
		"The app shall draw a draggable triangle.",    // requirement prose kept
		"SPEC-001 — Canvas rendering",                 // the spec hop
		"T-001 — Render triangle · **todo**",          // the task with its status
		"## Bug fixes",                                // promoted tasks, separately
		"T-046 — Fix labels (Fixes B-005)",            //   justified by the report
		"## Spine health",                             // and the honesty section
	} {
		if !strings.Contains(rendered, must) {
			t.Errorf("report lacks %q", must)
		}
	}
	// A wont requirement with no spec is exclusion working, not a warning.
	if strings.Contains(rendered, "REQ-002") && strings.Contains(
		section(rendered, "REQ-002"), "⚠") {
		t.Error("a wont requirement was flagged as a gap")
	}
}

func section(md, id string) string {
	i := strings.Index(md, id)
	if i < 0 {
		return ""
	}
	end := strings.Index(md[i:], "### ")
	if end < 0 {
		return md[i:]
	}
	return md[i : i+end]
}

// A requirement whose spec never came exists in the report as a named gap.
func TestTheReportNamesTheGaps(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Orphan\n\n**Priority:** must\n\nNothing implements this.\n",
	})
	rendered, err := s.TraceReport(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "no spec section implements this requirement") {
		t.Error("an unimplemented must requirement was not named as a gap")
	}
	// And the table names the same gap, so neither form hides it.
	if !strings.Contains(rendered, "| REQ-001 Orphan | must | ⚠ none | — |") {
		t.Errorf("the table row for the orphan requirement is wrong:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Spine health") {
		t.Error("the breaks section is missing")
	}
}
