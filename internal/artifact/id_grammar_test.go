package artifact

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPlanDocumentationMatchesGrammarAuthority(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "spec", "02-DATA-MODEL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, want := range []string{
		"only literal `SPEC-NNN` ids separated\n  by commas",
		"Ranges (`SPEC-004–SPEC-007`)",
		"semicolon groups",
		"terminal punctuation (`SPEC-004.`)",
		"another kind such as `REQ-004`",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("public plan grammar lacks %q", want)
		}
	}
	for _, want := range []string{"**Implements:** " + string(ShapeSpecIDs), "**Verification:** " + string(ShapeCommand)} {
		if !strings.Contains(PlanTaskGrammar(), want) {
			t.Errorf("rendered grammar lacks %q:\n%s", want, PlanTaskGrammar())
		}
	}
}

func TestFormatTaskImplementsRoundTripsThroughGrammar(t *testing.T) {
	want := []string{"SPEC-001", "SPEC-004", "SPEC-009"}
	doc, err := Parse("## M-01 — Core\n\n### T-001 — Task\n\n**Implements:** "+FormatTaskImplements(want)+"\n", KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Section("T-001").Implements; !slices.Equal(got, want) {
		t.Fatalf("round trip = %v, want %v", got, want)
	}
}
