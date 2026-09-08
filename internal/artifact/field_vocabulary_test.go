package artifact

import (
	"strings"
	"testing"
)

func TestSyntaxLintPlanFieldVocabulary(t *testing.T) {
	var milestone, task strings.Builder
	for _, definition := range FieldVocabulary() {
		if definition.Kind != KindPlan {
			continue
		}
		value := "value"
		if definition.Canonical == "Implements" {
			value = "SPEC-001"
		}
		line := "**" + definition.Canonical + ":** " + value + "\n"
		if definition.Scope == PlanMilestoneScope {
			milestone.WriteString(line)
		} else if definition.Scope == PlanTaskScope {
			task.WriteString(line)
		}
	}
	content := "## M-01 — Complete\n\n" + milestone.String() + "\n### T-001 — Complete\n\n" + task.String()
	errs, err := SyntaxLint(content, KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("canonical plan vocabulary rejected: %v", errs)
	}
}

func TestSyntaxLintCanonicalizesIntentionalAlias(t *testing.T) {
	doc, err := Parse("## M-01 — Core\n\n### T-001 — Task\n\n**Dependencies:** T-002\n", KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Section("T-001").Field("depends on"); got != "T-002" {
		t.Fatalf("alias = %q, want canonical value", got)
	}
	if len(doc.FieldErrors) != 0 {
		t.Fatalf("alias lint errors = %v", doc.FieldErrors)
	}
}

func TestSyntaxLintReportsUnknownBoldFields(t *testing.T) {
	content := "## M-01 — Core\n\n### T-001 — Task\n\n**Implementa:** SPEC-001\n**Nonsense field:** value\n"
	errs, err := SyntaxLint(content, KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 2 {
		t.Fatalf("errors = %v", errs)
	}
	if got := errs[0].Error(); got != "T-001 unknown field **Implementa:**; use **Implements:**" {
		t.Fatalf("localized diagnostic = %q", got)
	}
	if errs[1].Suggestion != "" || !strings.Contains(errs[1].Error(), "unknown field **Nonsense field:**") {
		t.Fatalf("unrelated diagnostic = %+v", errs[1])
	}
}

func TestSyntaxLintAcceptsPlanBoundaryFields(t *testing.T) {
	content := "## M-01 — Core\n\n### T-001 — Task\n\n**Out of scope:** no migration\n**Assumption:** storage is available\n"
	errs, err := SyntaxLint(content, KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("plan boundary fields rejected: %v", errs)
	}
}

func TestUnboldedPlanVocabularyUsesSameAuthority(t *testing.T) {
	doc, err := Parse("## M-01 — Core\n\nWork unit: delivery\n\n### T-001 — Task\n\nAcceptance slices: smoke\n", KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Sections[0].Field("work unit"); got != "delivery" {
		t.Fatalf("milestone field = %q", got)
	}
	if got := doc.Section("T-001").Field("acceptance slices"); got != "smoke" {
		t.Fatalf("task field = %q", got)
	}
}
