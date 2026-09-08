package strategy

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// A promoted portion retains the full parent report for evidence, but its
// numbered work contract and review scope stop at the authoritative current
// portion. Parent context can name a sibling's files and outcome without making
// either a deliverable or a review finding for this task.
// A persisted promotion from before the marker format must receive the same
// implementer and reviewer warning. Its complete promotion structure, rather
// than a bare Fixes reference, identifies the inherited report.
func TestLegacyPromotedTaskPromptsRejectSiblingEvidence(t *testing.T) {
	rec := &recorder{}
	siblingOutcome := "reserve plan-v2 lint grammar for sibling tasks"
	siblingFile := "internal/service/stages.go"
	prompt := "T-262 — Harden legacy task prompt construction\n\n" +
		"**Acceptance:**\n- legacy promoted tasks use their structured contract\n\n" +
		"**Owns:** internal/strategy/execute.go\n\n" +
		"Fixes B-348.\n\n## Reported\n\n" +
		"## Deliverables\n- " + siblingOutcome + "\n\nSibling lane: " + siblingFile + ".\n"
	params := pairParams(rec, "green",
		&agent.Outcome{Text: `{"deliverables":[{"id":1,"status":"done"}]}`},
		verdictOutcome("approve"),
	)
	params.Prompt = prompt
	params.Deliverables = []string{"legacy promoted tasks use their structured contract"}
	params.Rounds = 1

	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	for i, role := range rec.roles {
		if role != config.RoleImplementer && role != config.RoleReviewer {
			continue
		}
		got := rec.prompts[i]
		if !strings.Contains(got, "do not require sibling deliverables") {
			t.Errorf("%s legacy prompt lacks bounded-scope instruction:\n%s", role, got)
		}
		if strings.Contains(got[strings.Index(got, "## Deliverables"):], "1. "+siblingOutcome) {
			t.Errorf("%s prompt made legacy sibling outcome a numbered deliverable:\n%s", role, got)
		}
		if !strings.Contains(got, siblingFile) {
			t.Errorf("%s prompt lost legacy sibling evidence:\n%s", role, got)
		}
	}
}

func TestPromotedPortionPromptsBindOnlyTheCurrentContract(t *testing.T) {
	rec := &recorder{}
	parentSiblingOutcome := "proposal gates run during stage integration"
	parentSiblingFile := "internal/strategy/structurecheck.go"
	prompt := "Fixes B-344.\n\n" +
		"## Current portion contract (authoritative)\n\n" +
		"**Acceptance slices:**\n" +
		"- canonical artifact vocabulary is rendered\n\n" +
		"**Owns:** internal/service/stages.go\n\n" +
		"## Parent context (non-binding)\n\n" +
		"## Deliverables\n" +
		"- " + parentSiblingOutcome + "\n" +
		"The sibling changes " + parentSiblingFile + ".\n"
	params := pairParams(rec, "green",
		&agent.Outcome{Text: `{"deliverables":[{"id":1,"status":"done"}]}`},
		verdictOutcome("approve"),
	)
	params.Prompt = prompt
	params.Deliverables = ExtractDeliverables("Render canonical artifact vocabulary", prompt)
	params.Rounds = 1

	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}

	implementer, reviewer := "", ""
	for i, role := range rec.roles {
		switch role {
		case config.RoleImplementer:
			implementer = rec.prompts[i]
		case config.RoleReviewer:
			reviewer = rec.prompts[i]
		}
	}
	if implementer == "" || reviewer == "" {
		t.Fatalf("roles = %v, want implementer and reviewer prompts", rec.roles)
	}
	for role, got := range map[string]string{"implementer": implementer, "reviewer": reviewer} {
		if !strings.Contains(got, "Current portion contract") || !strings.Contains(got, "Parent context (non-binding)") {
			t.Errorf("%s prompt lost the bounded task brief:\n%s", role, got)
		}
		if !strings.Contains(got, "do not require sibling deliverables") {
			t.Errorf("%s prompt does not say that parent-context sibling work cannot create a requirement or finding:\n%s", role, got)
		}
		if !strings.Contains(got, parentSiblingOutcome) || !strings.Contains(got, parentSiblingFile) {
			t.Errorf("%s prompt lost parent evidence:\n%s", role, got)
		}
	}
	for role, got := range map[string]string{"implementer": implementer, "reviewer": reviewer} {
		for _, sibling := range []string{parentSiblingOutcome, parentSiblingFile} {
			if strings.Contains(got[strings.Index(got, "## Deliverables"):], "1. "+sibling) {
				t.Errorf("%s prompt made sibling work a numbered current-portion deliverable:\n%s", role, got)
			}
		}
	}
}
