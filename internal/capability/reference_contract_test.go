package capability

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConformanceReferencesNormalizeExactOutputContracts(t *testing.T) {
	refs := []StructuredReference{
		fixtureReference(t, "inspect_review_findings.json"),
		fixtureReference(t, "observe_gate.json"),
	}
	contracts, err := NormalizeReferenceContracts(refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 2 || contracts[0].Operation != "inspect_review_findings" || contracts[1].Operation != "observe_gate" {
		t.Fatalf("normalized order = %+v", contracts)
	}
	if got := contracts[0].RequiredOutputFields; !reflect.DeepEqual(got, []string{"inspections"}) {
		t.Fatalf("inspect_review_findings fields = %v", got)
	}
	if got := contracts[1].RequiredOutputFields; !reflect.DeepEqual(got, []string{"findings"}) {
		t.Fatalf("observe_gate fields = %v", got)
	}
	for _, contract := range contracts {
		if contract.AdditionalProperties || !strings.HasPrefix(contract.Digest, "sha256:") || len(contract.Digest) != 71 {
			t.Errorf("contract lost exactness or provenance: %+v", contract)
		}
		violations := contract.ValidateOutputFields(append(contract.RequiredOutputFields, "error"))
		if len(violations) != 1 || !strings.Contains(violations[0], `forbidden field "error"`) {
			t.Errorf("extra error field survived %s: %v", contract.Operation, violations)
		}
	}
}

func TestEveryConformanceFixtureDeclaresAValidOutputContract(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "conformance", "v1", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	refs := make([]StructuredReference, 0, len(paths))
	for _, path := range paths {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		refs = append(refs, StructuredReference{Source: path, Content: raw})
	}
	contracts, err := NormalizeReferenceContracts(refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != len(paths) {
		t.Fatalf("normalized %d contracts from %d fixture files", len(contracts), len(paths))
	}
}

func TestReferenceContractRejectsFixturesThatContradictDeclaration(t *testing.T) {
	raw := []byte(`{
  "schema_version":"fledge.capability-conformance/v1",
  "operation":"observe_gate",
  "contract":{"output":{"required":["findings"],"additional_properties":false}},
  "cases":[{"id":"bad","expected":{"findings":[],"error":""}}]
}`)
	_, err := NormalizeReferenceContracts([]StructuredReference{{Source: "bad.json", Content: raw}})
	if err == nil || !strings.Contains(err.Error(), `forbidden field "error"`) {
		t.Fatalf("contradictory fixture accepted: %v", err)
	}
}

func TestReferenceContractsRejectConflictingSources(t *testing.T) {
	makeRef := func(source, field string) StructuredReference {
		return StructuredReference{Source: source, Content: []byte(`{
  "schema_version":"fledge.capability-conformance/v1",
  "operation":"observe_gate",
  "contract":{"output":{"required":["` + field + `"],"additional_properties":false}},
  "cases":[{"id":"one","expected":{"` + field + `":[]}}]
}`)}
	}
	_, err := NormalizeReferenceContracts([]StructuredReference{makeRef("a.json", "findings"), makeRef("b.json", "errors")})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflicting declarations accepted: %v", err)
	}
}

func TestReferenceContractRegionIsDeterministicCompleteAndProvenanced(t *testing.T) {
	contracts := []ReferenceContract{
		{Operation: "observe_gate", RequiredOutputFields: []string{"findings"}, OptionalOutputFields: []string{}, AdditionalProperties: false, Source: "z.json", Digest: "sha256:z"},
		{Operation: "inspect_plan_task", RequiredOutputFields: []string{"inspections"}, OptionalOutputFields: []string{"notes"}, AdditionalProperties: true, Source: "a.json", Digest: "sha256:a"},
		{Operation: "observe_gate", RequiredOutputFields: []string{"findings"}, OptionalOutputFields: []string{}, AdditionalProperties: false, Source: "b.json", Digest: "sha256:b"},
	}
	rendered := RenderReferenceContractRegion(contracts)
	for _, want := range []string{
		"ducklab-reference-contracts:begin", "ducklab-reference-contracts:end",
		`"required": [`, `"optional": [`, `"additional_properties": true`,
		"`inspect_plan_task`: `a.json` (sha256:a)", "`observe_gate`: `b.json` (sha256:b)", "`observe_gate`: `z.json` (sha256:z)",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered region lacks %q:\n%s", want, rendered)
		}
	}
	if strings.Count(rendered, `"observe_gate": {`) != 1 {
		t.Fatalf("identical sources produced duplicate declarations:\n%s", rendered)
	}
	if strings.Index(rendered, "`inspect_plan_task`") > strings.Index(rendered, "`observe_gate`") {
		t.Fatalf("provenance is not operation-sorted:\n%s", rendered)
	}
	if again := RenderReferenceContractRegion(contracts); again != rendered {
		t.Fatal("rendering identical normalized contracts was not deterministic")
	}
}

func TestReferenceOutputDeclarationPreservesSemanticShape(t *testing.T) {
	contract := ReferenceContract{
		Operation: "observe_gate", RequiredOutputFields: []string{"findings"},
		OptionalOutputFields: []string{"notes"}, AdditionalProperties: false,
	}
	valid := ReferenceOutputDeclaration{Required: []string{"findings"}, Optional: []string{"notes"}, AdditionalProperties: false}
	if got := contract.ValidateOutputDeclaration(valid); len(got) != 0 {
		t.Fatalf("exact declaration rejected: %v", got)
	}
	wrong := ReferenceOutputDeclaration{Required: []string{"findings", "notes"}, Optional: []string{}, AdditionalProperties: true}
	got := strings.Join(contract.ValidateOutputDeclaration(wrong), "\n")
	for _, want := range []string{"required output fields do not match", "optional output fields do not match", "additional_properties does not match"} {
		if !strings.Contains(got, want) {
			t.Errorf("shape mismatch lacks %q: %s", want, got)
		}
	}
}

func fixtureReference(t *testing.T, name string) StructuredReference {
	t.Helper()
	path := filepath.Join("testdata", "conformance", "v1", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return StructuredReference{Source: path, Content: raw}
}
