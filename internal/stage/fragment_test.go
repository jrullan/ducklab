package stage

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
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
		"smallest semantic delta",
		"'not required' removes a mandatory constraint",
		"**Delete:** yes",
		"REMOVED has no effect",
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

func TestMergeFragmentConsumesExplicitSectionDeletion(t *testing.T) {
	base := &artifact.Document{Sections: []artifact.Section{
		{ID: "REQ-001", Title: "Capture", Body: "keep"},
		{ID: "REQ-011", Title: "File saving excluded", Body: "old exclusion"},
	}}
	out := mergeFragment(base, []artifact.Section{
		{ID: "REQ-011", Title: "File saving excluded", Body: "**Delete:** yes", Fields: map[string]string{"delete": "yes"}},
		{ID: "REQ-999", Title: "Already absent", Body: "**Delete:** yes", Fields: map[string]string{"delete": "yes"}},
	}, "REQ")
	if len(out.Sections) != 1 || out.Sections[0].ID != "REQ-001" {
		t.Fatalf("explicit deletion did not consume the tombstone: %+v", out.Sections)
	}
	if len(base.Sections) != 2 {
		t.Fatal("mergeFragment mutated its input")
	}
}

func TestMergeFragmentAcceptsDeleteMarkerOnHeading(t *testing.T) {
	base := &artifact.Document{Sections: []artifact.Section{{ID: "REQ-005", Title: "Keyboard shortcuts", Body: "old"}}}
	out := mergeFragment(base, []artifact.Section{{
		ID: "REQ-005", Title: "Keyboard shortcuts **Delete:** yes",
	}}, "REQ")
	if len(out.Sections) != 0 {
		t.Fatalf("heading tombstone survived as content: %+v", out.Sections)
	}
}

func TestFragmentDeletionSurvivesLaterMaterialization(t *testing.T) {
	base := &artifact.Document{Sections: []artifact.Section{
		{ID: "REQ-001", Title: "Capture"},
		{ID: "REQ-009", Title: "No saving"},
	}}
	deleted := map[string]bool{}
	tombstone := artifact.Section{ID: "REQ-009", Title: "No saving", Fields: map[string]string{"delete": "yes"}}
	rememberFragmentDeletes([]artifact.Section{tombstone}, deleted)
	first := mergeFragment(base, []artifact.Section{tombstone}, "REQ")
	applyFragmentDeletes(first, deleted)
	if len(first.Sections) != 1 {
		t.Fatalf("first materialization retained deletion: %+v", first.Sections)
	}

	// The next candidate is post-merge, so it contains no tombstone. Merging
	// it against the approved base would resurrect REQ-009 without the ledger.
	rememberFragmentDeletes(first.Sections, deleted)
	second := mergeFragment(base, first.Sections, "REQ")
	applyFragmentDeletes(second, deleted)
	if len(second.Sections) != 1 || second.Sections[0].ID != "REQ-001" {
		t.Fatalf("later materialization resurrected deleted section: %+v", second.Sections)
	}

	restore := artifact.Section{ID: "REQ-009", Title: "Saving", Body: "restored"}
	rememberFragmentDeletes([]artifact.Section{restore}, deleted)
	third := mergeFragment(base, []artifact.Section{restore}, "REQ")
	applyFragmentDeletes(third, deleted)
	if len(third.Sections) != 2 || third.Sections[1].Body != "restored" {
		t.Fatalf("explicit restore stayed tombstoned: %+v", third.Sections)
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

func TestFragmentCouncilRelaxesOnlyArchitectDocumentContracts(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindRequirements, "## REQ-001 — Trigger\n\nOld trigger.\n\n## REQ-002 — Clipboard\n\nUntouched clipboard.\n")
	base, _ := artifact.Load(root, artifact.KindRequirements)
	_, err := runFragment(context.Background(), Params{
		ProjectRoot: root, Stage: Intake, RunID: "r-contract", Mode: "council",
		Execute: func(_ context.Context, script *strategy.Script, _ string) (string, error) {
			if script.MaterializeCandidate == nil {
				t.Fatal("fragment council has no pre-review materializer")
			}
			candidate, materializeErr := script.MaterializeCandidate(nil, &agent.Outcome{Text: "## REQ-001 — Trigger\n\nNew trigger.\n"})
			if materializeErr != nil {
				t.Fatal(materializeErr)
			}
			if !strings.Contains(candidate.Text, "New trigger") || !strings.Contains(candidate.Text, "Untouched clipboard") {
				t.Fatalf("review candidate is not the complete merged document:\n%s", candidate.Text)
			}
			for _, turn := range script.Turns {
				switch turn.Role {
				case config.RoleArchitect:
					if turn.Contract != "" {
						t.Errorf("fragment architect contract = %q, want prompt-defined fragment", turn.Contract)
					}
				case config.RoleReviewer:
					if turn.Contract != "verdict" {
						t.Errorf("fragment reviewer contract = %q, want verdict", turn.Contract)
					}
				}
			}
			return "## REQ-001 — Trigger\n\nNew trigger.\n", nil
		},
	}, base, "change trigger")
	if err != nil {
		t.Fatal(err)
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
	base, err := artifact.Load(root, artifact.KindSpec)
	if err != nil {
		t.Fatal(err)
	}
	hint := coverageGapsHint(root, artifact.KindSpec, base)
	if !strings.Contains(hint, "REQ-002") || !strings.Contains(hint, "Exercise search") {
		t.Errorf("the uncovered requirement is missing from the hint: %q", hint)
	}
	if strings.Contains(hint, "REQ-001") {
		t.Error("a covered requirement was reported as a gap")
	}
	// Coverage says WHAT to write; the code says in what TENSE — the
	// as-built marker is what keeps the plan from tasking finished work.
	for _, must := range []string{"WHOLE assignment", "one at a time", "As-built:", "tense"} {
		if !strings.Contains(hint, must) {
			t.Errorf("the hint lost the code-check instruction %q", must)
		}
	}
	// Other kinds stay quiet; a plan prompt owes the spine nothing here.
	if coverageGapsHint(root, artifact.KindPlan, base) != "" {
		t.Error("the hint leaked outside spec updates")
	}
}

// A proposal revision is based on the pending candidate passed to the stage,
// not necessarily an approved spec visible through LoadSpine. Corrida 13
// revised one duplicate section but was told that nine already-covered
// requirements were gaps, so every repair round appended the spec again.
func TestSpecRevisionCoverageUsesTheMaterializedBase(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindRequirements,
		"## REQ-001 — Capture\n\nMust capture.\n\n"+
			"## REQ-002 — Save\n\nMust save.\n")
	base := &artifact.Document{Sections: []artifact.Section{
		{ID: "SPEC-001", Title: "Capture", Implements: []string{"REQ-001"}},
		{ID: "SPEC-002", Title: "Save", Implements: []string{"REQ-002"}},
	}}
	if hint := coverageGapsHint(root, artifact.KindSpec, base); hint != "" {
		t.Fatalf("materialized proposal was fully covered but got gaps:\n%s", hint)
	}
}

// The plan update's compass, mirrored from the spec's: open spec sections —
// not as-built, not excluded — that no task delivers. A plan update opened
// on the generic "review the project" and the architect enumerated its own
// priorities with the real assignment (four freshly accepted spec sections)
// nowhere in them.
func TestPlanUpdatesCarryTheirGaps(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec,
		"## SPEC-001 — Login\n\n**As-built:** yes\n\nBuilt.\n\n"+
			"## SPEC-002 — Never mind\n\n**Priority:** wont\n\nExcluded.\n\n"+
			"## SPEC-037 — Exercise images\n\nNew behaviour to build.\n\n"+
			"## SPEC-038 — Image selection\n\nAlso new.\n")
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Core\n\n### T-001 — Images groundwork\n\n**Implements:** SPEC-038\n\nStarted.\n")
	hint := planGapsHint(root, artifact.KindPlan)
	if !strings.Contains(hint, "SPEC-037") {
		t.Errorf("the undelivered open section is missing: %q", hint)
	}
	for _, not := range []string{"SPEC-001", "SPEC-002", "SPEC-038"} {
		if strings.Contains(hint, not+" —") {
			t.Errorf("%s should not be a gap (as-built/wont/covered): %q", not, hint)
		}
	}
	if !strings.Contains(hint, "WHOLE assignment") || !strings.Contains(hint, "one\nat a time") && !strings.Contains(hint, "one at a time") {
		t.Errorf("the hint lost its prescription: %q", hint)
	}
	if planGapsHint(root, artifact.KindSpec) != "" {
		t.Error("the plan hint leaked into other kinds")
	}
}

// The night the adopt refresh died: the architect's draft added three real
// sections, a critic asked to VERIFY them, and the revise answered with the
// verification in prose instead of re-typing its own work. The final reply
// parsed to no sections and the engine threw the draft away. A revise that
// stands pat means the draft stands — the engine keeps it.
func TestAStandPatReviseKeepsTheDraft(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Login\n\nFine as is.\n")
	base, _ := artifact.Load(root, artifact.KindSpec)
	res, err := runFragment(context.Background(), Params{
		ProjectRoot: root, Stage: Spec, RunID: "r-sp", Mode: "council",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			// What Execute returns is the FINAL turn: prose.
			return "Verified: all three sections are real shipped capabilities. No changes needed.", nil
		},
		Drafts: func() []string {
			// The architect's earlier draft, newest first.
			return []string{"## SPEC-900 — Advisor\n\nA second model drafts the answer.\n"}
		},
	}, base, "add what shipped since the survey")
	if err != nil {
		t.Fatalf("the stand-pat revise still discarded the draft: %v", err)
	}
	if len(res.Proposed.Sections) != 2 || res.Proposed.Sections[1].Title != "Advisor" {
		t.Fatalf("draft did not survive the revise: %+v", res.Proposed.Sections)
	}
}

// With no draft anywhere, prose is still an answer — the refusal keeps
// speaking exactly as before.
func TestAStandPatWithNoDraftStillRefuses(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Login\n\nFine as is.\n")
	base, _ := artifact.Load(root, artifact.KindSpec)
	_, err := runFragment(context.Background(), Params{
		ProjectRoot: root, Stage: Spec, RunID: "r-sq", Mode: "council",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			return "Nothing should change: the document already covers it.", nil
		},
		Drafts: func() []string { return []string{"also prose, no sections here either"} },
	}, base, "add nothing")
	if err == nil || !strings.Contains(err.Error(), "already covers") {
		t.Fatalf("refusal lost its words: %v", err)
	}
}
