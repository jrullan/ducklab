package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/store"
	"github.com/jrullan/ducklab/internal/strategy"
)

// A promoted bug was the one door into the build loop whose task carried no
// **Acceptance slices:** checklist: T-070 ran with "1/1" — the task as a whole —
// while every plan-born task reports item by item. The triager now proposes
// the contract, promotion renders it, and extraction reads the label's block
// only, so a REPORT that happens to contain bullets does not become the
// contract.
func TestPromotedTaskCarriesTheTriagersDeliverables(t *testing.T) {
	b := &store.Bug{
		ID: "B-060", Title: "brake is a one-way door",
		Body:         "What happened:\n- refusal one\n- refusal two\n\nExpected: fs_read resets the streak.",
		Component:    "tool dispatch",
		TriageReason: "the brake never resets",
		TestStrategy: "test-first",
		Deliverables: "The brake resets after a successful fs_read of the braked path\nA test asserts the reset restores a one-probe window",
	}
	body := promotedTaskBody(b, "")
	if !strings.Contains(body, "**Acceptance slices:**\n- The brake resets after a successful fs_read of the braked path\n- A test asserts the reset restores a one-probe window\n") {
		t.Fatalf("body lacks the checklist:\n%s", body)
	}
	got := strategy.ExtractDeliverables(b.Title, body)
	if len(got) != 2 || got[0] != "The brake resets after a successful fs_read of the braked path" {
		t.Fatalf("extracted contract = %v — the reporter's bullets must not leak in", got)
	}
}

func TestPromotedReportLabelsAreNotMachineFields(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s, BugRequest{
		Title: "expected label", Body: "**Expected:** the selected option remains visible",
	})
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := db.GetTask(out["task"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task.Body, "**Expected**: the selected option remains visible") {
		t.Fatalf("report label was not preserved as readable context:\n%s", task.Body)
	}
	doc, err := artifact.Parse("## M-01 — Fixes\n\n### "+task.ID+" — "+task.Title+"\n\n"+task.Body, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range doc.FieldErrors {
		if diagnostic.Code == "unknown_field" {
			t.Fatalf("promoted report emitted an unknown machine field: %v\n%s", diagnostic, task.Body)
		}
	}
}

func TestReporterContextOnlyNeutralisesLineStartLabelsOutsideFences(t *testing.T) {
	input := strings.Join([]string{
		"**Expected:** line-start label",
		"Quote `**Toolchain:** cmd:python` remains exact.",
		"```markdown",
		"**Expected:** fenced evidence",
		"```",
		"    **Observed:** indented label",
	}, "\n")
	want := strings.Join([]string{
		"**Expected**: line-start label",
		"Quote `**Toolchain:** cmd:python` remains exact.",
		"```markdown",
		"**Expected:** fenced evidence",
		"```",
		"    **Observed**: indented label",
	}, "\n")
	if got := reporterContext(input); got != want {
		t.Fatalf("reporter context =\n%s\nwant\n%s", got, want)
	}
}

func TestPromotedTaskNeutralisesReopenAndTriageLabels(t *testing.T) {
	b := &store.Bug{
		ID:           "B-477",
		Title:        "labels remain prose",
		Body:         "report prose",
		TriageReason: "**Expected:** triage explanation",
	}
	body := promotedTaskBody(b, "**Observed:** reopened evidence")
	for _, want := range []string{"**Observed**: reopened evidence", "**Expected**: triage explanation"} {
		if !strings.Contains(body, want) {
			t.Fatalf("promoted body lacks neutralised context %q:\n%s", want, body)
		}
	}
	doc, err := artifact.Parse("## M-01 — Fixes\n\n### T-001 — Fix\n\n"+body, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range doc.FieldErrors {
		if diagnostic.Code == "unknown_field" {
			t.Fatalf("free-text context emitted an unknown machine field: %v\n%s", diagnostic, body)
		}
	}
}

func TestPromotedTaskUsesOnlyCanonicalMachineFields(t *testing.T) {
	b := &store.Bug{
		ID: "B-447", Title: "promote output is valid",
		Component: "trace", SuspectedFiles: "internal/service/bugs.go",
		TestStrategy: "test-first", TestReason: "the template is deterministic",
		Deliverables: "The task parses without unknown fields",
	}
	content := "## M-01 — Fixes\n\n### T-001 — " + b.Title + "\n\n" + promotedTaskBody(b, "")
	doc, err := artifact.Parse(content, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range doc.FieldErrors {
		if diagnostic.Code == "unknown_field" {
			t.Fatalf("promote emitted an unknown machine field: %v\n%s", diagnostic, content)
		}
	}
}

// Without a triage contract the old shape holds: the report's own bullets are
// still not deliverables when a label exists elsewhere, and a body with no
// label keeps the historical all-bullets reading.
func TestPromotedTaskWithoutDeliverablesKeepsItsShape(t *testing.T) {
	b := &store.Bug{ID: "B-001", Title: "t", Body: "prose only", TriageReason: "r"}
	body := promotedTaskBody(b, "")
	if strings.Contains(body, "**Acceptance slices:**") {
		t.Fatalf("invented a checklist:\n%s", body)
	}
}

func TestManualAcceptanceSlicesAreReservedForTheHumanGate(t *testing.T) {
	items := []string{
		"The CSS provider is removed",
		"GNOME Wayland manual smoke shows the frozen frame",
	}
	manual := manualDeliverables(items)
	if len(manual) != 1 || !manual[2] {
		t.Fatalf("manual deliverables = %v, want slice 2", manual)
	}
	payload := humanVerificationPayload(items)
	if len(payload) != 1 || payload[0]["id"] != 2 || payload[0]["text"] != items[1] {
		t.Fatalf("human verification payload = %#v", payload)
	}
}

// B-552: T-063's slices cite the calculator's manual. The noun is not a way
// of verifying, and both slices were backed by UI regressions the gate ran.
func TestTheProductManualIsNotManualVerification(t *testing.T) {
	notManual := []string{
		"El flujo UI del manual permite guardar, recuperar y redefinir ×2+3, y aplicarlo con resultados y contador correctos.",
		"Las regresiones UI cubren el acceso desde Home vacío y mantienen los controles B-010/B-019, actualizando su preparación incompatible con el manual.",
		"Key legends match the user manual",
		"Los atajos a mano cubren el flujo",
		"Las leyendas escritas a mano se respetan",
		"Diagrams drawn by hand stay in docs/",
	}
	if got := manualDeliverables(notManual); len(got) != 0 {
		t.Fatalf("the product manual was read as manual verification: %v", got)
	}
	manual := []string{
		"Verified manually on the desktop",
		"Manual check: the dialog opens",
		"Verificación manual en la app: el contador avanza",
		"Se comprueba a mano en el escritorio",
		"Pruebas manuales del flujo de guardado",
		"Checked by hand on a physical device",
		"Manual acceptance: the counter advances",
		"Manual: open the app and press 2nd",
		"Validación manual del contador",
		"Chequeo manual en la app",
		"manual e2e on device",
	}
	if got := manualDeliverables(manual); len(got) != len(manual) {
		t.Fatalf("manual verification wording missed: %v of %d", got, len(manual))
	}
}
