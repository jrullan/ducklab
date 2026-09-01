package stage

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
)

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
