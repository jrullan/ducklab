package service

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/store"
	"github.com/jrullan/ducklab/internal/strategy"
)

// A promoted bug was the one door into the build loop whose task carried no
// **Deliverables:** checklist: T-070 ran with "1/1" — the task as a whole —
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
	body := promotedTaskBody(b)
	if !strings.Contains(body, "**Deliverables:**\n- The brake resets after a successful fs_read of the braked path\n- A test asserts the reset restores a one-probe window\n") {
		t.Fatalf("body lacks the checklist:\n%s", body)
	}
	got := strategy.ExtractDeliverables(b.Title, body)
	if len(got) != 2 || got[0] != "The brake resets after a successful fs_read of the braked path" {
		t.Fatalf("extracted contract = %v — the reporter's bullets must not leak in", got)
	}
}

// Without a triage contract the old shape holds: the report's own bullets are
// still not deliverables when a label exists elsewhere, and a body with no
// label keeps the historical all-bullets reading.
func TestPromotedTaskWithoutDeliverablesKeepsItsShape(t *testing.T) {
	b := &store.Bug{ID: "B-001", Title: "t", Body: "prose only", TriageReason: "r"}
	body := promotedTaskBody(b)
	if strings.Contains(body, "**Deliverables:**") {
		t.Fatalf("invented a checklist:\n%s", body)
	}
}
