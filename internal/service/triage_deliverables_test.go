package service

import (
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
