package artifact

import (
	"os"
	"strings"
	"testing"
)

// B-521: the TI-36X journal as the old engine wrote it — the person's INT-002
// marked superseded with no requirements, and reviewer-findings blobs
// recorded as INT-003 and INT-005. It must still parse, resolve its runs, and
// trace.
const oldShapeIntent = `---
kind: intent
version: 10
approved_by: human
---

## INT-001 — I want to create a picture perfect one page web application

**Run:** r-4zgi
**Submitted at:** 2026-10-02T03:02:11Z
**Outcome:** accepted
**Requirements:** REQ-001, REQ-002

### Original brief

I want to create a picture perfect one page web application.

## INT-002 — Use this guide to fill in the gap

**Run:** r-tpfn
**Submitted at:** 2026-10-10T11:55:19Z
**Outcome:** superseded
**Requirements:** -

### Original brief

Use this guide to fill in the gap between the implemented functionality and the intended functionality of the calculator

## INT-003 — - [major] REQ-008 invents behavior (polar conversion of a real scalar…

**Run:** r-eett
**Submitted at:** 2026-10-10T12:15:43Z
**Outcome:** superseded
**Requirements:** -

### Original brief

- [major] REQ-008 invents behavior (polar conversion of a real scalar raising an LCD error). (REQ-008) Fix: Remove the real-scalar error example.
- [minor] REQ-009 introduces 'confirmed clear-variable operation'. (REQ-009) Fix: Restate the clear-variable requirement.

## INT-005 — - [major] REQ-008 invents behavior (polar conversion of a real scalar…

**Run:** r-6kyd
**Submitted at:** 2026-10-11T17:20:17Z
**Outcome:** not accepted
**Requirements:** -

### Original brief

- [major] REQ-008 invents behavior. (REQ-008) Fix: Remove the real-scalar error example.
`

// fieldDefects drops the journal-wide grammar notice every pre-grammar
// intent.md carries; the fields themselves must be clean.
func fieldDefects(diagnostics []FieldError) []FieldError {
	var out []FieldError
	for _, d := range diagnostics {
		if d.Code != "legacy_grammar" {
			out = append(out, d)
		}
	}
	return out
}

func writeOldShapeIntent(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(DocsDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(root, KindIntent), []byte(oldShapeIntent), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestOldShapeIntentJournalStillParsesAndTraces(t *testing.T) {
	root := writeOldShapeIntent(t)
	doc, err := Load(root, KindIntent)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) != 4 || len(fieldDefects(doc.FieldErrors)) != 0 {
		t.Fatalf("old journal: %d sections, field errors %v", len(doc.Sections), doc.FieldErrors)
	}
	if diagnostics, err := SyntaxLint(oldShapeIntent, KindIntent); err != nil || len(fieldDefects(diagnostics)) != 0 {
		t.Fatalf("old journal lints dirty: %v %v", diagnostics, err)
	}
	for run, want := range map[string]string{"r-tpfn": "INT-002", "r-eett": "INT-003", "r-6kyd": "INT-005", "r-none": ""} {
		if got, err := IntentIDForRun(root, run); err != nil || got != want {
			t.Fatalf("IntentIDForRun(%s) = %q, %v; want %q", run, got, err, want)
		}
	}
	// A requirements document citing the old revision entries still traces.
	reqs, _ := Parse("## REQ-008 — Complex numbers\n\n**Originates from:** INT-001, INT-005\n\nIt converts.\n", KindRequirements)
	spine := &Spine{Intent: doc, Requirements: reqs, Spec: &Document{}, Plan: &Document{}}
	node, err := spine.Walk("REQ-008")
	if err != nil || strings.Join(node.Up, ",") != "INT-001,INT-005" {
		t.Fatalf("old provenance walk = %+v, %v", node, err)
	}
	for _, e := range spine.Check() {
		if e.Kind == UnrealizedIntent {
			t.Fatalf("old journal reports %v", e)
		}
	}
}

// A revision appended to an old-shape intent joins its header block, reopens
// it, and leaves the rest of the journal untouched; the new shape lints clean.
func TestRevisionOfAnOldShapeIntentReopensItAndLintsClean(t *testing.T) {
	root := writeOldShapeIntent(t)
	n, err := AppendIntentRevision(root, "INT-002", "r-new", "2026-10-12T00:00:00Z", "Keep the guidebook wording.", "- [critical] The Ans sentence was deleted.")
	if err != nil || n != 1 {
		t.Fatalf("append = %d, %v", n, err)
	}
	raw, _ := os.ReadFile(Path(root, KindIntent))
	doc, _ := Load(root, KindIntent)
	sec := doc.Section("INT-002")
	if len(doc.Sections) != 4 || sec.Field("outcome") != "pending" || sec.Field("revisions") != "r-new" {
		t.Fatalf("after revision: %d sections, outcome %q, revisions %q", len(doc.Sections), sec.Field("outcome"), sec.Field("revisions"))
	}
	if !strings.Contains(sec.Body, "**Requirements:** -\n**Revisions:** r-new\n") {
		t.Fatalf("Revisions did not join the header block:\n%s", sec.Body)
	}
	for _, want := range []string{"### Original brief\n\nUse this guide", "### Revision 1 — r-new", "#### The person's note\n\n<!-- ducklab:text note -->\nKeep the guidebook wording.", "#### Reviewer findings carried over\n\n<!-- ducklab:text findings -->\n- [critical] The Ans sentence was deleted."} {
		if !strings.Contains(sec.Body, want) {
			t.Fatalf("revision body lacks %q:\n%s", want, sec.Body)
		}
	}
	if diagnostics, err := SyntaxLint(string(raw), KindIntent); err != nil || len(fieldDefects(diagnostics)) != 0 {
		t.Fatalf("new journal lints dirty: %v %v", diagnostics, err)
	}
	if got, _ := IntentIDForRun(root, "r-new"); got != "INT-002" {
		t.Fatalf("revision run resolves to %q", got)
	}
	// Idempotent per run.
	if again, err := AppendIntentRevision(root, "INT-002", "r-new", "x", "y", ""); err != nil || again != 1 {
		t.Fatalf("re-append = %d, %v", again, err)
	}
	if doc2, _ := Load(root, KindIntent); strings.Count(doc2.Section("INT-002").Body, "### Revision") != 1 {
		t.Fatal("a re-append duplicated the revision")
	}
	// An accepted intention takes no revisions: a change to it is new intent.
	if _, err := AppendIntentRevision(root, "INT-001", "r-late", "x", "y", ""); err == nil {
		t.Fatal("an accepted intent was reopened")
	}
}

// Three chained revisions: one intent; only the head's non-accept closes it;
// acceptance from the head closes it with the requirements.
func TestThreeRevisionsLeaveOneIntentAndOnlyTheHeadDecides(t *testing.T) {
	root := t.TempDir()
	id, err := AppendIntent(root, "r-0", "2026-10-10T00:00:00Z", "Fill the gap")
	if err != nil {
		t.Fatal(err)
	}
	for i, run := range []string{"r-1", "r-2", "r-3"} {
		n, err := AppendIntentRevision(root, id, run, "2026-10-10T00:00:00Z", "", "- [major] finding "+run)
		if err != nil || n != i+1 {
			t.Fatalf("revision %s = %d, %v", run, n, err)
		}
	}
	doc, _ := Load(root, KindIntent)
	if len(doc.Sections) != 1 || doc.Sections[0].Field("revisions") != "r-1, r-2, r-3" {
		t.Fatalf("journal: %d sections, revisions %q", len(doc.Sections), doc.Sections[0].Field("revisions"))
	}
	for _, run := range []string{"r-0", "r-1", "r-2"} {
		if err := ResolveIntent(root, run, "superseded", nil); err != nil {
			t.Fatal(err)
		}
	}
	if doc, _ := Load(root, KindIntent); doc.Sections[0].Field("outcome") != "pending" {
		t.Fatalf("an older run closed the intent: %q", doc.Sections[0].Field("outcome"))
	}
	if err := ResolveIntent(root, "r-3", "accepted", []string{"REQ-001"}); err != nil {
		t.Fatal(err)
	}
	// A late sibling supersede cannot downgrade an accepted intent.
	if err := ResolveIntent(root, "r-3", "superseded", nil); err != nil {
		t.Fatal(err)
	}
	doc, _ = Load(root, KindIntent)
	if doc.Sections[0].Field("outcome") != "accepted" || doc.Sections[0].Field("requirements") != "REQ-001" {
		t.Fatalf("after accept: %q %q", doc.Sections[0].Field("outcome"), doc.Sections[0].Field("requirements"))
	}
}
