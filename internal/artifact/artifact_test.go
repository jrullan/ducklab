package artifact

import (
	"strings"
	"testing"
)

const requirements = `---
kind: requirements
project: miempresa
version: 3
updated_at: 2026-07-27T10:00:00Z
run_id: r-20260727-100000-ab12
ducklings: [pato-atom, pato-local]
approved_by: human
---

Some preamble the architect wrote.

## REQ-001 — Users can log in with email and password

**Priority:** must
**Status:** approved

Body prose here.

**Acceptance:**
- Given a registered user, correct credentials return a session.

## REQ-002 - Sessions expire

**Priority:** should

Second requirement.
`

func TestParseFrontmatter(t *testing.T) {
	doc, err := Parse(requirements, KindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	f := doc.Front
	if f.Kind != KindRequirements || f.Project != "miempresa" || f.Version != 3 {
		t.Errorf("frontmatter = %+v", f)
	}
	if f.RunID != "r-20260727-100000-ab12" {
		t.Errorf("run_id = %q", f.RunID)
	}
	if len(f.Ducklings) != 2 {
		t.Errorf("ducklings = %v", f.Ducklings)
	}
	if !f.Approved() {
		t.Error("approved_by was set but Approved() is false")
	}
}

func TestParseSections(t *testing.T) {
	doc, _ := Parse(requirements, KindRequirements)
	if len(doc.Sections) != 2 {
		t.Fatalf("got %d sections, want 2", len(doc.Sections))
	}
	if doc.Sections[0].ID != "REQ-001" {
		t.Errorf("id = %q", doc.Sections[0].ID)
	}
	if doc.Sections[0].Title != "Users can log in with email and password" {
		t.Errorf("title = %q", doc.Sections[0].Title)
	}
	// Em dash and hyphen must both work: models produce both.
	if doc.Sections[1].Title != "Sessions expire" {
		t.Errorf("hyphen-separated title = %q", doc.Sections[1].Title)
	}
	if doc.Sections[0].Field("priority") != "must" {
		t.Errorf("priority = %q", doc.Sections[0].Field("priority"))
	}
	if !strings.Contains(doc.Sections[0].Body, "Body prose here.") {
		t.Errorf("body lost: %q", doc.Sections[0].Body)
	}
	if doc.Preamble == "" {
		t.Error("preamble lost")
	}
}

// The Implements line is the traceability edge; losing it silently would make
// the spine wrong rather than absent.
func TestImplementsEdges(t *testing.T) {
	doc, _ := Parse(`## SPEC-004 — Session tokens

**Implements:** REQ-001, REQ-007
**Status:** approved

Design prose.
`, KindSpec)
	s := doc.Section("SPEC-004")
	if s == nil {
		t.Fatal("SPEC-004 not found")
	}
	if len(s.Implements) != 2 || s.Implements[0] != "REQ-001" || s.Implements[1] != "REQ-007" {
		t.Errorf("implements = %v", s.Implements)
	}
}

// Prose in the Implements value must not become a phantom edge.
func TestImplementsIgnoresNonIDs(t *testing.T) {
	doc, _ := Parse("## SPEC-001 — X\n\n**Implements:** REQ-001, nothing in particular, TBD\n", KindSpec)
	got := doc.Section("SPEC-001").Implements
	if len(got) != 1 || got[0] != "REQ-001" {
		t.Errorf("implements = %v; prose became an edge", got)
	}
}

// A plan nests tasks under milestones.
func TestParsePlanNestsTasksUnderMilestones(t *testing.T) {
	doc, _ := Parse(`## M-01 — Authentication

### T-003 — Implement session token issuance

**Implements:** SPEC-004
**Complexity:** medium
**Depends on:** T-001

What done looks like.

### T-004 — Expire sessions

**Implements:** SPEC-004

## M-02 — Reporting

### T-010 — Nightly rollup
`, KindPlan)

	if len(doc.Sections) != 2 {
		t.Fatalf("got %d milestones, want 2", len(doc.Sections))
	}
	m1 := doc.Sections[0]
	if m1.ID != "M-01" || len(m1.Children) != 2 {
		t.Fatalf("M-01 has %d tasks", len(m1.Children))
	}
	t3 := m1.Children[0]
	if t3.ID != "T-003" || t3.Title != "Implement session token issuance" {
		t.Errorf("task = %+v", t3)
	}
	if t3.Field("complexity") != "medium" {
		t.Errorf("complexity = %q", t3.Field("complexity"))
	}
	if t3.Field("depends on") != "T-001" {
		t.Errorf("depends on = %q", t3.Field("depends on"))
	}
	if doc.Sections[1].Children[0].ID != "T-010" {
		t.Error("second milestone's task not nested")
	}
	if got := doc.IDs(); len(got) != 5 {
		t.Errorf("IDs = %v", got)
	}
}

// An artifact is a human document first: an extra heading must not break it.
func TestUnrecognisedHeadingsArePreservedNotFatal(t *testing.T) {
	doc, err := Parse(`## Introduction

Not a section id.

## REQ-001 — Real requirement

Body.
`, KindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) != 1 || doc.Sections[0].ID != "REQ-001" {
		t.Fatalf("sections = %v", doc.IDs())
	}
	if !strings.Contains(doc.Preamble, "Introduction") {
		t.Errorf("unrecognised heading was dropped: %q", doc.Preamble)
	}
}

// "REQ-uirements" is prose, not a section id.
func TestHeadingNeedsNumericID(t *testing.T) {
	doc, _ := Parse("## REQ-uirements overview\n\nprose\n", KindRequirements)
	if len(doc.Sections) != 0 {
		t.Errorf("a non-numeric id was indexed: %v", doc.IDs())
	}
}

func TestDocumentWithoutFrontmatterStillParses(t *testing.T) {
	doc, err := Parse("## REQ-001 — X\n\nbody\n", KindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) != 1 {
		t.Errorf("sections = %v", doc.IDs())
	}
	if doc.Front.Version != 0 {
		t.Errorf("invented a version: %d", doc.Front.Version)
	}
}

// An unterminated header is malformed, not absent: treating it as body would
// silently swallow the metadata.
func TestUnterminatedFrontmatterIsNotSwallowed(t *testing.T) {
	doc, _ := Parse("---\nkind: requirements\nversion: 2\n\n## REQ-001 — X\n", KindRequirements)
	if doc.Front.Version == 2 {
		t.Error("an unterminated header was parsed as valid frontmatter")
	}
}

func TestRoundTrip(t *testing.T) {
	doc, _ := Parse(requirements, KindRequirements)
	again, err := Parse(Render(doc), KindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Sections) != len(doc.Sections) {
		t.Fatalf("round trip lost sections: %v vs %v", again.IDs(), doc.IDs())
	}
	if again.Front.Version != doc.Front.Version || again.Front.RunID != doc.Front.RunID {
		t.Errorf("round trip lost frontmatter: %+v", again.Front)
	}
	if again.Sections[0].Field("priority") != "must" {
		t.Error("round trip lost a field")
	}
}

func TestKindHelpers(t *testing.T) {
	if KindRequirements.Prefix() != "REQ" || KindSpec.Prefix() != "SPEC" || KindPlan.Prefix() != "M" {
		t.Error("wrong prefixes")
	}
	if KindSpec.Filename() != "spec.md" {
		t.Errorf("filename = %q", KindSpec.Filename())
	}
	if !ValidKind("plan") || ValidKind("nonsense") {
		t.Error("ValidKind is wrong")
	}
}

func TestSectionLookupMissingReturnsNil(t *testing.T) {
	doc, _ := Parse(requirements, KindRequirements)
	if doc.Section("REQ-999") != nil {
		t.Error("found a section that does not exist")
	}
}
