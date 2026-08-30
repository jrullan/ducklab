package stage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/strategy"
)

func projectWith(t *testing.T, files map[artifact.Kind]string) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(artifact.DocsDir(root), 0o755)
	for kind, body := range files {
		os.WriteFile(artifact.Path(root, kind), []byte(body), 0o644)
	}
	return root
}

func replay(text string) func(context.Context, *strategy.Script, string) (string, error) {
	return func(context.Context, *strategy.Script, string) (string, error) { return text, nil }
}

// AC-37: intake produces requirements with REQ ids and frontmatter naming the
// run that made them.
func TestIntakeWritesAProposal(t *testing.T) {
	root := projectWith(t, nil)
	res, err := Run(context.Background(), Params{
		ProjectRoot: root, Stage: Intake, RunID: "r-1", Seed: "Build a timesheet app.",
		Ducklings: []string{"pato-atom"},
		Execute:   replay("## REQ-001 — Users can log time\n\n**Priority:** must\n\nBody.\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != artifact.KindRequirements {
		t.Errorf("kind = %q", res.Kind)
	}

	// Written as a proposal, never as the artifact.
	if _, err := os.Stat(artifact.Path(root, artifact.KindRequirements)); !os.IsNotExist(err) {
		t.Error("the stage wrote the artifact directly")
	}
	proposed, _ := artifact.LoadProposed(root, artifact.KindRequirements)
	if proposed == nil || len(proposed.Sections) != 1 {
		t.Fatal("no proposal written")
	}
	if proposed.Sections[0].ID != "REQ-001" {
		t.Errorf("id = %q", proposed.Sections[0].ID)
	}
	if proposed.Front.RunID != "r-1" {
		t.Errorf("run id = %q", proposed.Front.RunID)
	}
	if proposed.Front.Approved() {
		t.Error("proposal arrived approved")
	}
}

func TestAdoptRunsInventoryBeforeDraftAndCapsIt(t *testing.T) {
	root := projectWith(t, nil)
	calls := 0
	var recorded int
	res, err := Run(context.Background(), Params{
		ProjectRoot: root, Stage: Intake, RunID: "r-inventory", Adopt: true,
		OnInventory: func(inv *agent.Inventory) error { recorded = len(inv.Items); return nil },
		Execute: func(_ context.Context, script *strategy.Script, _ string) (string, error) {
			calls++
			if script.Name == "survey-inventory" {
				items := make([]string, 61)
				for i := range items {
					items[i] = fmt.Sprintf(`{"name":"surface-%d","kind":"service","evidence-path":"internal/surface-%d.go"}`, i, i)
				}
				return fmt.Sprintf(`{"items":[%s]}`, strings.Join(items, ",")), nil
			}
			return "## REQ-001 — Surveyed\n\n**Priority:** must\n\nBody.\n", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || recorded != 60 || res == nil {
		t.Fatalf("inventory calls=%d recorded=%d result=%v", calls, recorded, res != nil)
	}
}

func TestNonAdoptDoesNotRunInventory(t *testing.T) {
	root := projectWith(t, nil)
	calls := 0
	_, err := Run(context.Background(), Params{ProjectRoot: root, Stage: Intake, RunID: "r-normal", Execute: func(_ context.Context, script *strategy.Script, _ string) (string, error) {
		calls++
		if script.Name == "survey-inventory" {
			t.Fatal("non-adopt stage ran inventory")
		}
		return "## REQ-001 — Normal\n\n**Priority:** must\n\nBody.\n", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want one draft", calls)
	}
}

// A stage that produced nothing must not write an empty proposal: accepting it
// would erase the previous artifact.
func TestEmptyOutputIsRejected(t *testing.T) {
	root := projectWith(t, nil)
	_, err := Run(context.Background(), Params{
		ProjectRoot: root, Stage: Intake, RunID: "r-1",
		Execute: replay("I could not think of any requirements, sorry."),
	})
	if err == nil {
		t.Fatal("an empty artifact was accepted")
	}
	if _, statErr := os.Stat(artifact.ProposedPath(root, artifact.KindRequirements)); !os.IsNotExist(statErr) {
		t.Error("an empty proposal was written")
	}
}

// A second intake must not renumber existing requirements out from under the
// spec sections that point at them.
func TestSecondIntakePreservesExistingIDs(t *testing.T) {
	root := projectWith(t, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-004 — Users can log time\n\n**Priority:** must\n",
	})
	res, err := Run(context.Background(), Params{
		ProjectRoot: root, Stage: Intake, RunID: "r-2",
		Execute: replay("## REQ-001 — Users can log time\n\n**Priority:** must\n\n" +
			"## REQ-002 — Managers approve timesheets\n\n**Priority:** should\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Proposed.IDs()
	if got[0] != "REQ-004" {
		t.Errorf("existing requirement renumbered to %q", got[0])
	}
	if got[1] != "REQ-005" {
		t.Errorf("new requirement = %q, want REQ-005", got[1])
	}
}

// AC-39: plan produces milestones and tasks whose ids are unique project-wide.
func TestPlanNumbersTasksAcrossMilestones(t *testing.T) {
	root := projectWith(t, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Login\n\n**Priority:** must\n",
		artifact.KindSpec:         "## SPEC-001 — Tokens\n\n**Implements:** REQ-001\n",
	})
	res, err := Run(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-3",
		Execute: replay(`## M-01 — Auth

### T-001 — Issue tokens

**Implements:** SPEC-001

## M-02 — Reporting

### T-001 — Nightly rollup

**Implements:** SPEC-001
`),
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := res.Proposed.IDs()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id in the plan: %v", ids)
		}
		seen[id] = true
	}
}

// Each stage sees only what it needs; feeding it the whole cycle would bury
// the thing it is meant to work on.
func TestSpecPromptCarriesRequirementsNotThePlan(t *testing.T) {
	root := projectWith(t, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Users can log in\n\n**Priority:** must\n\nDetail here.\n",
		artifact.KindPlan:         "## M-01 — Should not appear\n",
	})
	current, _ := artifact.Load(root, artifact.KindSpec)
	prompt, err := BuildPrompt(root, Spec, "", current, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "REQ-001") || !strings.Contains(prompt, "Detail here.") {
		t.Errorf("spec prompt is missing its requirements:\n%s", prompt)
	}
	if strings.Contains(prompt, "Should not appear") {
		t.Error("spec prompt leaked the plan")
	}
	if !strings.Contains(prompt, "Implements:") {
		t.Error("spec prompt does not ask for the traceability edge")
	}
	// A 236k-char reference corpus once came back as 14 sections mirroring
	// the 14 requirements title-for-title with zero design vocabulary
	// (B-088): the contract must demand the HOW and invite cross-cutting
	// sections, or the skeleton anchors to the requirements list.
	for _, want := range []string{
		"HOW the system delivers",
		"Cross-cutting design",
		"Do not shape the document as one section per requirement",
		"Approved requirement invariant matrix",
		"Treat these as simultaneous constraints",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("spec prompt lacks %q", want)
		}
	}
}

func TestSpecAndPlanPutMustAndWontInOneInvariantMatrix(t *testing.T) {
	root := projectWith(t, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-004 — Desktop integration\n\nRegister a fixed shortcut.\n\n**Priority:** must\n\n" +
			"## REQ-006 — Configurable shortcut\n\nUser configuration is out of scope.\n\n**Priority:** wont\n",
		artifact.KindSpec: "## SPEC-001 — Integration\n\n**Implements:** REQ-004, REQ-006\n\nUse one fixed shortcut.\n",
	})
	for _, name := range []Name{Spec, Plan} {
		current, _ := artifact.Load(root, name.Kind())
		prompt, err := BuildPrompt(root, name, "", current, "", false, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"REQ-004 — Desktop integration", "must", "REQ-006 — Configurable shortcut", "wont"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s invariant matrix lost %q:\n%s", name, want, prompt)
			}
		}
	}
}

// What the person attaches at launch — context, reference documents — must
// reach the architect in every stage that accepts it. Spec refs used to load,
// log, land in the brief, and never enter the prompt (B-086).
func TestSpecAndPlanPromptsCarryTheSeed(t *testing.T) {
	root := projectWith(t, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Users can log in\n\n**Priority:** must\n\nDetail here.\n",
		artifact.KindSpec:         "## SPEC-001 — Login\n\n**Implements:** REQ-001\n\nDetail.\n",
	})
	seed := "## Reference documents\n\nThe wiki spec of the login module."
	for _, name := range []Name{Spec, Plan} {
		current, _ := artifact.Load(root, name.Kind())
		prompt, err := BuildPrompt(root, name, seed, current, "", false, false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(prompt, "The wiki spec of the login module.") {
			t.Errorf("%s prompt dropped the seed:\n%s", name, prompt)
		}
	}
}

// A revision inherits the launch context of the run it revises; the person
// once wrote "use the reference documents" into a revise note and the prompt
// held none of them (B-087).
func TestRevisionPromptCarriesTheSeed(t *testing.T) {
	root := projectWith(t, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Users can log in\n\n**Priority:** must\n\nDetail here.\n",
		artifact.KindSpec:         "## SPEC-001 — Login\n\n**Implements:** REQ-001\n\nDetail.\n",
	})
	current, _ := artifact.Load(root, artifact.KindSpec)
	prompt, err := BuildPrompt(root, Spec, "## Reference documents\n\nThe wiki RBAC spec.", current, "cover RBAC", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "The wiki RBAC spec.") {
		t.Errorf("revision prompt dropped the seed:\n%s", prompt)
	}
	if !strings.Contains(prompt, "cover RBAC") {
		t.Errorf("revision prompt lost the note")
	}
}

// A stage that cannot run says which one to run first, rather than producing
// an artifact with nothing behind it.
func TestSpecWithoutRequirementsFailsWithAnAction(t *testing.T) {
	root := projectWith(t, nil)
	current, _ := artifact.Load(root, artifact.KindSpec)
	_, err := BuildPrompt(root, Spec, "", current, "", false, false)
	if err == nil {
		t.Fatal("spec ran with no requirements")
	}
	if !strings.Contains(err.Error(), "intake") {
		t.Errorf("error does not say what to do: %v", err)
	}
}

func TestPlanWithoutSpecFailsWithAnAction(t *testing.T) {
	root := projectWith(t, nil)
	current, _ := artifact.Load(root, artifact.KindPlan)
	_, err := BuildPrompt(root, Plan, "", current, "", false, false)
	if err == nil || !strings.Contains(err.Error(), "spec") {
		t.Errorf("err = %v", err)
	}
}

// The architect is told where new ids start, so it does not have to guess.
func TestPromptStatesTheNextFreeID(t *testing.T) {
	root := projectWith(t, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-007 — Existing\n\n**Priority:** must\n",
	})
	current, _ := artifact.Load(root, artifact.KindRequirements)
	prompt, _ := BuildPrompt(root, Intake, "brief", current, "", false, false)
	if !strings.Contains(prompt, "REQ-008") {
		t.Errorf("prompt does not state the next free id:\n%s", prompt)
	}
	if !strings.Contains(prompt, "REQ-007") {
		t.Error("prompt does not list the ids that already exist")
	}
}

// With no brief, the architect interviews rather than inventing.
func TestIntakeWithoutASeedAsksTheHuman(t *testing.T) {
	root := projectWith(t, nil)
	current, _ := artifact.Load(root, artifact.KindRequirements)
	prompt, _ := BuildPrompt(root, Intake, "", current, "", false, false)
	if !strings.Contains(prompt, "Ask the human") {
		t.Errorf("prompt does not ask for an interview:\n%s", prompt)
	}
}

// Project memory reaches every stage prompt.
func TestPromptCarriesProjectMemory(t *testing.T) {
	root := projectWith(t, nil)
	m := &artifact.Memory{Description: "A billing product for small firms."}
	artifact.SaveMemory(root, m)

	current, _ := artifact.Load(root, artifact.KindRequirements)
	prompt, _ := BuildPrompt(root, Intake, "brief", current, "", false, false)
	if !strings.Contains(prompt, "billing product") {
		t.Errorf("project memory not injected:\n%s", prompt)
	}
}

func TestUnknownStageIsRejected(t *testing.T) {
	if _, err := Run(context.Background(), Params{Stage: "guessing", Execute: replay("x")}); err == nil {
		t.Error("an unknown stage ran")
	}
	if Valid("release") {
		t.Error("release is not a stage this package runs yet")
	}
}

// Models narrate. "Let me check the requirements I wrote…" is the model
// describing its own process, not part of the artifact.
func TestModelNarrationIsNotKeptInTheArtifact(t *testing.T) {
	root := projectWith(t, nil)
	res, err := Run(context.Background(), Params{
		ProjectRoot: root, Stage: Intake, RunID: "r-1", Seed: "brief",
		Execute: replay(`The reviewer approved. Let me check the requirements I wrote:

1. REQ-001 covers auth
2. REQ-002 covers clients

Here is the final version:

## REQ-001 — Users can log in

**Priority:** must
`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Proposed.Preamble != "" {
		t.Errorf("model narration kept in the artifact:\n%s", res.Proposed.Preamble)
	}
	rendered := artifact.Render(res.Proposed)
	if strings.Contains(rendered, "Let me check") {
		t.Errorf("narration survived rendering:\n%s", rendered)
	}
	if !strings.Contains(rendered, "REQ-001 — Users can log in") {
		t.Error("the actual requirement was lost")
	}
}

// Accept and reject are a verdict on a document that is usually almost right,
// and "almost" has no button. A revision is the third answer: keep it, change
// this one thing.
func TestARevisionAsksForAnEditNotANewDocument(t *testing.T) {
	root := t.TempDir()
	current := &artifact.Document{
		Raw: "## SPEC-001 — Dragging\n\nVertices drag.\n\n## SPEC-004 — Locking\n\nAngles lock.\n",
		Sections: []artifact.Section{
			{ID: "SPEC-001", Title: "Dragging"},
			{ID: "SPEC-004", Title: "Locking"},
		},
	}
	got, err := BuildPrompt(root, Spec, "", current,
		"SPEC-004 should stop the opposite vertex from being dragged", false, false)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"Revise the document below",
		"opposite vertex",           // the note itself
		"Change what the note asks", // and nothing else
		"Keep every id",
		"SPEC-001 — Dragging", // the document it must return unchanged
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the revision prompt is missing %q:\n%s", want, got)
		}
	}
	// It must not still be asking for a fresh document, or the model gets two
	// contradictory jobs and picks one.
	for _, unwanted := range []string{"Write the specification", "Allocate new ids"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the revision prompt still asks for a new document (%q):\n%s", unwanted, got)
		}
	}
}

// Without a note, nothing changes: the ordinary drafting prompt.
func TestNoRevisionMeansTheOrdinaryPrompt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ducklab", "docs", "requirements.md"),
		[]byte("---\nkind: requirements\napproved_by: human\n---\n\n## REQ-001 — A thing\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := BuildPrompt(root, Spec, "", &artifact.Document{}, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Write the specification") {
		t.Errorf("the drafting prompt was lost:\n%s", got)
	}
	if strings.Contains(got, "Revise the document") {
		t.Error("a run with no note was told to revise")
	}
}

// A round is the whole sequence — draft, critique, revise — and the loop stops
// early when the reviewer approves. So raising the limit costs nothing on a
// draft that converges and buys another lap on one that does not.
func TestRoundsOverridesTheScriptsLimit(t *testing.T) {
	council := strategy.ArtifactScript("REQ", "council", nil)
	if council.MaxRounds != 2 {
		t.Fatalf("test assumption broken: council MaxRounds = %d", council.MaxRounds)
	}
	// Zero leaves the script's own answer alone.
	for _, c := range []struct{ rounds, want int }{{0, 2}, {1, 1}, {4, 4}} {
		s := strategy.ArtifactScript("REQ", "council", nil)
		if c.rounds > 0 {
			s.MaxRounds = c.rounds
		}
		if s.MaxRounds != c.want {
			t.Errorf("rounds %d gave MaxRounds %d, want %d", c.rounds, s.MaxRounds, c.want)
		}
	}
}
