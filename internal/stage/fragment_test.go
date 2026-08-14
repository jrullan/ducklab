package stage

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/strategy"
)

// Three spec runs died in one night on three different walls — a turn cap, a
// stream timeout, an output cap — that were all one wall: re-emitting twenty
// thousand tokens to change two hundred. The fragment prompt is an OUTLINE;
// the full text of a section rides only if the architect reads it itself.
func TestTheFragmentPromptIsAnOutline(t *testing.T) {
	root := t.TempDir()
	huge := strings.Repeat("Behavioral detail after behavioral detail. ", 400)
	base := &artifact.Document{Front: artifact.Frontmatter{Kind: artifact.KindSpec}}
	for _, id := range []string{"SPEC-001", "SPEC-002", "SPEC-003"} {
		base.Sections = append(base.Sections, artifact.Section{ID: id, Title: "Title " + id, Body: huge})
	}
	prompt, err := buildFragmentPrompt(root, artifact.KindSpec, base, "add exercise search")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "Behavioral detail") {
		t.Error("section bodies leaked into the prompt — the 20k-token wall again")
	}
	for _, must := range []string{
		"SPEC-002 — Title SPEC-002",
		"add exercise search",
		"Return ONLY the sections you add or change",
		"artifact_read",
		"EXISTING id",
		"SPEC-900",
		"return NO sections",
	} {
		if !strings.Contains(prompt, must) {
			t.Errorf("the prompt lost %q", must)
		}
	}
	if len(prompt) > 4000 {
		t.Errorf("prompt is %d bytes for a three-section doc; it must stay an outline", len(prompt))
	}
}

// The merge: an existing id replaces in place with the id preserved (every
// reference to it stays true); the placeholder appends with the next free
// id; the unchanged majority is copied by code.
func TestMergeFragmentReplacesAndAppends(t *testing.T) {
	base := &artifact.Document{Sections: []artifact.Section{
		{ID: "SPEC-001", Title: "Login", Body: "old login"},
		{ID: "SPEC-002", Title: "Profile", Body: "untouched profile"},
		{ID: "SPEC-007", Title: "Dashboard", Body: "untouched dashboard"},
	}}
	out := mergeFragment(base, []artifact.Section{
		{ID: "SPEC-001", Title: "Login v2", Body: "new login"},
		{ID: "SPEC-900", Title: "Exercise search", Body: "brand new"},
	}, "SPEC")

	if len(out.Sections) != 4 {
		t.Fatalf("sections = %d, want 3 kept + 1 added", len(out.Sections))
	}
	if out.Sections[0].Body != "new login" || out.Sections[0].ID != "SPEC-001" {
		t.Errorf("replacement missed: %+v", out.Sections[0])
	}
	if out.Sections[1].Body != "untouched profile" {
		t.Error("an unemitted section was touched")
	}
	if out.Sections[3].ID != "SPEC-008" || out.Sections[3].Title != "Exercise search" {
		t.Errorf("new section = %s — %s, want SPEC-008 past the highest", out.Sections[3].ID, out.Sections[3].Title)
	}
	if base.Sections[0].Body != "old login" {
		t.Error("mergeFragment mutated its input")
	}
}

// End to end against disk: outline in, fragment out, merged proposal with
// the unchanged majority intact — and the double-contract trap disarmed.
func TestRunFragmentWritesAMergedProposal(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec,
		"## SPEC-001 — Login\n\nThe original login behaviour survives verbatim.\n\n"+
			"## SPEC-002 — Profile\n\nThe original profile behaviour.\n")
	base, err := artifact.Load(root, artifact.KindSpec)
	if err != nil {
		t.Fatal(err)
	}
	res, err := runFragment(context.Background(), Params{
		ProjectRoot: root, Stage: Spec, RunID: "r-frag", Mode: "solo",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			for _, turn := range script.Turns {
				if turn.Contract != "" {
					t.Errorf("turn %s still carries contract %q — the amendment's double-contract trap", turn.Role, turn.Contract)
				}
			}
			return "## SPEC-002 — Profile, editable\n\nPer-field modals now.\n", nil
		},
	}, base, "make the profile editable per field")
	if err != nil {
		t.Fatal(err)
	}
	got := res.Proposed
	if len(got.Sections) != 2 {
		t.Fatalf("sections = %d, want both", len(got.Sections))
	}
	if got.Sections[0].Body != "The original login behaviour survives verbatim." {
		t.Error("the unchanged section did not survive verbatim")
	}
	if !strings.Contains(got.Sections[1].Body, "Per-field modals") || got.Sections[1].Title != "Profile, editable" {
		t.Errorf("the changed section did not land: %+v", got.Sections[1])
	}
	if prop, pErr := artifact.LoadProposed(root, artifact.KindSpec); pErr != nil || prop == nil {
		t.Errorf("no proposal on disk: %v", pErr)
	}
}

// Prose with no sections is the architect's "nothing should change" — an
// answer, delivered with its words, not a parse error.
func TestAFragmentRefusalSpeaks(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Login\n\nFine as is.\n")
	base, _ := artifact.Load(root, artifact.KindSpec)
	_, err := runFragment(context.Background(), Params{
		ProjectRoot: root, Stage: Spec, RunID: "r-r", Mode: "solo",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			return "Nothing should change: the login section already covers this request.", nil
		},
	}, base, "add login")
	if err == nil || !strings.Contains(err.Error(), "already covers") {
		t.Fatalf("the refusal lost the architect's words: %v", err)
	}
}

// The plan updates by fragment too — a 110-task plan redraft died on a 20k
// output cap for re-typing what it kept. Task edits replace in place with
// id and position preserved; a milestone edit keeps custody of nothing (its
// tasks survive); new tasks ride the amendment's placement machinery.
func TestPlanFragmentEditsInPlace(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Core\n\nFoundation.\n\n"+
			"### T-001 — Schema\n\nOriginal schema.\n\n"+
			"### T-002 — Boundary\n\nOriginal boundary.\n\n"+
			"## M-002 — Polish\n\nLater.\n\n"+
			"### T-007 — Colors\n\nOriginal colors.\n")
	base, _ := artifact.Load(root, artifact.KindPlan)
	res, err := runFragment(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-pf", Mode: "solo",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			if !strings.Contains(prompt, "T-012 — Title") && !strings.Contains(prompt, "EXISTING id") {
				t.Error("the plan rules did not reach the prompt")
			}
			return "## T-002 — Boundary, hardened\n\nNew boundary body.\n\n" +
				"## M-002 — Polish and delight\n\nRenamed milestone.\n\n" +
				"## T-900 — Export CSV\n**Milestone:** M-002\n\nAdd the export.\n", nil
		},
	}, base, "harden the boundary, rename polish, add export")
	if err != nil {
		t.Fatal(err)
	}
	got := res.Proposed
	// T-002 replaced in place, inside M-001, id kept.
	if got.Sections[0].Children[1].Body != "New boundary body." || got.Sections[0].Children[1].ID != "T-002" {
		t.Errorf("task edit missed: %+v", got.Sections[0].Children[1])
	}
	if got.Sections[0].Children[0].Body != "Original schema." {
		t.Error("an untouched task changed")
	}
	// M-002 renamed, its child intact.
	if got.Sections[1].Title != "Polish and delight" || len(got.Sections[1].Children) < 1 ||
		got.Sections[1].Children[0].Body != "Original colors." {
		t.Errorf("milestone edit claimed custody: %+v", got.Sections[1])
	}
	// The new task landed under M-002 with the next real id.
	last := got.Sections[1].Children[len(got.Sections[1].Children)-1]
	if last.Title != "Export CSV" || last.ID != "T-008" {
		t.Errorf("new task = %s — %s, want T-008 — Export CSV", last.ID, last.Title)
	}
}

// The spine speaks into spec updates: the engine KNOWS which requirements
// no spec section implements — a sectioned update simply skipped two new
// requirements and nobody noticed until the person read both documents side
// by side. Deterministic knowledge rides the prompt; judgment stays with
// the model.
func TestSpecUpdatesCarryTheCoverageGaps(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindRequirements,
		"## REQ-001 — Login\n\n**Priority:** must\n\nUsers log in.\n\n"+
			"## REQ-002 — Exercise search\n\n**Priority:** must\n\nUsers search the catalog.\n")
	writeDoc(t, root, artifact.KindSpec,
		"## SPEC-001 — Login\n\n**Implements:** REQ-001\n\nLogin flow.\n")
	hint := coverageGapsHint(root, artifact.KindSpec)
	if !strings.Contains(hint, "REQ-002") || !strings.Contains(hint, "Exercise search") {
		t.Errorf("the uncovered requirement is missing from the hint: %q", hint)
	}
	if strings.Contains(hint, "REQ-001") {
		t.Error("a covered requirement was reported as a gap")
	}
	// Other kinds stay quiet; a plan prompt owes the spine nothing here.
	if coverageGapsHint(root, artifact.KindPlan) != "" {
		t.Error("the hint leaked outside spec updates")
	}
}
