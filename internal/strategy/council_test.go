package strategy

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

func councilParams(rec *recorder, outcomes ...*agent.Outcome) *ExecuteParams {
	return &ExecuteParams{
		Prompt: "Write the requirements for a timesheet app.",
		Runner: rec.runner(outcomes...),
		Roster: map[config.Role]config.DucklingID{
			config.RoleArchitect: "pato-atom",
			config.RoleReviewer:  "pato-local",
		},
	}
}

func TestCouncilScriptValidates(t *testing.T) {
	if err := CouncilScript("REQ", nil).Validate(testRegistry(t)); err != nil {
		t.Fatalf("council does not validate: %v", err)
	}
}

// The reviewer must see the draft. Anonymize controls WHO is shown, not
// whether the transcript appears — conflating them left council's reviewer
// reviewing nothing at all.
func TestCouncilReviewerSeesTheDraft(t *testing.T) {
	rec := &recorder{}
	draft := "## REQ-001 — Users can log time\n\nBody of the draft."
	params := councilParams(rec,
		&agent.Outcome{Text: draft},
		verdictOutcome("approve"),
		&agent.Outcome{Text: draft},
	)
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	if len(rec.prompts) < 2 {
		t.Fatalf("only %d turns ran", len(rec.prompts))
	}
	reviewerPrompt := rec.prompts[1]
	if !strings.Contains(reviewerPrompt, "Body of the draft.") {
		t.Errorf("the reviewer was not shown the draft:\n%s", reviewerPrompt)
	}
}

// A finding-free approval settles the council at the reviewer. Requiring the
// architect to repeat an already-approved document spent a full turn per
// successful round and created another opportunity to regress it.
func TestCouncilApprovalSkipsTheFinalRevision(t *testing.T) {
	rec := &recorder{}
	draft := &agent.Outcome{Text: "## REQ-001 — Draft\n\nApproved body.\n\n**Priority:** must", Parsed: []agent.Section{{ID: "REQ-001", Title: "Draft", Body: "Approved body.\n\n**Priority:** must"}}}
	res, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), councilParams(rec,
		draft,
		verdictOutcome("approve"),
		&agent.Outcome{Text: "this turn must not run"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.roles) != 2 || rec.roles[0] != config.RoleArchitect || rec.roles[1] != config.RoleReviewer {
		t.Fatalf("roles = %v, want architect → reviewer", rec.roles)
	}
	if res.Text != draft.Text {
		t.Fatalf("proposal = %q, want reviewed draft %q", res.Text, draft.Text)
	}
}

func TestFragmentCouncilParsesApprovalAndCarriesARequestedRevision(t *testing.T) {
	script := CouncilScript("REQ", nil)
	for i := range script.Turns {
		if script.Turns[i].Role == config.RoleArchitect {
			script.Turns[i].Contract = "" // fragment update shape
		}
	}
	rec := &recorder{}
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n\nold"},
		verdictOutcome("request-changes", agent.Finding{Severity: "major", Issue: "old", Fix: "write new"}),
		&agent.Outcome{Text: "## REQ-001 — Draft\n\nnew"},
		verdictOutcome("approve"),
	)
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Role{config.RoleArchitect, config.RoleReviewer, config.RoleArchitect, config.RoleReviewer}
	if !slices.Equal(rec.roles, want) {
		t.Fatalf("roles = %v, want %v (no opening or closing architect in round 2)", rec.roles, want)
	}
	if res.Rounds != 2 || !strings.Contains(res.Text, "new") {
		t.Fatalf("result rounds=%d text=%q, want carried R1 revision approved in R2", res.Rounds, res.Text)
	}
}

// A revision that cannot see the critique is just a second draft.
func TestCouncilArchitectSeesTheCritique(t *testing.T) {
	rec := &recorder{}
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("request-changes", agent.Finding{
			Severity: "major", File: "requirements.md",
			Issue: "REQ-001 does not say what is out of scope", Fix: "add a scope line",
		}),
		&agent.Outcome{Text: "## REQ-001 — Revised\n"},
	)
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	if len(rec.prompts) < 3 {
		t.Fatalf("the revision turn never ran (%d turns)", len(rec.prompts))
	}
	revision := rec.prompts[2]
	if !strings.Contains(revision, "out of scope") {
		t.Errorf("the architect's revision could not see the critique:\n%s", revision)
	}
}

// pair keeps the opposite rule: its reviewer must NOT read the author's
// reasoning, or the second model stops being decorrelated.
func TestPairReviewerStillCannotSeeTheAuthorsReasoning(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "green",
		editsOutcome("I changed it because the operator was inverted"),
		verdictOutcome("approve"),
	)
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.prompts[1], "because the operator was inverted") {
		t.Error("pair's reviewer was shown the author's reasoning")
	}
}

func TestCouncilSkipsTheHumanTurnWhenUnattended(t *testing.T) {
	rec := &recorder{}
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("approve"),
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
	)
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	for _, role := range rec.roles {
		if role == config.RoleHuman {
			t.Error("a human turn ran with no human present")
		}
	}
}

// Four rounds at most: a steadily converging candidate gets one final
// reviewed repair, but the conversation remains bounded.
func TestCouncilStopsAtFourRounds(t *testing.T) {
	rec := &recorder{}
	params := councilParams(rec)
	res, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds > 4 {
		t.Errorf("ran %d rounds", res.Rounds)
	}
}

func TestCouncilContractFollowsThePrefix(t *testing.T) {
	for prefix, want := range map[string]string{"REQ": "markdown_sections:REQ", "SPEC": "markdown_sections:SPEC", "M": "markdown_sections:M"} {
		got := ""
		for _, turn := range CouncilScript(prefix, nil).Turns {
			if strings.HasPrefix(turn.Contract, "markdown_sections:") {
				got = turn.Contract
				break
			}
		}
		if got != want {
			t.Errorf("prefix %q: contract = %q, want %q", prefix, got, want)
		}
	}
}

func TestPlanCouncilPreflightsATopologyManifestWithoutTools(t *testing.T) {
	turns := CouncilScript("M", nil).Turns
	if len(turns) < 3 || turns[0].Contract != "json:plan_manifest" || turns[0].Persona != PersonaPlanManifest || turns[0].Toolbelt != "none" {
		t.Fatalf("plan opening turn = %+v, want tool-free manifest", turns[0])
	}
	if turns[1].Persona != PersonaPlanManifestCritic || turns[1].Contract != "verdict" || turns[1].Toolbelt != "none" {
		t.Fatalf("plan manifest review turn = %+v", turns[1])
	}
	if turns[2].Contract != "markdown_sections:M" {
		t.Fatalf("plan document turn = %+v", turns[2])
	}
}

func TestStandardSupportProfileKeepsSelfContainedManifestCriticToolFree(t *testing.T) {
	manifestCritic := Turn{Role: config.RoleReviewer, Toolbelt: "none", Persona: PersonaPlanManifestCritic}
	applySupportProfile(&manifestCritic, false)
	if manifestCritic.Toolbelt != "none" {
		t.Fatalf("standard manifest critic toolbelt = %q, want none", manifestCritic.Toolbelt)
	}
	critic := Turn{Role: config.RoleReviewer, Toolbelt: "none", Persona: PersonaCritic}
	applySupportProfile(&critic, false)
	if critic.Toolbelt != "read-only" {
		t.Fatalf("standard document critic toolbelt = %q, want read-only", critic.Toolbelt)
	}
	smallCritic := Turn{Role: config.RoleReviewer, Toolbelt: "none", Persona: PersonaCritic}
	applySupportProfile(&smallCritic, true)
	if smallCritic.Toolbelt != "none" {
		t.Fatalf("small critic toolbelt = %q, want none", smallCritic.Toolbelt)
	}
	architect := Turn{Role: config.RoleArchitect, Toolbelt: "none", Persona: PersonaPlanManifest}
	applySupportProfile(&architect, false)
	if architect.Toolbelt != "none" {
		t.Fatalf("architect toolbelt changed to %q", architect.Toolbelt)
	}
}

func TestPlanManifestReviewContractNamesEverySpecAndTask(t *testing.T) {
	manifest := &agent.Outcome{Parsed: &agent.PlanManifest{Milestones: []agent.ManifestMilestone{{
		ID: "M-01", Tasks: []agent.ManifestTask{{ID: "T-002"}, {ID: "T-001"}},
	}}}}
	params := &ExecuteParams{KnownIDs: map[string]bool{"REQ-001": true, "SPEC-008": true, "SPEC-001": true}}
	got := planManifestReviewContract(params, manifest)
	if got != "verdict:plan_manifest:SPEC-001,SPEC-008|T-001,T-002" {
		t.Fatalf("manifest review contract = %q", got)
	}
}

func TestPlanManifestReviewContractUsesOnlyInScopeCoverageSlots(t *testing.T) {
	manifest := &agent.Outcome{Parsed: &agent.PlanManifest{Milestones: []agent.ManifestMilestone{{
		ID: "M-01", Tasks: []agent.ManifestTask{{ID: "T-001"}},
	}}}}
	params := &ExecuteParams{
		KnownIDs: map[string]bool{"SPEC-001": true, "SPEC-002": true, "SPEC-003": true},
		PlanSeed: []PlanSeedSpec{
			{ID: "SPEC-001", Priority: "must"},
			{ID: "SPEC-002", Priority: "wont"},
			{ID: "SPEC-003", Priority: "could"},
		},
	}
	got := planManifestReviewContract(params, manifest)
	if got != "verdict:plan_manifest:SPEC-001|T-001" {
		t.Fatalf("manifest review contract = %q, want only the engine-owned coverage slot", got)
	}
}

func TestPlanCouncilRendersAndApprovesValidatedManifest(t *testing.T) {
	manifestText := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build the app","acceptance_slices":["the app compiles"],"acceptance_probes":["meson compile -C build"],"produces":["file:meson.build","build-target:app"],"consumes":[],"verification":"meson compile -C build"}]}]}`
	manifest := &agent.Outcome{Text: "```json\n" + manifestText + "\n```", Parsed: &agent.PlanManifest{Milestones: []agent.ManifestMilestone{{
		ID: "M-01", Title: "Setup", Tasks: []agent.ManifestTask{{ID: "T-001", Title: "Build", Implements: []string{"SPEC-001"}, WorkUnit: "build the app", AcceptanceSlices: []string{"the app compiles"}, AcceptanceProbes: []string{"meson compile -C build"}, Produces: []string{"file:meson.build", "build-target:app"}, Consumes: []string{}, Verification: "meson compile -C build"}},
	}}}}
	planText := "## M-01 — Setup\n\n### T-001 — Build\n\n**Implements:** SPEC-001\n**Produces:** file:meson.build, build-target:app\n**Consumes:** none\n**Verification:** `meson compile -C build`"
	plan := &agent.Outcome{Text: planText, Parsed: []agent.Section{{ID: "M-01", Title: "Setup", Body: strings.SplitN(planText, "\n\n", 2)[1]}}}
	rec := &recorder{}
	params := councilParams(rec, manifest, verdictOutcome("approve"), plan, verdictOutcome("approve"))
	params.ExecContext = &tools.ExecContext{}
	baseRunner := params.Runner
	params.Runner = func(ctx context.Context, turn *Turn, duckling config.DucklingID, prompt string, toolbelt []string, tc TurnContext) (*agent.Outcome, error) {
		if turn.Persona == PersonaPlanManifestCritic && params.ExecContext.DraftUnderReview["plan"] != manifestText {
			t.Fatalf("manifest critic draft = %q", params.ExecContext.DraftUnderReview["plan"])
		}
		if turn.Persona == PersonaPlanManifestCritic && strings.Contains(prompt, "```json\n```json") {
			t.Fatalf("manifest candidate was double fenced:\n%s", prompt)
		}
		return baseRunner(ctx, turn, duckling, prompt, toolbelt, tc)
	}
	res, err := ExecuteScript(context.Background(), CouncilScript("M", nil), params)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rec.roles, []config.Role{config.RoleArchitect, config.RoleReviewer, config.RoleArchitect, config.RoleReviewer}) {
		t.Fatalf("plan roles = %v", rec.roles)
	}
	if !strings.Contains(res.Text, "### T-001") || !strings.Contains(res.Text, "**Owns:**") {
		t.Fatalf("rendered plan = %s", res.Text)
	}
}

func TestPlanManifestIsReviewedBeforeFreezeAndRegeneratedAtMostThreeTimes(t *testing.T) {
	manifestText := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build the app","acceptance_slices":["the app compiles"],"acceptance_probes":["true"],"produces":["file:app"],"consumes":[],"verification":"true"}]}]}`
	parsedManifest, err := agent.ParseContract("json:plan_manifest", manifestText)
	if err != nil {
		t.Fatal(err)
	}
	manifest := &agent.Outcome{Text: manifestText, Parsed: parsedManifest}
	patchText := `{"operations":[{"op":"replace_task","task_id":"T-001","milestone_id":"M-01","task":{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build the app","acceptance_slices":["the app compiles"],"acceptance_probes":["true"],"produces":["file:app"],"consumes":[],"verification":"true"}}]}`
	parsedPatch, err := agent.ParseContract("json:plan_manifest_patch", patchText)
	if err != nil {
		t.Fatal(err)
	}
	patch := &agent.Outcome{Text: patchText, Parsed: parsedPatch}
	planText := "## M-01 — Setup\n\n### T-001 — Build\n\nBuild it.\n\n**Implements:** SPEC-001\n\n**Produces:** file:app\n\n**Consumes:** none\n\n**Verification:** `true`"
	parsedPlan, err := agent.ParseContract("markdown_sections:M", planText)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	res, err := ExecuteScript(context.Background(), CouncilScript("M", nil), councilParams(rec,
		manifest,
		verdictOutcome("request-changes", agent.Finding{Severity: "major", Issue: "two unrelated concerns are bundled", Fix: "repartition the existing manifest"}),
		patch,
		verdictOutcome("approve"),
		&agent.Outcome{Text: planText, Parsed: parsedPlan},
		verdictOutcome("approve"),
	))
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Role{config.RoleArchitect, config.RoleReviewer, config.RoleArchitect, config.RoleReviewer, config.RoleArchitect, config.RoleReviewer}
	if !slices.Equal(rec.roles, want) {
		t.Fatalf("roles = %v, want %v", rec.roles, want)
	}
	if !strings.Contains(rec.prompts[2], "two unrelated concerns are bundled") {
		t.Fatalf("manifest patch could not see semantic rejection:\n%s", rec.prompts[2])
	}
	if rec.contracts[2] != "json:plan_manifest_patch" || !strings.Contains(rec.prompts[2], "Canonical plan manifest — patch this object") {
		t.Fatalf("second architect was not constrained to a transactional patch: contract=%q\n%s", rec.contracts[2], rec.prompts[2])
	}
	if !strings.Contains(rec.prompts[1], "Compact plan manifest audit — required") ||
		!strings.Contains(rec.prompts[1], "Plan manifest candidate — authoritative") {
		t.Fatalf("manifest critic did not receive its candidate and policy:\n%s", rec.prompts[1])
	}
	for _, prohibited := range []string{"Markdown rendering fields such as Owns", "Depends on", "Assumption", "Do not request any key outside this schema"} {
		if !strings.Contains(rec.prompts[1], prohibited) {
			t.Errorf("manifest critic prompt lacks protocol isolation %q:\n%s", prohibited, rec.prompts[1])
		}
	}
	for _, semantic := range []string{"same actor", "do not split merely by", "Implements is a many-to-many trace link", "do not reject duplicate Implements links"} {
		if !strings.Contains(rec.prompts[1], semantic) {
			t.Errorf("manifest critic prompt lacks cohesion rule %q:\n%s", semantic, rec.prompts[1])
		}
	}
	normalizedPrompt := strings.Join(strings.Fields(rec.prompts[1]), " ")
	for _, ownership := range []string{"exclusive writable ownership", "one producer in the whole manifest", "Consumes is read-only", "Never tell a task to add an artifact to Produces when another task already produces it"} {
		if !strings.Contains(normalizedPrompt, ownership) {
			t.Errorf("manifest critic prompt lacks ownership invariant %q:\n%s", ownership, rec.prompts[1])
		}
	}
	if !strings.Contains(res.Text, "### T-001") {
		t.Fatalf("approved manifest was not rendered: %s", res.Text)
	}
}

func TestPlanManifestPatchRetriesOnceWhenItCannotApplyToCanonicalBase(t *testing.T) {
	manifestText := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build the app","acceptance_slices":["the app compiles"],"acceptance_probes":["true"],"produces":["file:app"],"consumes":[],"verification":"true"}]}]}`
	parsedManifest, err := agent.ParseContract("json:plan_manifest", manifestText)
	if err != nil {
		t.Fatal(err)
	}
	manifest := &agent.Outcome{Text: manifestText, Parsed: parsedManifest}
	patchFor := func(id string) *agent.Outcome {
		text := `{"operations":[{"op":"replace_task","task_id":"` + id + `","milestone_id":"M-01","task":{"id":"` + id + `","title":"Build","implements":["SPEC-001"],"work_unit":"build the app","acceptance_slices":["the app compiles"],"acceptance_probes":["true"],"produces":["file:app"],"consumes":[],"verification":"true"}}]}`
		parsed, parseErr := agent.ParseContract("json:plan_manifest_patch", text)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		return &agent.Outcome{Text: text, Parsed: parsed}
	}
	planText := "## M-01 — Setup\n\n### T-001 — Build\n\nBuild it.\n\n**Implements:** SPEC-001\n\n**Produces:** file:app\n\n**Consumes:** none\n\n**Verification:** `true`"
	parsedPlan, err := agent.ParseContract("markdown_sections:M", planText)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	var events []string
	params := councilParams(rec,
		manifest,
		verdictOutcome("request-changes", agent.Finding{Severity: "major", Issue: "tighten T-001", Fix: "replace T-001"}),
		patchFor("T-999"),
		patchFor("T-001"),
		verdictOutcome("approve"),
		&agent.Outcome{Text: planText, Parsed: parsedPlan},
		verdictOutcome("approve"),
	)
	params.OnEvent = func(kind string, _ map[string]interface{}) { events = append(events, kind) }
	res, err := ExecuteScript(context.Background(), CouncilScript("M", nil), params)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(events, "plan_manifest_patch_rejected") || !slices.Contains(events, "plan_manifest_patched") {
		t.Fatalf("patch application evidence = %v", events)
	}
	if len(rec.prompts) < 4 || !strings.Contains(rec.prompts[3], "replace target T-999 does not exist") {
		t.Fatalf("application error was not fed to bounded retry: prompts=%d", len(rec.prompts))
	}
	if !strings.Contains(res.Text, "### T-001") {
		t.Fatalf("valid retry did not reach the rendered plan: %s", res.Text)
	}
}

func TestPlanManifestSemanticReviewStopsAfterThreeRejectedCandidates(t *testing.T) {
	manifestText := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build the app","acceptance_slices":["the app compiles"],"acceptance_probes":["true"],"produces":["file:app"],"consumes":[],"verification":"true"}]}]}`
	parsedManifest, err := agent.ParseContract("json:plan_manifest", manifestText)
	if err != nil {
		t.Fatal(err)
	}
	patchText := `{"operations":[{"op":"replace_task","task_id":"T-001","milestone_id":"M-01","task":{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build the app","acceptance_slices":["the app compiles"],"acceptance_probes":["true"],"produces":["file:app"],"consumes":[],"verification":"true"}}]}`
	parsedPatch, err := agent.ParseContract("json:plan_manifest_patch", patchText)
	if err != nil {
		t.Fatal(err)
	}
	authors, critics, documents := 0, 0, 0
	params := councilParams(&recorder{})
	params.Runner = func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
		switch turn.Persona {
		case PersonaPlanManifest:
			authors++
			if turn.Contract == "json:plan_manifest_patch" {
				return &agent.Outcome{Text: patchText, Parsed: parsedPatch}, nil
			}
			return &agent.Outcome{Text: manifestText, Parsed: parsedManifest}, nil
		case PersonaPlanManifestCritic:
			critics++
			return verdictOutcome("request-changes", agent.Finding{Severity: "major", Issue: "concerns remain bundled", Fix: "repartition the existing manifest"}), nil
		default:
			documents++
			return nil, fmt.Errorf("document turn must not run")
		}
	}
	if _, err := ExecuteScript(context.Background(), CouncilScript("M", nil), params); err != ErrStructureFailed {
		t.Fatalf("error = %v, want ErrStructureFailed", err)
	}
	if authors != 3 || critics != 3 || documents != 0 {
		t.Fatalf("authors=%d critics=%d documents=%d, want 3/3/0", authors, critics, documents)
	}
}

// H1d's first Fledge plan named SPEC-001 on its scaffolding task, so every
// mechanical trace check was green even though no acceptance slice delivered
// SPEC-001's authority boundary. A plan critic must be assigned the semantic
// audit explicitly; a generic "anything missing?" review approved that plan.
func TestPlanCriticAuditsObligationsNotJustImplementsIDs(t *testing.T) {
	manifestText := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Scaffold","implements":["SPEC-001"],"work_unit":"scaffold the crate","acceptance_slices":["the crate checks"],"acceptance_probes":["cargo check"],"produces":["file:Cargo.toml"],"consumes":[],"verification":"cargo check"}]}]}`
	manifest, err := agent.ParseContract("json:plan_manifest", manifestText)
	if err != nil {
		t.Fatal(err)
	}
	planText := "## M-01 — Setup\n\n### T-001 — Scaffold\n\n**Implements:** SPEC-001\n\n**Produces:** file:Cargo.toml\n\n**Consumes:** none\n\n**Verification:** `cargo check`"
	parsed, err := agent.ParseContract("markdown_sections:M", planText)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	_, err = ExecuteScript(context.Background(), CouncilScript("M", nil), councilParams(rec,
		&agent.Outcome{Text: manifestText, Parsed: manifest},
		verdictOutcome("approve"),
		&agent.Outcome{Text: planText, Parsed: parsed},
		verdictOutcome("approve"),
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.prompts) < 3 {
		t.Fatalf("only %d turns ran", len(rec.prompts))
	}
	critic := rec.prompts[3]
	for _, want := range []string{
		"Plan obligation audit — required",
		"An **Implements:** id is an index pointer, never evidence",
		"including sections absent from all Implements lines",
		"authority/boundary rules",
		"A `could` section needs work only when the accepted",
		"A `wont` section is a boundary to preserve",
		"an Assumption, Out of scope clause",
		"actor/action/object relations",
		"permitted aggregate boundary for its child",
		"Name the exact\n  SPEC id and omitted obligation",
		"Do not infer coverage merely",
	} {
		if !strings.Contains(critic, want) {
			t.Errorf("plan critic prompt lacks %q:\n%s", want, critic)
		}
	}
	if !strings.HasSuffix(critic, planCoverageReview) {
		t.Errorf("plan critic policy is not the final prompt section:\n%s", critic)
	}
}

// H1e's final plan reviewer approved a candidate after the ordinary reviewer
// had found — and the architect had merely reworded — a missing mandatory
// behavior. The final route must receive the exact same obligation policy as
// every critic in the repair loop, not just the preceding finding ledger.
func TestPlanFinalReviewReceivesTheSameObligationPolicy(t *testing.T) {
	script := CouncilScript("M", nil)
	manifestText := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build the crate","acceptance_slices":["the crate checks"],"acceptance_probes":["cargo check"],"produces":["file:Cargo.toml"],"consumes":[],"verification":"cargo check"}]}]}`
	manifest, err := agent.ParseContract("json:plan_manifest", manifestText)
	if err != nil {
		t.Fatal(err)
	}
	planForRevision := func(revision int) *agent.Outcome {
		planText := fmt.Sprintf("## M-01 — Setup\n\n### T-001 — Build\n\nRevision %d.\n\n**Implements:** SPEC-001\n\n**Work unit:** Build the package\n\n**Acceptance slices:**\n- The package builds.\n\n**Produces:** file:Cargo.toml\n\n**Consumes:** none\n\n**Verification:** `cargo check`\n\n**Exercises:** file:Cargo.toml\n\n**Out of scope:** Publishing.\n\n**Assumption:** Cargo is installed.", revision)
		return &agent.Outcome{Text: planText, Parsed: []agent.Section{{ID: "M-01", Title: "Setup", Body: strings.SplitN(planText, "\n\n", 2)[1]}}}
	}

	reviewerTurns, architectTurns := 0, 0
	var finalPrompt string
	params := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, tc TurnContext) (*agent.Outcome, error) {
			switch {
			case turn.Persona == PersonaPlanManifest:
				return &agent.Outcome{Text: manifestText, Parsed: manifest}, nil
			case turn.Persona == PersonaPlanManifestCritic:
				return verdictOutcome("approve"), nil
			case turn.Role == config.RoleReviewer:
				reviewerTurns++
				if tc.Index >= len(script.Turns) {
					finalPrompt = prompt
					return verdictOutcome("approve"), nil
				}
				return verdictOutcome("request-changes", agent.Finding{Severity: "major", File: "draft", Issue: "missing obligation", Fix: "add a slice"}), nil
			default:
				architectTurns++
				return planForRevision(architectTurns), nil
			}
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	if _, err := ExecuteScript(context.Background(), script, params); err != nil {
		t.Fatal(err)
	}
	if reviewerTurns != 5 {
		t.Fatalf("reviewer turns = %d, want four round reviews plus final verification", reviewerTurns)
	}
	for _, want := range []string{
		"Final candidate under review",
		"Plan obligation audit — required",
		"including sections absent from all Implements lines",
		"A `could` section needs work only when the accepted",
		"an Assumption, Out of scope clause",
		"actor/action/object relations",
		"permitted aggregate boundary for its child",
	} {
		if !strings.Contains(finalPrompt, want) {
			t.Errorf("final plan critic prompt lacks %q:\n%s", want, finalPrompt)
		}
	}
	policyAt := strings.LastIndex(finalPrompt, "Plan obligation audit — required")
	if policyAt < strings.LastIndex(finalPrompt, "Final candidate under review") ||
		policyAt < strings.LastIndex(finalPrompt, "Open finding ledger") {
		t.Errorf("final plan critic policy is buried before candidate or ledger:\n%s", finalPrompt)
	}
}

func TestPlanCouncilLetsReviewedRevisionCorrectManifestSemantics(t *testing.T) {
	manifestText := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-008"],"work_unit":"build the app","acceptance_slices":["the app compiles"],"acceptance_probes":["meson compile -C build"],"produces":["file:meson.build"],"consumes":[],"verification":"meson compile -C build"}]}]}`
	manifest, err := agent.ParseContract("json:plan_manifest", manifestText)
	if err != nil {
		t.Fatal(err)
	}
	planText := func(spec, workUnit string) string {
		return "## M-01 — Setup\n\n### T-001 — Build\n\nBuild the application entry point.\n\n**Deliverables:**\n- A compilable application.\n\n**Implements:** " + spec + "\n\n**Work unit:** " + workUnit + "\n\n**Produces:** file:meson.build\n\n**Consumes:** none\n\n**Verification:** `meson compile -C build`\n\n**Exercises:** file:meson.build\n\n**Out of scope:** Packaging.\n\n**Assumption:** Meson is installed."
	}
	parsedPlan := func(text string) *agent.Outcome {
		parsed, parseErr := agent.ParseContract("markdown_sections:M", text)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		return &agent.Outcome{Text: text, Parsed: parsed}
	}
	rec := &recorder{}
	res, err := ExecuteScript(context.Background(), CouncilScript("M", nil), councilParams(rec,
		&agent.Outcome{Text: manifestText, Parsed: manifest},
		verdictOutcome("approve"),
		parsedPlan(planText("SPEC-008", "build the app")),
		verdictOutcome("request-changes", agent.Finding{Severity: "major", File: "draft", Issue: "task maps to exclusions", Fix: "use SPEC-001"}),
		parsedPlan(planText("SPEC-001", "install and publish the app")+"\n\n## M-02 — Invented\n\n### T-002 — Extra\n\nUnreviewed topology.\n\n**Implements:** SPEC-001\n\n**Produces:** file:extra\n\n**Consumes:** none\n\n**Verification:** `true`"),
		verdictOutcome("approve"),
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "**Implements:** SPEC-001") || strings.Contains(res.Text, "**Implements:** SPEC-008") {
		t.Fatalf("reviewed semantic correction was restored from the manifest:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "**Work unit:** build the app") || strings.Contains(res.Text, "install and publish") {
		t.Fatalf("review revision changed the frozen task identity:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "M-02") || strings.Contains(res.Text, "T-002") || strings.Contains(res.Text, "file:extra") {
		t.Fatalf("review revision changed frozen topology:\n%s", res.Text)
	}
}

// A council with three ticked boxes seats three ducklings. For as long as the
// council had exactly two chairs, the third box saved fine and did nothing —
// a person ticked k3, sonnet and luna, and luna watched from the gallery.
func TestCouncilSeatsOneCritiqueTurnPerCritic(t *testing.T) {
	script := CouncilScript("REQ", []config.DucklingID{"pato-local", "pato-luna"})
	var critics []config.DucklingID
	for _, turn := range script.Turns {
		if turn.Role == config.RoleReviewer {
			critics = append(critics, turn.Duckling)
		}
	}
	if len(critics) != 2 || critics[0] != "pato-local" || critics[1] != "pato-luna" {
		t.Fatalf("critique turns pinned to %v, want [pato-local pato-luna]", critics)
	}
	if err := script.Validate(testRegistry(t)); err != nil {
		t.Fatalf("multi-critic council does not validate: %v", err)
	}
	// The line-up order runs in order: drafter first, then each critic. A
	// unanimous finding-free approval needs no revision.
	rec := &recorder{}
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("approve"),
		verdictOutcome("approve"),
		&agent.Outcome{Text: "## REQ-001 — Final that must not run\n"},
	)
	if _, err := ExecuteScript(context.Background(), script, params); err != nil {
		t.Fatal(err)
	}
	want := []config.DucklingID{"pato-atom", "pato-local", "pato-luna"}
	if len(rec.ducklings) != len(want) {
		t.Fatalf("%d turns ran with %v, want %v", len(rec.ducklings), rec.ducklings, want)
	}
	for i, d := range want {
		if rec.ducklings[i] != d {
			t.Errorf("turn %d ran on %s, want %s", i, rec.ducklings[i], d)
		}
	}
}

// Each critic reads the draft, not the other critics. A critic shown a fellow
// critic's findings anchors on them, and N critics become one critique read N
// times — which is the decorrelation the extra seats exist for, undone.
func TestCouncilCriticsDoNotSeeEachOther(t *testing.T) {
	rec := &recorder{}
	script := CouncilScript("REQ", []config.DucklingID{"pato-local", "pato-luna"})
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n\nBody of the draft."},
		verdictOutcome("request-changes", agent.Finding{
			Severity: "major", File: "requirements.md",
			Issue: "the scope line is missing", Fix: "add one",
		}),
		verdictOutcome("approve"),
		&agent.Outcome{Text: "## REQ-001 — Revised\n"},
	)
	if _, err := ExecuteScript(context.Background(), script, params); err != nil {
		t.Fatal(err)
	}
	second := rec.prompts[2]
	if strings.Contains(second, "the scope line is missing") {
		t.Error("the second critic was shown the first critic's findings")
	}
	if !strings.Contains(second, "Body of the draft.") {
		t.Errorf("the second critic was not shown the draft:\n%s", second)
	}
	// The revision, by contrast, must see every critique — that is its input.
	revision := rec.prompts[3]
	if !strings.Contains(revision, "the scope line is missing") {
		t.Errorf("the revision could not see the first critic's findings:\n%s", revision)
	}
}

// The round's verdict is the WORST across its critics. Folding by overwrite
// meant the last critic to speak decided for everyone: request-changes then
// approve settled the round as approved, and the objection evaporated.
func TestCouncilOneRequestChangesOutvotesTheApprovals(t *testing.T) {
	rec := &recorder{}
	script := CouncilScript("REQ", []config.DucklingID{"pato-local", "pato-luna"})
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("request-changes", agent.Finding{
			Severity: "major", File: "requirements.md", Issue: "no scope", Fix: "add one",
		}),
		verdictOutcome("approve"), // the LAST critic approves
		&agent.Outcome{Text: "## REQ-001 — Revised\n"},
	)
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 4 {
		t.Errorf("ran %d rounds — unresolved request-changes must run to the bounded cap", res.Rounds)
	}
}

// And the converse: unanimous approval settles the round.
func TestCouncilUnanimousApprovalSettlesTheRound(t *testing.T) {
	rec := &recorder{}
	script := CouncilScript("REQ", []config.DucklingID{"pato-local", "pato-luna"})
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("approve"),
		verdictOutcome("approve"),
		&agent.Outcome{Text: "## REQ-001 — Final\n"},
	)
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1 {
		t.Errorf("ran %d rounds on a unanimous approval", res.Rounds)
	}
}

// No critics is the original council: one unpinned reviewer, the roster's own.
func TestCouncilWithNoCriticsKeepsTheOriginalShape(t *testing.T) {
	script := CouncilScript("REQ", nil)
	var reviewers int
	for _, turn := range script.Turns {
		if turn.Role == config.RoleReviewer {
			reviewers++
			if turn.Duckling != "" {
				t.Errorf("the fallback reviewer is pinned to %q; it must come from the roster", turn.Duckling)
			}
		}
	}
	if reviewers != 1 {
		t.Errorf("%d reviewer turns, want 1", reviewers)
	}
}

// The code-review framing sent a real critic hunting for a diff that by design
// does not exist: it called git_diff (empty — a proposal never touches the
// tree), artifact_read (the OLD approved document) and fs_read (no such file),
// and its tools truthfully corroborated "there is no draft anywhere". Three of
// its six turns went to archaeology. The critique turn now presents the draft
// under its own heading with the mechanism spelled out.
func TestACriticIsToldTheDraftLivesInTheConversation(t *testing.T) {
	rec := &recorder{}
	script := CouncilScript("REQ", []config.DucklingID{"pato-local"})
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-016 — Zoom\n\nScroll to zoom."},
		verdictOutcome("approve"),
		&agent.Outcome{Text: "## REQ-016 — Zoom\n"},
	)
	if _, err := ExecuteScript(context.Background(), script, params); err != nil {
		t.Fatal(err)
	}
	critique := rec.prompts[1]
	if !strings.Contains(critique, "The draft under review") {
		t.Errorf("the critique prompt never names the draft as the thing under review:\n%s", critique)
	}
	if !strings.Contains(critique, "do not go looking for it with tools") {
		t.Errorf("the critique prompt does not warn off the tool hunt:\n%s", critique)
	}
	if !strings.Contains(critique, "Scroll to zoom.") {
		t.Errorf("the draft itself is missing:\n%s", critique)
	}
}

// Every critique turn of a council carries the critic persona; a task-mode
// reviewer (pair) keeps the code framing, because there a diff IS the thing
// under review.
func TestOnlyCouncilCritiquesCarryTheCriticPersona(t *testing.T) {
	for _, turn := range CouncilScript("REQ", []config.DucklingID{"a", "b"}).Turns {
		if turn.Role == config.RoleReviewer && turn.Persona != PersonaCritic {
			t.Errorf("council critique turn without the critic persona: %+v", turn)
		}
		if turn.Role == config.RoleReviewer {
			belt, err := turn.ResolveToolbelt(testRegistry(t))
			if err != nil {
				t.Fatal(err)
			}
			if len(belt) != 0 {
				t.Errorf("closed document critic received workspace tools: %v", belt)
			}
		}
	}
	for _, turn := range PairScript().Turns {
		if turn.Persona != "" {
			t.Errorf("pair turn carries persona %q", turn.Persona)
		}
	}
}
