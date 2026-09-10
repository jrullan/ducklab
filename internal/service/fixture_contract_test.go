package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

func fixtureContractProject(t *testing.T, testSource string) string {
	t.Helper()
	root := t.TempDir()
	plan := `## M-01 — Conformance

### T-001 — Exercise the corpus

**Produces:** file:tests/corpus.rs

**Consumes:** file:conformance/v1/corpus.json, file:src/default_registry.rs
`
	for path, body := range map[string]string{
		artifact.Path(root, artifact.KindPlan):                  plan,
		filepath.Join(root, "conformance", "v1", "corpus.json"): `{}`,
		filepath.Join(root, "tests", "corpus.rs"):               testSource,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFixtureInvariantRejectsFilteringTheNamedDefaultRegistry(t *testing.T) {
	root := fixtureContractProject(t, `
let source = include_str!("../conformance/v1/corpus.json");
for constructor in BUILTIN_CONSTRUCTORS {
    let mut payload = constructor().into_payload();
    if payload.id != capability { payload.inspections.clear(); }
}
run_document("corpus.json", source, resolver);
`)

	findings := taskFixtureNarrowingFindings(root, "T-001", []string{"tests/corpus.rs"})
	if len(findings) != 1 || !strings.Contains(findings[0].Issue, "test narrows the named fixture") ||
		!strings.Contains(findings[0].Issue, "clearing provider payload") {
		t.Fatalf("findings = %#v, want one named-fixture narrowing", findings)
	}
}

func TestFixtureInvariantRejectsEditingACorpusCaseSelection(t *testing.T) {
	root := fixtureContractProject(t, `
let source = include_str!("../conformance/v1/corpus.json");
for case in document["cases"].as_array_mut().unwrap() {
    case["selection"]["disabled"] = json!(["the-provider-under-test"]);
}
run_document("corpus.json", source, resolver);
`)

	findings := taskFixtureNarrowingFindings(root, "T-001", []string{"tests/corpus.rs"})
	if len(findings) != 1 || !strings.Contains(findings[0].Issue, "editing the corpus case selection") {
		t.Fatalf("findings = %#v, want corpus-selection narrowing", findings)
	}
}

func TestFixtureInvariantAllowsSelectingNamedCasesWithoutChangingTheirInputs(t *testing.T) {
	root := fixtureContractProject(t, `
let source = include_str!("../conformance/v1/corpus.json");
document["cases"].as_array_mut().unwrap().retain(|case| case["id"] == wanted_id);
run_document("corpus.json", source, default_registry);
`)

	if findings := taskFixtureNarrowingFindings(root, "T-001", []string{"tests/corpus.rs"}); len(findings) != 0 {
		t.Fatalf("focused case selection was mistaken for input tampering: %#v", findings)
	}
}

func TestFixtureInvariantIgnoresTestsThatDoNotConsumeAConformanceDocument(t *testing.T) {
	root := fixtureContractProject(t, `payload.inspections.clear();`)
	plan := strings.ReplaceAll(string(mustReadFixtureContract(t, artifact.Path(root, artifact.KindPlan))), "file:conformance/v1/corpus.json", "file:fixtures/local.json")
	if err := os.WriteFile(artifact.Path(root, artifact.KindPlan), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}

	if findings := taskFixtureNarrowingFindings(root, "T-001", []string{"tests/corpus.rs"}); len(findings) != 0 {
		t.Fatalf("local fixture produced conformance invariant: %#v", findings)
	}
}

func mustReadFixtureContract(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
