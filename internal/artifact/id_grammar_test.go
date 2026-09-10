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

// B-370: the old cardinality-only check accepted a bare path, after which the
// toolchain silently discarded it because only typed file: entries have lane
// semantics. Grammar 2 must reject the information at its boundary instead.
func TestPlanContractRejectsUntypedArtifactReferences(t *testing.T) {
	for _, field := range []string{"Produces", "Consumes", "Exercises"} {
		t.Run(field, func(t *testing.T) {
			body := "---\nkind: plan\ngrammar: 2\nversion: 1\n---\n\n" +
				"## M-01 — Core\n\n### T-001 — Build\n\n" +
				"**Implements:** SPEC-001\n**Work unit:** build the app\n" +
				"**Acceptance slices:**\n- the app builds\n" +
				"**Acceptance probes:**\n1. `go test ./...`\n" +
				"**Produces:** file:src/main.go\n**Consumes:** none\n" +
				"**Verification:** `go test ./...`\n**Exercises:** file:src/main.go\n"
			body = strings.Replace(body, "**"+field+":** "+map[string]string{
				"Produces": "file:src/main.go", "Consumes": "none", "Exercises": "file:src/main.go",
			}[field], "**"+field+":** src/main.go", 1)
			diagnostics, err := ContractLint(body, KindPlan)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(diagnostics, func(d FieldError) bool {
				return d.Code == "invalid_artifact_reference" && d.Key == field && d.Token == "src/main.go"
			}) {
				t.Fatalf("%s bare path passed grammar 2: %+v", field, diagnostics)
			}
		})
	}
}

func TestPlanArtifactReferenceAuthorityMatchesManifestContract(t *testing.T) {
	for _, item := range []string{"file:src/main.go", "dir:fixtures", "build-target:app", "capability:runtime"} {
		if !ValidPlanArtifact(item) {
			t.Errorf("valid artifact rejected: %q", item)
		}
	}
	for _, item := range []string{"src/main.go", "file:", "image:clipboard", "none"} {
		if ValidPlanArtifact(item) {
			t.Errorf("invalid artifact accepted: %q", item)
		}
	}
}
