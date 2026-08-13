package stage

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/strategy"
)

// A cosmetic two-task amendment carried the whole plan in every prompt — 30k
// tokens a call on a hundred-task project — because it ran as a full-document
// revision. The amendment prompt is an OUTLINE: ids and titles, no bodies.
func TestTheExtendPromptIsAnOutlineNotTheDocument(t *testing.T) {
	root := t.TempDir()
	huge := strings.Repeat("Reported details and triage analysis and acceptance criteria. ", 100)
	plan := &artifact.Document{Front: artifact.Frontmatter{Kind: artifact.KindPlan}}
	plan.Sections = []artifact.Section{{
		ID: "M-001", Title: "Core",
		Children: []artifact.Section{
			{ID: "T-001", Title: "Database schema", Body: huge},
			{ID: "T-002", Title: "User boundary", Body: huge},
		},
	}}
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Snapshot\n\nShows weight.\n")

	prompt, err := buildExtendPrompt(root, plan, "make the header cosmetic change")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "triage analysis") {
		t.Error("task bodies leaked into the prompt — the 30k-token regression again")
	}
	for _, must := range []string{
		"T-001 — Database schema", // the outline
		"SPEC-001 — Snapshot",     // the wiring list
		"make the header cosmetic change",
		"Return ONLY the new task section(s)",
		"real ids are assigned by the engine",
		"Never invent SPEC ids",
		"feature brief",
	} {
		if !strings.Contains(prompt, must) {
			t.Errorf("the prompt lost %q", must)
		}
	}
	if len(prompt) > 4000 {
		t.Errorf("prompt is %d bytes for a two-task plan; it must stay an outline", len(prompt))
	}
}

// The merge is the engine's half of the contract: fresh sequential ids, the
// named milestone honored, the placement field stripped, and every untouched
// section copied by code — which cannot truncate.
func TestMergePlacesTasksAndAssignsRealIDs(t *testing.T) {
	current := &artifact.Document{Sections: []artifact.Section{
		{ID: "M-001", Title: "Core", Children: []artifact.Section{{ID: "T-001", Title: "Schema", Body: "Done."}}},
		{ID: "M-002", Title: "Polish", Children: []artifact.Section{{ID: "T-007", Title: "Old", Body: "x"}}},
	}}
	tasks := []artifact.Section{
		{ID: "T-NEW", Title: "Recolor the header", Body: "**Milestone:** M-002\n**Implements:** SPEC-004\n\nMake it match.", Implements: []string{"SPEC-004"}},
		{ID: "T-NEW", Title: "Second thing", Body: "No milestone named."},
	}
	// Fields as the parser would have them.
	tasks[0].Fields = map[string]string{"milestone": "M-002", "implements": "SPEC-004"}

	out := mergeExtension(current, tasks)
	if len(out.Sections[0].Children) != 1 {
		t.Errorf("M-001 was touched: %d children", len(out.Sections[0].Children))
	}
	polish := out.Sections[1].Children
	if len(polish) != 3 {
		t.Fatalf("M-002 children = %d, want old + both new (fallback lands on last milestone)", len(polish))
	}
	first, second := polish[1], polish[2]
	// Ids continue the plan's own sequence, past its highest.
	if first.ID != "T-008" || second.ID != "T-009" {
		t.Errorf("ids = %s, %s — want T-008, T-009", first.ID, second.ID)
	}
	if strings.Contains(first.Body, "Milestone:") {
		t.Error("the placement field survived into the task body")
	}
	if !strings.Contains(first.Body, "**Implements:** SPEC-004") {
		t.Error("the wiring line was lost from the body")
	}
	// The input document is not mutated: the caller may still hold it.
	if len(current.Sections[1].Children) != 1 {
		t.Error("mergeExtension mutated its input")
	}
}

// No sections is the architect's refusal — the change was core, or the output
// was unusable — and the person deserves its words, not a parse error.
func TestAnEmptyAmendmentFailsWithTheArchitectsWords(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan, "## M-001 — Core\n\n### T-001 — Schema\n\nDone.\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-x",
		Extend: "rewrite the whole product in Rust",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			return "This alters the product's requirements: a rewrite is not an amendment.", nil
		},
	}, current)
	if err == nil {
		t.Fatal("an amendment that added nothing succeeded")
	}
	if !strings.Contains(err.Error(), "alters the product's requirements") {
		t.Errorf("the refusal lost the architect's words: %v", err)
	}
}

// The whole flow against disk: fragment in, merged proposal out, with every
// existing section carried by code.
func TestRunExtendWritesAMergedProposal(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Core\n\n### T-001 — Schema\n\nThe whole original body survives.\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	res, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-amend", Mode: "solo",
		Extend: "recolor the header",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			return "## T-900 — Recolor the header\n\nSwap the palette token.\n", nil
		},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	got := res.Proposed
	if len(got.Sections) != 1 || len(got.Sections[0].Children) != 2 {
		t.Fatalf("merged shape wrong: %+v", got.Sections)
	}
	if got.Sections[0].Children[0].Body != "The whole original body survives." {
		t.Error("the existing task's body was not carried verbatim")
	}
	if got.Sections[0].Children[1].ID != "T-002" {
		t.Errorf("new task id = %s, want T-002", got.Sections[0].Children[1].ID)
	}
	if prop, pErr := artifact.LoadProposed(root, artifact.KindPlan); pErr != nil || prop == nil {
		t.Errorf("no proposal on disk: %v", pErr)
	}
}

// The phantom task, pinned with the exact fragment that created it: an
// architect declaring a NEW milestone above its task. The heading became
// task "Dashboard UI" — title, no body — the person launched it first, and
// a test-writer handed an empty brief invented one. A milestone declaration
// creates a milestone; only tasks become tasks.
func TestAMilestoneDeclarationNeverBecomesATask(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Core\n\n### T-001 — Schema\n\nDone.\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	fragment := "## M-015 — Dashboard UI\n" +
		"## T-900 — Move the Streak card to the top\n" +
		"**Milestone:** M-015\n" +
		"**Implements:** SPEC-007\n\n" +
		"Reorder the JSX only — do not alter the card's content.\n"
	res, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-a", Mode: "solo",
		Extend: "streak card first",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			return fragment, nil
		},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	got := res.Proposed
	if len(got.Sections) != 2 {
		t.Fatalf("sections = %d, want Core + the new Dashboard UI milestone", len(got.Sections))
	}
	dash := got.Sections[1]
	if dash.Title != "Dashboard UI" || dash.ID != "M-002" {
		t.Errorf("new milestone = %s — %s, want M-002 — Dashboard UI", dash.ID, dash.Title)
	}
	if len(dash.Children) != 1 {
		t.Fatalf("dashboard tasks = %d, want exactly the real one — no phantom", len(dash.Children))
	}
	task := dash.Children[0]
	if task.ID != "T-002" || !strings.Contains(task.Body, "Reorder the JSX only") {
		t.Errorf("the real task lost its id or body: %s %q", task.ID, task.Body)
	}
	// And nothing empty-bodied landed anywhere.
	for _, m := range got.Sections {
		for _, c := range m.Children {
			if strings.TrimSpace(c.Body) == "" {
				t.Errorf("phantom task %s — %s with empty body", c.ID, c.Title)
			}
		}
	}
}

// A fragment that is ONLY a milestone declaration added no work: refusal.
func TestAMilestoneAloneIsNotAnAmendment(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan, "## M-001 — Core\n\n### T-001 — Schema\n\nDone.\n")
	current, _ := artifact.Load(root, artifact.KindPlan)
	_, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-b",
		Extend: "something",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			return "## M-020 — A new era\n", nil
		},
	}, current)
	if err == nil {
		t.Fatal("a milestone with no tasks was accepted as an amendment")
	}
}
