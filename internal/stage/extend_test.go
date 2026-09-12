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
func TestExtendPromptDirectsRetiringSupersededTasksToTaskRemove(t *testing.T) {
	plan := &artifact.Document{Front: artifact.Frontmatter{Kind: artifact.KindPlan}}
	plan.Sections = []artifact.Section{{ID: "M-001", Title: "Core", Children: []artifact.Section{{ID: "T-061", Title: "Old approach"}}}}
	prompt, err := buildExtendPrompt(t.TempDir(), plan, "replace the old approach", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, instruction := range []string{
		"cannot remove tasks",
		"superseded",
		"task_remove",
	} {
		if !strings.Contains(prompt, instruction) {
			t.Errorf("extend prompt must explain retirement: missing %q", instruction)
		}
	}
}

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

	prompt, err := buildExtendPrompt(root, plan, "make the header cosmetic change", "")
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
	if first.Field("milestone") != "" {
		t.Error("the placement field survived in parsed task fields")
	}
	if !strings.Contains(first.Body, "**Implements:** SPEC-004") {
		t.Error("the wiring line was lost from the body")
	}
	// The input document is not mutated: the caller may still hold it.
	if len(current.Sections[1].Children) != 1 {
		t.Error("mergeExtension mutated its input")
	}
}

// B-388: an amendment changes an accepted artifact; it does not claim that the
// file was created twice. The composed plan must preserve that distinction.
func TestMergeExtensionPreservesModificationWithoutDuplicateProducer(t *testing.T) {
	spec, _ := artifact.Parse("## SPEC-001 — Registry\n\nContract.\n", artifact.KindSpec)
	current, _ := artifact.Parse("## M-01 — Core\n\n"+
		"### T-001 — Create registry\n\n**Implements:** SPEC-001\n**Produces:** file:src/registry.rs\n", artifact.KindPlan)
	task := artifact.Section{
		ID: "T-NEW", Title: "Amend registry", Implements: []string{"SPEC-001"},
		Body: "**Implements:** SPEC-001\n**Modifies:** file:src/registry.rs\n**Depends on:** T-001",
		Fields: map[string]string{
			"implements": "SPEC-001", "modifies": "file:src/registry.rs", "depends on": "T-001",
		},
	}
	merged := mergeExtension(current, []artifact.Section{task})
	if errs := artifact.CheckPlan(spec, merged); len(errs) != 0 {
		t.Fatalf("accepted artifact amendment failed composition: %+v", errs)
	}
	landed := merged.Section("T-002")
	if landed == nil || landed.Field("modifies") != "file:src/registry.rs" {
		t.Fatalf("extension lost Modifies field: %#v", landed)
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

func TestExtendRefusesToTurnAnExistingTaskEditIntoADuplicate(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Core\n\n### T-001 — Schema\n\nOriginal body.\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-edit", Mode: "solo",
		Extend: "change only T-001 verification",
		Execute: func(context.Context, *strategy.Script, string) (string, error) {
			return "## T-001 — Schema\n\nChanged body.\n", nil
		},
	}, current)
	if err == nil || !strings.Contains(err.Error(), "rewrite existing task T-001") {
		t.Fatalf("existing task edit was silently converted into a new id: %v", err)
	}
	if prop, pErr := artifact.LoadProposed(root, artifact.KindPlan); pErr != nil || prop != nil {
		t.Fatalf("refused duplicate left a proposal behind: prop=%v err=%v", prop, pErr)
	}
}

// B-371: the composition reviewer could require an existing consumer to wait
// for the newly added prerequisite, but the only legal response was rejected
// as an existing-task rewrite. Dependency-only stubs make that edge explicit
// without granting plan_extend authority over the task body.
func TestExtendAddsANewTaskAndAppendsItsRealIDToAnExistingDependency(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Build\n\nContract.\n")
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Core\n\n"+
			"### T-001 — Foundation\n\nFoundation body.\n\n"+
			"### T-002 — Existing consumer\n\nConsumer body.\n\n**Depends on:** T-001\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	var reviewPrompt string
	res, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-dependency", Mode: "solo",
		Extend: "add a prerequisite and make the existing consumer wait for it",
		Execute: func(_ context.Context, script *strategy.Script, prompt string) (string, error) {
			if script.Name == "composition-review" {
				reviewPrompt = prompt
				return `{"verdict":"approve","findings":[]}`, nil
			}
			return "## T-900 — New prerequisite\n\n**Milestone:** M-001\n**Implements:** SPEC-001\n\nBuild it.\n\n" +
				"## T-002 — Existing consumer\n\n**Milestone:** M-001\n**Depends on:** T-001, T-900\n", nil
		},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	consumer := res.Proposed.Section("T-002")
	if consumer == nil || consumer.Field("depends on") != "T-001, T-003" || !strings.Contains(consumer.Body, "Consumer body.") {
		t.Fatalf("dependency-only amendment corrupted consumer: %+v", consumer)
	}
	if current.Section("T-002").Field("depends on") != "T-001" {
		t.Fatal("dependency amendment mutated the approved plan")
	}
	body := artifact.RenderBody(res.Proposed)
	if prerequisite, consumerAt := strings.Index(body, "### T-003 — New prerequisite"), strings.Index(body, "### T-002 — Existing consumer"); prerequisite < 0 || consumerAt < 0 || prerequisite > consumerAt {
		t.Fatalf("new prerequisite was not placed before its existing consumer:\n%s", body)
	}
	for _, finding := range res.CompositionMechanical {
		if strings.Contains(finding, string(artifact.ForwardDependency)) {
			t.Fatalf("legal dependency-only amendment still trips forward_dependency: %v", res.CompositionMechanical)
		}
	}
	for _, want := range []string{"T-002 Depends on only", "No other existing task"} {
		if !strings.Contains(reviewPrompt, want) {
			t.Errorf("composition scope lost %q:\n%s", want, reviewPrompt)
		}
	}
}

func TestExtendReplacesProofFieldsOnAnUnacceptedExistingTask(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Build\n\nContract.\n")
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Core\n\n"+
			"### T-001 — Existing consumer\n\nConsumer body.\n\n"+
			"**Produces:** file:shared.h, file:consumer.c\n"+
			"**Consumes:** none\n**Exercises:** file:shared.h\n**Verification:** `old-check`\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	res, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-fields", Mode: "solo",
		Extend: "move shared.h to a prerequisite", MutablePlanTasks: map[string]bool{"T-001": true},
		Execute: func(_ context.Context, script *strategy.Script, _ string) (string, error) {
			if script.Name == "composition-review" {
				return `{"verdict":"approve","findings":[]}`, nil
			}
			return "## T-900 — Produce shared header\n\n**Milestone:** M-001\n**Implements:** SPEC-001\n" +
				"**Work unit:** create the shared header\n**Acceptance slices:**\n- header exists\n" +
				"**Acceptance probes:**\n1. `test -f shared.h`\n**Produces:** file:shared.h\n**Consumes:** none\n" +
				"**Verification:** `test -f shared.h`\n**Exercises:** file:shared.h\n\nBuild it.\n\n" +
				"## T-001 — Existing consumer\n\n**Milestone:** M-001\n**Depends on:** T-900\n" +
				"**Produces:** file:consumer.c\n**Consumes:** file:shared.h\n" +
				"**Exercises:** file:consumer.c\n**Verification:** `new-check`\n", nil
		},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	consumer := res.Proposed.Section("T-001")
	if consumer == nil {
		t.Fatal("existing consumer disappeared")
	}
	for field, want := range map[string]string{
		"depends on": "T-002", "produces": "file:consumer.c", "consumes": "file:shared.h",
		"exercises": "file:consumer.c", "verification": "`new-check`",
	} {
		if got := consumer.Field(field); got != want {
			t.Errorf("T-001 %s = %q, want %q", field, got, want)
		}
	}
	if got := strings.Join(res.CompositionMechanical, "\n"); got != "" {
		t.Fatalf("valid field replacement remained mechanically blocked: %s", got)
	}
}

func TestExtendKeepsAcceptedExistingTaskProofFieldsImmutable(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Core\n\n### T-001 — Accepted task\n\n**Produces:** file:shared.h\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-immutable", Mode: "solo",
		Extend: "move shared.h", MutablePlanTasks: map[string]bool{},
		Execute: func(context.Context, *strategy.Script, string) (string, error) {
			return "## T-001 — Accepted task\n\n**Produces:** none\n", nil
		},
	}, current)
	if err == nil || !strings.Contains(err.Error(), "rewrite existing task T-001") {
		t.Fatalf("accepted task proof fields were mutable: %v", err)
	}
}

func TestExtendConsumesSupersededTaskTombstones(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan, "## M-001 — Core\n\n### T-001 — Existing\n\nDone.\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	res, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-tombstone", Mode: "solo", Extend: "add one task",
		Execute: func(_ context.Context, script *strategy.Script, _ string) (string, error) {
			if script.Name == "composition-review" {
				return `{"verdict":"approve","findings":[]}`, nil
			}
			return "## T-900 — Real task\n\nDo it.\n\n" +
				"## T-901 — Superseded duplicate of T-900\n\n**Superseded by:** T-900\n", nil
		},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	body := artifact.RenderBody(res.Proposed)
	if strings.Contains(body, "Superseded") || strings.Count(body, "### T-") != 2 {
		t.Fatalf("superseded tombstone reached the proposal:\n%s", body)
	}
}

func TestDependencyPlacementMovesALaterExistingClosureBeforeTheEarliestConsumer(t *testing.T) {
	plan := &artifact.Document{Sections: []artifact.Section{
		{ID: "M-001", Title: "Core", Children: []artifact.Section{
			{ID: "T-001", Title: "Early consumer"},
		}},
		{ID: "M-002", Title: "Later", Children: []artifact.Section{
			{ID: "T-002", Title: "Later consumer"},
			{ID: "T-003", Title: "Foundation"},
			{ID: "T-004", Title: "Direct prerequisite", Fields: map[string]string{"depends on": "T-003"}},
		}},
	}}
	err := placeDependenciesBeforeConsumers(plan,
		map[string][]string{"T-002": {"T-004"}, "T-001": {"T-004"}})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, milestone := range plan.Sections {
		for _, task := range milestone.Children {
			order = append(order, task.ID)
		}
	}
	if got := strings.Join(order, ","); got != "T-003,T-004,T-001,T-002" {
		t.Fatalf("task order = %s, want dependency closure before earliest consumer", got)
	}
	if len(plan.Sections[0].Children) != 3 || len(plan.Sections[1].Children) != 1 {
		t.Fatalf("new dependency closure was not moved into the consumer milestone: %+v", plan.Sections)
	}
}

// The review probe for B-381: a new prerequisite can itself depend on a later
// existing task. Moving only the new task introduced a second forward edge and
// left the legal dependency-only amendment mechanically blocked.
func TestExtendOrdersANewPrerequisiteWithItsExistingDependency(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Build\n\nContract.\n")
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Consumer\n\n"+
			"### T-001 — Existing consumer\n\n**Implements:** SPEC-001\n**Depends on:** T-002\n\n"+
			"## M-002 — Later foundations\n\n"+
			"### T-002 — Existing prerequisite\n\n**Implements:** SPEC-001\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	res, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-existing-closure", Mode: "solo",
		Extend: "add a prerequisite that uses T-002 and make T-001 wait for it",
		Execute: func(_ context.Context, script *strategy.Script, _ string) (string, error) {
			if script.Name == "composition-review" {
				return `{"verdict":"approve","findings":[]}`, nil
			}
			return "## T-900 — New prerequisite\n\n**Milestone:** M-002\n**Implements:** SPEC-001\n**Depends on:** T-002\n\nBuild it.\n\n" +
				"## T-001 — Existing consumer\n\n**Milestone:** M-001\n**Depends on:** T-002, T-900\n", nil
		},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, milestone := range res.Proposed.Sections {
		for _, task := range milestone.Children {
			order = append(order, task.ID)
		}
	}
	if got := strings.Join(order, ","); got != "T-002,T-003,T-001" {
		t.Fatalf("task order = %s, want existing dependency then new prerequisite then consumer", got)
	}
	for _, finding := range res.CompositionMechanical {
		if strings.Contains(finding, string(artifact.ForwardDependency)) {
			t.Fatalf("dependency closure still trips forward_dependency: %v", res.CompositionMechanical)
		}
	}
}

// B-375: a named H2 is preserved Markdown rather than an indexed task. The
// old fragment merge had no legal representation for replacing it and folded
// an H3 copy into the new task instead.
func TestExtendReplacesAnExistingNamedPlanSectionWithoutSwallowingTasks(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Build\n\nContract.\n")
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Core\n\n"+
			"### T-001 — Foundation\n\nFoundation body.\n\n"+
			"## Traceability closed\n\n| SPEC | Tasks |\n|---|---|\n| 001 | T-001 |\n\n"+
			"### T-002 — Later task\n\nThis task must survive.\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	var reviewPrompt string
	res, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-named", Mode: "solo",
		Extend: "add the new task and update Traceability closed",
		Execute: func(_ context.Context, script *strategy.Script, prompt string) (string, error) {
			if script.Name == "composition-review" {
				reviewPrompt = prompt
				return `{"verdict":"approve","findings":[]}`, nil
			}
			return "## T-900 — New task\n\n**Milestone:** M-001\n**Implements:** SPEC-001\n\nBuild it.\n\n" +
				"## Traceability closed\n\n| SPEC | Tasks |\n|---|---|\n| 001 | T-001,T-003 |\n", nil
		},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	body := artifact.RenderBody(res.Proposed)
	for _, want := range []string{"| 001 | T-001,T-003 |", "### T-002 — Later task", "### T-003 — New task"} {
		if !strings.Contains(body, want) {
			t.Errorf("composed plan lost %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "| 001 | T-001 |") || strings.Count(body, "## Traceability closed") != 1 {
		t.Fatalf("named section was duplicated instead of replaced:\n%s", body)
	}
	if !strings.Contains(reviewPrompt, "## Traceability closed") || !strings.Contains(reviewPrompt, "Engine-authorized changes") {
		t.Fatalf("reviewer was not told the named-section scope:\n%s", reviewPrompt)
	}
}

// B-375: bug-promoted tasks use human H2 subsections in their bodies. They
// are not global plan sections, even when hundreds of tasks repeat the same
// Reported/Triage headings, and must never enter the replacement vocabulary.
func TestExtendKeepsTaskBodyHeadingsOutOfNamedSectionReplacements(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Bugs\n\nContract.\n")
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Bugs\n\n"+
			"### T-001 — First bug\n\nFirst body.\n\n## Reported\n\nFirst report.\n\n## Triage\n\nFirst triage.\n\n"+
			"### T-002 — Second bug\n\nSecond body.\n\n## Reported\n\nSecond report.\n\n## Triage\n\nSecond triage.\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	var architectPrompt string
	res, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-promoted-bug", Mode: "solo",
		Extend: "add another promoted bug task",
		Execute: func(_ context.Context, script *strategy.Script, prompt string) (string, error) {
			if script.Name == "composition-review" {
				return `{"verdict":"approve","findings":[]}`, nil
			}
			architectPrompt = prompt
			return "## T-900 — Third bug\n\n**Milestone:** M-001\n**Implements:** SPEC-001\n\n" +
				"## Reported\n\nThird report.\n\n## Triage\n\nThird triage.\n", nil
		},
	}, current)
	if err != nil {
		t.Fatalf("task-body headings were mistaken for named replacements: %v", err)
	}
	if strings.Contains(architectPrompt, "Named plan sections you may replace") || strings.Contains(architectPrompt, "- ## Reported") {
		t.Fatalf("task-body headings leaked into the replacement prompt:\n%s", architectPrompt)
	}
	third := res.Proposed.Section("T-003")
	if third == nil || !strings.Contains(third.Body, "Reported") || !strings.Contains(third.Body, "Third report") {
		t.Fatalf("promoted-bug body did not merge unchanged: %+v", third)
	}
}

func TestPlanCompositionRejectsANamedSectionWrittenAsH3(t *testing.T) {
	plan, err := artifact.Parse("## M-01 — Core\n\n### T-001 — Task\n\nBody.\n\n### Traceability closed\n\nDuplicate table.\n", artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(invalidPlanSubheadingFindings(plan), "\n")
	if !strings.Contains(got, `non-task H3 heading "Traceability closed"`) {
		t.Fatalf("misleveled named section escaped the mechanical check: %s", got)
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
			if script.Name == "composition-review" {
				return `{"verdict":"approve","findings":[]}`, nil
			}
			for _, turn := range script.Turns {
				if turn.Persona == strategy.PersonaPlanManifest || turn.Contract == "json:plan_manifest" {
					t.Fatalf("plan amendment retained first-draft manifest turn: %+v", turn)
				}
			}
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
			if script.Name == "composition-review" {
				return `{"verdict":"approve","findings":[]}`, nil
			}
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

// From the field, verbatim: an architect fused its milestone and its task
// into one M- heading carrying a complete brief — and the id-only rule filed
// the work as a declaration and refused the run. The person's expectation is
// the law here: an amendment that was given real work always produces work.
// Ask strictly, read generously.
func TestWorkWearingAMilestoneIdIsStillWork(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan,
		"## M-015 — Dashboard UI\n\n### T-114 — Move the streak card\n\nReorder.\n")
	current, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	fragment := "## M-015 — Dashboard UI: Streak card reordering\n" +
		"**Milestone:** M-015\n" +
		"**Implements:** SPEC-007, SPEC-012\n\n" +
		"In `frontend/src/DashboardPage.jsx`, move the Streak card to be the first card " +
		"rendered inside the dashboard grid. No changes to data fetching — only the render order.\n"
	res, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-f", Mode: "solo",
		Extend: "streak card first",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			if script.Name == "composition-review" {
				return `{"verdict":"approve","findings":[]}`, nil
			}
			return fragment, nil
		},
	}, current)
	if err != nil {
		t.Fatalf("real work was refused: %v", err)
	}
	dash := res.Proposed.Sections[0]
	if len(dash.Children) != 2 {
		t.Fatalf("M-015 children = %d, want the old task plus the new one", len(dash.Children))
	}
	task := dash.Children[1]
	if task.ID != "T-115" {
		t.Errorf("id = %s, want the next free T-115", task.ID)
	}
	if !strings.Contains(task.Body, "only the render order") {
		t.Errorf("the brief was lost: %q", task.Body)
	}
	// The Milestone: field named M-015 — an existing milestone — so the task
	// landed there, not in a duplicate.
	if len(res.Proposed.Sections) != 1 {
		t.Errorf("a duplicate milestone was created: %d sections", len(res.Proposed.Sections))
	}
}

// A bare M- heading — empty body or fields only — is still a declaration.
func TestABareMilestoneHeadingIsStillADeclaration(t *testing.T) {
	if !looksLikeMilestoneDecl(artifact.Section{ID: "M-020", Title: "New era"}) {
		t.Error("empty body must read as a declaration")
	}
	if !looksLikeMilestoneDecl(artifact.Section{ID: "M-020", Title: "New era", Body: "**Milestone:** M-020"}) {
		t.Error("fields-only body must read as a declaration")
	}
	if looksLikeMilestoneDecl(artifact.Section{ID: "M-020", Title: "X", Body: "Move the card to the top."}) {
		t.Error("prose is work, whatever the id")
	}
}

// A screenshot says what a paragraph cannot: the amendment's images ride the
// architect's own turn, like a bug's screenshots ride the triager's.
func TestAmendmentImagesRideTheArchitectsTurn(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan, "## M-001 — Core\n\n### T-001 — Schema\n\nDone.\n")
	current, _ := artifact.Load(root, artifact.KindPlan)
	var seen []string
	_, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-i", Mode: "solo",
		Extend: "match this mock",
		Images: []string{"data:image/png;base64,aGk="},
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			if script.Name == "composition-review" {
				return `{"verdict":"approve","findings":[]}`, nil
			}
			for _, turn := range script.Turns {
				seen = append(seen, turn.Images...)
			}
			return "## T-900 — Match the mock\n\nDo it.\n", nil
		},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "data:image/png;base64,aGk=" {
		t.Errorf("the image never reached a turn: %v", seen)
	}
}

// Two contracts, one turn: ArtifactScript demands full-plan M sections while
// the amendment prompt demands a T-900 fragment. One model fused its task
// into an M- heading to satisfy the validator; another obeyed the fragment
// and was executed by the contract — "no sections matching M". An amendment
// turn carries NO document contract; the prompt's fragment rules are the
// only law.
func TestTheAmendmentTurnCarriesNoDocumentContract(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan, "## M-001 — Core\n\n### T-001 — Schema\n\nDone.\n")
	current, _ := artifact.Load(root, artifact.KindPlan)
	_, err := runExtend(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-c", Mode: "solo",
		Extend: "small thing",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			if script.Name == "composition-review" {
				return `{"verdict":"approve","findings":[]}`, nil
			}
			for _, turn := range script.Turns {
				if turn.Contract != "" {
					t.Errorf("turn %s still carries contract %q", turn.Role, turn.Contract)
				}
			}
			return "## T-900 — Small thing\n\nDo it.\n", nil
		},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
}
