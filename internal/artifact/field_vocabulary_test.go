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

func TestSyntaxLintTreatsBoldBulletLabelsAsProse(t *testing.T) {
	content := "## SPEC-001 — Capture\n\n- **UI Layer**: GTK4 overlay\n- **Wayland**: portal capture\n\n**Implements:** REQ-001\n"
	errs, err := SyntaxLint(content, KindSpec)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("prose labels were parsed as machine fields: %v", errs)
	}
	doc, err := Parse(content, KindSpec)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Section("SPEC-001").Field("ui layer") != "" {
		t.Fatal("a prose bullet label entered the machine-readable field map")
	}
}

func TestDeleteIsAControlFieldForEveryArtifactSectionShape(t *testing.T) {
	cases := []struct {
		name, content string
		kind          Kind
		id            string
	}{
		{"intent", "## INT-001 — Remove\n\n**Delete:** yes\n", KindIntent, "INT-001"},
		{"requirements", "## REQ-001 — Remove\n\n**Delete:** yes\n", KindRequirements, "REQ-001"},
		{"spec", "## SPEC-001 — Remove\n\n**Delete:** yes\n", KindSpec, "SPEC-001"},
		{"plan milestone", "## M-01 — Remove\n\n**Delete:** yes\n", KindPlan, "M-01"},
		{"plan task", "## M-01 — Core\n\n### T-001 — Remove\n\n**Delete:** yes\n", KindPlan, "T-001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse(tc.content, tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			section := doc.Section(tc.id)
			if section == nil || section.Field("delete") != "yes" {
				t.Fatalf("delete field was not parsed: section=%+v", section)
			}
			if len(section.FieldErrors) != 0 {
				t.Fatalf("delete control produced diagnostics: %v", section.FieldErrors)
			}
		})
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

// B-352: intake and adoption explicitly tell the architect to record inferred
// requirements as Assumption fields. Once grammar 2 made the vocabulary
// strict, the field was legal only on plan tasks, so every REQ in Ducklab's
// own adopted document became a deterministic proposal blocker.
func TestSyntaxLintAcceptsAnIntakeAssumptionOnARequirement(t *testing.T) {
	content := "## REQ-001 — Existing behavior\n\n**Priority:** must\n\n**Assumption:** inferred from the checked-in command handler.\n"
	errs, err := SyntaxLint(content, KindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("intake assumption rejected: %v", errs)
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
