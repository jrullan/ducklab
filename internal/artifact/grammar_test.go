package artifact

import (
	"fmt"
	"strings"
	"testing"
)

func TestVersionedArtifactGrammarMatrix(t *testing.T) {
	kinds := []struct {
		name string
		kind Kind
		body string
	}{
		{"requirements", KindRequirements, "## REQ-001 — Requirement\n"},
		{"spec", KindSpec, "## SPEC-001 — Specification\n"},
		{"plan", KindPlan, "## M-01 — Milestone\n\n### T-001 — Task\n"},
	}

	for _, tc := range kinds {
		t.Run(tc.name+" grammar 2 round trip", func(t *testing.T) {
			content := artifactWithGrammar(2, tc.kind, tc.body)
			doc, err := Parse(content, tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			if doc.Front.Version != 41 {
				t.Fatalf("version = %d, want 41", doc.Front.Version)
			}

			rendered := Render(doc)
			if !strings.Contains(rendered, "grammar: 2\n") || !strings.Contains(rendered, "version: 41\n") {
				t.Fatalf("grammar-2 render must preserve grammar and version:\n%s", rendered)
			}
			again, err := Parse(rendered, tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			if again.Front.Version != 41 {
				t.Fatalf("round-trip version = %d, want 41", again.Front.Version)
			}
		})

		t.Run(tc.name+" missing grammar is legacy diagnostic", func(t *testing.T) {
			errs, err := SyntaxLint(artifactWithGrammar(0, tc.kind, tc.body), tc.kind)
			if err != nil {
				t.Fatalf("missing grammar must remain readable: %v", err)
			}
			if !hasGrammarDiagnostic(errs, "legacy_grammar") {
				t.Fatalf("missing grammar diagnostics = %v, want legacy_grammar", errs)
			}
		})

		t.Run(tc.name+" unknown grammar is unsupported only", func(t *testing.T) {
			errs, err := SyntaxLint(artifactWithGrammar(99, tc.kind, tc.body), tc.kind)
			if err != nil {
				t.Fatalf("unknown grammar must report a diagnostic, not fail parsing: %v", err)
			}
			if !hasGrammarDiagnostic(errs, "unsupported_grammar") {
				t.Fatalf("unknown grammar diagnostics = %v, want unsupported_grammar", errs)
			}
			if hasGrammarDiagnostic(errs, "legacy_grammar") {
				t.Fatalf("unknown grammar diagnostics = %v, must not include legacy_grammar", errs)
			}
		})
	}
}

func artifactWithGrammar(grammar int, kind Kind, body string) string {
	frontmatter := "---\nkind: " + string(kind) + "\nversion: 41\n"
	if grammar != 0 {
		frontmatter += fmt.Sprintf("grammar: %d\n", grammar)
	}
	return frontmatter + "---\n\n" + body
}

func hasGrammarDiagnostic(errs []FieldError, code string) bool {
	for _, err := range errs {
		if strings.Contains(err.Error(), code) {
			return true
		}
	}
	return false
}
