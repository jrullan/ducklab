package stage

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
)

func TestLinkCandidateIntentMakesProvenancePartOfReviewedBody(t *testing.T) {
	root := t.TempDir()
	if _, err := artifact.AppendIntent(root, "r-intake", "2026-01-02T00:00:00Z", "Add capture"); err != nil {
		t.Fatal(err)
	}
	current := &artifact.Document{Front: artifact.Frontmatter{Kind: artifact.KindRequirements}}
	candidate := &agent.Outcome{Text: "## REQ-001 — Capture\n\nCapture the screen.\n"}

	linked, _, err := linkCandidateIntent(root, "r-intake", current, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(linked.Text, "**Originates from:** INT-001") {
		t.Fatalf("reviewed body lacks intention provenance:\n%s", linked.Text)
	}
	doc, err := artifact.Parse(linked.Text, artifact.KindRequirements)
	if err != nil || artifact.RenderBody(doc) != linked.Text {
		t.Fatalf("linked body is not stable: err=%v\n%s", err, linked.Text)
	}
	artifact.LinkRequirementsDocument(current, doc, "INT-001")
	if got := artifact.RenderBody(doc); got != linked.Text {
		t.Fatalf("post-review compatibility pass changed the body:\n%s", got)
	}
}

func TestLinkCandidateIntentDedupesAfterProvenanceMutation(t *testing.T) {
	root := t.TempDir()
	if _, err := artifact.AppendIntent(root, "r-intake", "2026-01-02T00:00:00Z", "Add capture"); err != nil {
		t.Fatal(err)
	}
	current := &artifact.Document{Front: artifact.Frontmatter{Kind: artifact.KindRequirements}}
	candidate := &agent.Outcome{Text: "## REQ-016 — OCR\n\n**Originates from:** INT-001\n\nNo OCR.\n\n## REQ-018 — OCR\n\nNo OCR.\n"}

	linked, dropped, err := linkCandidateIntent(root, "r-intake", current, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || strings.Contains(linked.Text, "REQ-018") {
		t.Fatalf("post-provenance duplicate survived: dropped=%v\n%s", dropped, linked.Text)
	}
}

func TestMaterializeCandidateFoldsOrderBeforeAssigningIDs(t *testing.T) {
	current := &artifact.Document{Front: artifact.Frontmatter{Kind: artifact.KindSpec}}
	first := "## SPEC-001 — Core\n\n**Implements:** REQ-001\n\ncore\n\n" +
		"## SPEC-002 — UI\n\n**Implements:** REQ-002\n\nui\n"
	closing := "## SPEC-002 — UI\n\n**Implements:** REQ-002\n\nrevised ui\n\n" +
		"## SPEC-001 — Core\n\n**Implements:** REQ-001\n\nrevised core\n"

	out, _, _, _, err := materializeCandidate(current, []string{first, closing}, &agent.Outcome{Text: closing}, artifact.KindSpec)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := "## SPEC-001 — Core"
	if !strings.HasPrefix(out.Text, wantOrder) || strings.Index(out.Text, "## SPEC-001") > strings.Index(out.Text, "## SPEC-002") {
		t.Fatalf("candidate did not preserve canonical section identity/order:\n%s", out.Text)
	}
	parsed, err := artifact.Parse(out.Text, artifact.KindSpec)
	if err != nil || artifact.RenderBody(parsed) != out.Text {
		t.Fatalf("materialized body is not stable: err=%v\n%s", err, out.Text)
	}
}
