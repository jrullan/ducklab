package stage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/strategy"
)

// Each amendment route must send the fully composed artifact to a semantic
// reviewer. The reviewer stub makes the expected semantic verdict explicit;
// these assertions define the evidence and audit record the production hook
// must preserve.
func TestAmendmentRoutesRecordPostCompositionSemanticReview(t *testing.T) {
	const request = "Replace the approved socket transport with an in-process API. The stdin/stdout wire protocol is explicitly out of scope."
	const oldTransport = "The service uses the approved socket transport.\n\n**Priority:** must"
	const cleanReplacement = "The service uses an in-process API.\n\n**Priority:** must"
	const reintroducedTransport = "The service uses an in-process API, but also retains the approved socket transport.\n\n**Priority:** must"
	const excludedProtocol = "The service uses an in-process API and adds a stdin/stdout wire protocol.\n\n**Priority:** must"

	type route struct {
		name string
		plan bool
		run  func(t *testing.T, root, body string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) (*Result, *artifact.Document)
	}
	routes := []route{
		{name: "main", run: func(t *testing.T, root, _ string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) (*Result, *artifact.Document) {
			writeDoc(t, root, artifact.KindRequirements, "## REQ-001 — Transport\n\n"+oldTransport+"\n")
			base, err := artifact.Load(root, artifact.KindRequirements)
			if err != nil {
				t.Fatal(err)
			}
			res, err := Run(context.Background(), Params{ProjectRoot: root, Stage: Intake, RunID: "r-main", Seed: request, Execute: execute, OnEvent: event})
			if err != nil {
				t.Fatal(err)
			}
			return res, base
		}},
		{name: "fragment", run: func(t *testing.T, root, _ string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) (*Result, *artifact.Document) {
			base, err := artifact.Parse("## REQ-001 — Transport\n\n"+oldTransport+"\n", artifact.KindRequirements)
			if err != nil {
				t.Fatal(err)
			}
			res, err := runFragment(context.Background(), Params{ProjectRoot: root, Stage: Intake, RunID: "r-fragment", Execute: execute, OnEvent: event}, base, request)
			if err != nil {
				t.Fatal(err)
			}
			return res, base
		}},
		{name: "adopt", run: func(t *testing.T, root, _ string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) (*Result, *artifact.Document) {
			writeDoc(t, root, artifact.KindRequirements, "## REQ-001 — Transport\n\n"+oldTransport+"\n")
			base, err := artifact.Load(root, artifact.KindRequirements)
			if err != nil {
				t.Fatal(err)
			}
			res, err := Run(context.Background(), Params{ProjectRoot: root, Stage: Intake, RunID: "r-adopt", Adopt: true, Seed: request, Execute: execute, OnEvent: event})
			if err != nil {
				t.Fatal(err)
			}
			return res, base
		}},
		{name: "sectioned", plan: true, run: func(t *testing.T, root, body string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) (*Result, *artifact.Document) {
			writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Transport\n\nContract.\n")
			base, err := artifact.Parse(planWithTransport("The service uses the approved socket transport."), artifact.KindPlan)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			sectionExecute := func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
				if script.Name == "composition-review" {
					return execute(ctx, script, prompt)
				}
				calls++
				if calls == 1 {
					return "T-001", nil
				}
				return "## T-001 — Transport\n\n**Implements:** SPEC-001\n**Work unit:** Transport\n\n" + body, nil
			}
			res, err := runSectioned(context.Background(), Params{ProjectRoot: root, Stage: Plan, RunID: "r-sectioned", Execute: sectionExecute, OnEvent: event}, base, request)
			if err != nil {
				t.Fatal(err)
			}
			return res, base
		}},
		{name: "extend", plan: true, run: func(t *testing.T, root, _ string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) (*Result, *artifact.Document) {
			writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Transport\n\nContract.\n")
			base, err := artifact.Parse(planWithTransport("The service uses the approved socket transport."), artifact.KindPlan)
			if err != nil {
				t.Fatal(err)
			}
			res, err := runExtend(context.Background(), Params{ProjectRoot: root, Stage: Plan, RunID: "r-extend", Extend: request, Execute: execute, OnEvent: event}, base)
			if err != nil {
				t.Fatal(err)
			}
			return res, base
		}},
	}
	cases := []struct{ name, body, issue, verdict string }{
		{name: "approve clean replacement", body: cleanReplacement, verdict: "approve"},
		{name: "reject reintroduced replacement", body: reintroducedTransport, issue: "reintroduces the replaced socket transport", verdict: "request-changes"},
		{name: "reject excluded protocol", body: excludedProtocol, issue: "invents the excluded stdin/stdout wire protocol", verdict: "request-changes"},
	}
	for _, route := range routes {
		for _, tc := range cases {
			t.Run(route.name+"/"+tc.name, func(t *testing.T) {
				var prompt string
				var mechanical, started, completed map[string]interface{}
				execute := func(_ context.Context, script *strategy.Script, got string) (string, error) {
					if script.Name == "survey-inventory" {
						return `{"items":[]}`, nil
					}
					if script.Name == "composition-review" {
						prompt = got
						if tc.verdict == "approve" {
							return `{"verdict":"approve","findings":[]}`, nil
						}
						file := "REQ-001"
						if route.plan {
							file = "T-001"
						}
						return fmt.Sprintf(`{"verdict":"request-changes","findings":[{"severity":"critical","file":"%s","line":0,"issue":"%s","fix":"remove the excluded design","invariant":"the final candidate must preserve the requested replacement and scope"}]}`, file, tc.issue), nil
					}
					return amendmentCandidate(route.plan, tc.body), nil
				}
				event := func(kind string, data map[string]interface{}) {
					switch kind {
					case "composition_mechanical_check":
						mechanical = data
					case "composition_review_started":
						started = data
					case "composition_review_completed":
						completed = data
					}
				}
				res, base := route.run(t, t.TempDir(), tc.body, execute, event)
				if res.CompositionReview == nil || res.CompositionReview.Verdict != tc.verdict {
					t.Fatalf("semantic verdict = %+v, want %s", res.CompositionReview, tc.verdict)
				}
				if tc.verdict == "approve" && len(res.CompositionReview.Findings) != 0 {
					t.Fatalf("clean candidate has semantic findings: %+v", res.CompositionReview.Findings)
				}
				if tc.verdict == "request-changes" && (len(res.CompositionReview.Findings) != 1 || res.CompositionReview.Findings[0].Issue != tc.issue) {
					t.Fatalf("semantic finding = %+v, want %q", res.CompositionReview.Findings, tc.issue)
				}
				baseBody, candidateBody := artifact.RenderBody(base), artifact.RenderBody(res.Proposed)
				for _, want := range []string{"Base artifact", baseBody, "Requested delta", request, "Final candidate", candidateBody} {
					if !strings.Contains(prompt, want) {
						t.Errorf("semantic prompt lacks %q:\n%s", want, prompt)
					}
				}
				if mechanical == nil {
					t.Error("no deterministic composition_mechanical_check event")
				}
				if _, ok := mechanical["findings"]; !ok {
					t.Errorf("mechanical event has no explicit findings payload: %#v", mechanical)
				}
				if started == nil {
					t.Fatal("no post-composition semantic start event")
				}
				if completed == nil {
					t.Fatal("no post-composition semantic completion event")
				}
				if completed["category"] != "semantic" || completed["verdict"] != tc.verdict {
					t.Errorf("semantic completion = %#v", completed)
				}
				if got, want := completed["base_digest"], sha256Digest(baseBody); got != want {
					t.Errorf("base_digest = %#v, want SHA-256 of exact base", got)
				}
				if got, want := completed["candidate_digest"], sha256Digest(candidateBody); got != want {
					t.Errorf("candidate_digest = %#v, want SHA-256 of exact candidate", got)
				}
				for _, key := range []string{"base_digest", "delta_digest", "candidate_digest"} {
					if started[key] != completed[key] {
						t.Errorf("%s changed between start and completion: start=%#v complete=%#v", key, started[key], completed[key])
					}
				}
				if tc.verdict == "request-changes" {
					if findings, ok := completed["findings"].([]agent.Finding); !ok || len(findings) != 1 || findings[0].Issue != tc.issue {
						t.Errorf("semantic completion findings = %#v", completed["findings"])
					}
				}
				if _, hasCategory := mechanical["category"]; hasCategory {
					t.Errorf("mechanical event must not be labeled semantic: %#v", mechanical)
				}
			})
		}
	}
}

func TestCompositionReviewBoundsNormativeReferences(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec,
		"## SPEC-001 — Relevant contract\n\nRELEVANT-MARKER\n\n"+
			"## SPEC-999 — Unrelated contract\n\nUNRELATED-MARKER\n")
	base, err := artifact.Parse(planWithTransport("Old behavior."), artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	proposed, err := artifact.Parse(planWithTransport("New behavior."), artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	prompt := buildArtifactCompositionReviewPrompt(root, artifact.KindPlan, "amend transport", base, proposed)
	if !strings.Contains(prompt, "RELEVANT-MARKER") {
		t.Fatalf("review omitted the candidate's referenced normative section:\n%s", prompt)
	}
	if strings.Contains(prompt, "UNRELATED-MARKER") {
		t.Fatalf("review leaked an unrelated normative section:\n%s", prompt)
	}
}

// B-373: CheckPlan reported zero findings for invalid task-field grammar, so
// the engine spent a semantic reviewer on a candidate artifact_lint already
// knew could not be accepted.
func TestCompositionReviewRunsArtifactContractBeforeSemanticReviewer(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Build\n\nContract.\n")
	valid := "---\nkind: plan\ngrammar: 2\nversion: 1\n---\n\n" +
		"## M-01 — Core\n\n### T-001 — Build\n\n" +
		"**Implements:** SPEC-001\n**Work unit:** build the app\n" +
		"**Acceptance slices:**\n- the app builds\n" +
		"**Acceptance probes:**\n1. `go test ./...`\n" +
		"**Produces:** file:src/main.go\n**Consumes:** none\n" +
		"**Verification:** `go test ./...`\n**Exercises:** file:src/main.go\n"
	base, err := artifact.Parse(valid, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	proposed, err := artifact.Parse(strings.Replace(valid, "**Verification:** `go test ./...`", "**Verification:** go test ./...", 1), artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	mechanical, semantic, err := reviewComposition(context.Background(), Params{
		ProjectRoot: root,
		Execute: func(context.Context, *strategy.Script, string) (string, error) {
			calls++
			return `{"verdict":"approve","findings":[]}`, nil
		},
	}, artifact.KindPlan, "change the build task", base, proposed)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || semantic != nil {
		t.Fatalf("invalid contract reached semantic reviewer: calls=%d verdict=%+v", calls, semantic)
	}
	if got := strings.Join(mechanical, "\n"); !strings.Contains(got, "T-001 **Verification:** must be one backtick command") {
		t.Fatalf("composition omitted artifact contract finding: %v", mechanical)
	}
}

// B-373: enforcing grammar 2 over the whole composed candidate must not make
// an amendment migrate every historical task before it can be reviewed.
func TestCompositionReviewAllowsLegacyPlanFieldsToReachSemanticReviewer(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Build\n\nContract.\n\n## SPEC-002 — Test\n\nContract.\n")
	legacy := "---\nkind: plan\nversion: 1\n---\n\n" +
		"## M-01 — Core\n\n### T-001 — Build\n\n" +
		"**Implements:** SPEC-001\n**Component:** engine\n**Work unit:** build the app\n"
	base, err := artifact.Parse(legacy, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	addition := "\n### T-002 — Test\n\n" +
		"**Implements:** SPEC-002\n**Work unit:** test the app\n" +
		"**Acceptance slices:**\n- the app is tested\n" +
		"**Acceptance probes:**\n1. `go test ./...`\n" +
		"**Produces:** file:src/main_test.go\n**Consumes:** file:src/main.go\n" +
		"**Verification:** `go test ./...`\n**Exercises:** file:src/main_test.go\n"
	proposed, err := artifact.Parse(legacy+addition, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	mechanical, semantic, err := reviewComposition(context.Background(), Params{
		ProjectRoot: root,
		Execute: func(context.Context, *strategy.Script, string) (string, error) {
			calls++
			return `{"verdict":"approve","findings":[]}`, nil
		},
	}, artifact.KindPlan, "add one valid task", base, proposed)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || semantic == nil || semantic.Verdict != "approve" {
		t.Fatalf("legacy amendment did not reach semantic reviewer: calls=%d verdict=%+v", calls, semantic)
	}
	for _, finding := range mechanical {
		if strings.Contains(finding, "unknown field") || strings.Contains(finding, "legacy_grammar") {
			t.Fatalf("legacy vocabulary became a mechanical blocker: %v", mechanical)
		}
	}
}

func TestCompositionReviewSubtractsInheritedGrammarTwoContractDebt(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Build\n\nContract.\n\n## SPEC-002 — Test\n\nContract.\n")
	baseRaw := "---\nkind: plan\ngrammar: 2\nversion: 6\n---\n\n" +
		"## M-01 — Core\n\n### T-001 — Legacy build\n\n" +
		"**Implements:** SPEC-001\n**Deliverables:** file:src/main.go\n"
	base, err := artifact.Parse(baseRaw, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	addition := "\n### T-002 — Test\n\n" +
		"**Implements:** SPEC-002\n**Work unit:** test the app\n" +
		"**Acceptance slices:**\n- the app is tested\n" +
		"**Acceptance probes:**\n1. `go test ./...`\n" +
		"**Produces:** file:src/main_test.go\n**Consumes:** file:src/main.go\n" +
		"**Verification:** `go test ./...`\n**Exercises:** file:src/main_test.go\n"
	proposed, err := artifact.Parse(baseRaw+addition, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	mechanical, semantic, err := reviewComposition(context.Background(), Params{
		ProjectRoot: root,
		Execute: func(context.Context, *strategy.Script, string) (string, error) {
			calls++
			return `{"verdict":"approve","findings":[]}`, nil
		},
	}, artifact.KindPlan, "add one valid task", base, proposed)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || semantic == nil || semantic.Verdict != "approve" {
		t.Fatalf("inherited grammar debt blocked review: calls=%d verdict=%+v mechanical=%v", calls, semantic, mechanical)
	}
	for _, finding := range mechanical {
		if strings.Contains(finding, "T-001") {
			t.Fatalf("inherited T-001 contract debt remained a blocker: %v", mechanical)
		}
	}
}

func TestReferenceContractCheckerStillRejectsAnInvalidUnmaterializedBlock(t *testing.T) {
	contract := capability.ReferenceContract{
		SchemaVersion: capability.CapabilityConformanceV1, Operation: "observe_gate",
		RequiredOutputFields: []string{"findings"}, Source: "observe_gate.json",
		Digest: "sha256:0123456789abcdef", AdditionalProperties: false,
	}
	candidate, _ := artifact.Parse("## SPEC-001 — Gate\n\n```ducklab-reference-contracts\n{\"observe_gate\":{\"required\":[\"findings\",\"error\"],\"optional\":[],\"additional_properties\":false}}\n```\n", artifact.KindSpec)
	got := strings.Join(referenceContractFindings(artifact.KindSpec, candidate, []capability.ReferenceContract{contract}), "\n")
	for _, want := range []string{"observe_gate", "observe_gate.json", "sha256:0123456789abcdef", `forbidden field "error"`} {
		if !strings.Contains(got, want) {
			t.Errorf("contract finding lacks %q: %s", want, got)
		}
	}
}

func TestReferenceContractsAreMaterializedBeforeCompositionReview(t *testing.T) {
	contract := capability.ReferenceContract{
		SchemaVersion: capability.CapabilityConformanceV1, Operation: "observe_gate",
		RequiredOutputFields: []string{"findings"}, Source: "observe_gate.json",
		Digest: "sha256:0123456789abcdef", AdditionalProperties: false,
	}
	base, _ := artifact.Parse("## SPEC-001 — Gate\n\nOld contract.\n", artifact.KindSpec)
	candidate, _ := artifact.Parse("## SPEC-001 — Gate\n\nThe output is `findings: [Finding]`.\n", artifact.KindSpec)
	var prompt string
	mechanical, semantic, err := reviewComposition(context.Background(), Params{
		ReferenceContracts: []capability.ReferenceContract{contract},
		Execute: func(_ context.Context, script *strategy.Script, got string) (string, error) {
			if script.Name == "composition-review" {
				prompt = got
			}
			return `{"verdict":"approve","findings":[]}`, nil
		},
	}, artifact.KindSpec, "clarify gate output", base, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if len(mechanical) != 0 || semantic == nil || semantic.Verdict != "approve" {
		t.Fatalf("materialized review = mechanical %v semantic %+v", mechanical, semantic)
	}
	rendered := artifact.RenderBody(candidate)
	for _, want := range []string{"ducklab-reference-contracts:begin", "```ducklab-reference-contracts", `"observe_gate"`, `"findings"`, "observe_gate.json", "sha256:0123456789abcdef"} {
		if !strings.Contains(rendered, want) || !strings.Contains(prompt, want) {
			t.Errorf("materialized candidate/review lacks %q\ncandidate:\n%s\nprompt:\n%s", want, rendered, prompt)
		}
	}
}

func TestReferenceContractMaterializationIsIdempotentAndReplacesStaleMetadata(t *testing.T) {
	doc, _ := artifact.Parse("## SPEC-001 — Gate\n\nKeep this prose.\n", artifact.KindSpec)
	old := []capability.ReferenceContract{{Operation: "observe_gate", RequiredOutputFields: []string{"old"}, Source: "old.json", Digest: "sha256:old"}}
	current := []capability.ReferenceContract{{Operation: "observe_gate", RequiredOutputFields: []string{"findings"}, Source: "new.json", Digest: "sha256:new"}}
	materializeReferenceContracts(artifact.KindSpec, doc, old)
	materializeReferenceContracts(artifact.KindSpec, doc, current)
	materializeReferenceContracts(artifact.KindSpec, doc, current)
	rendered := artifact.RenderBody(doc)
	if strings.Count(rendered, "```ducklab-reference-contracts") != 1 || strings.Count(rendered, "ducklab-reference-contracts:begin") != 1 {
		t.Fatalf("materialization duplicated its machine-owned region:\n%s", rendered)
	}
	for _, stale := range []string{"old.json", "sha256:old", `"old"`} {
		if strings.Contains(rendered, stale) {
			t.Errorf("stale metadata %q survived replacement:\n%s", stale, rendered)
		}
	}
	for _, want := range []string{"Keep this prose.", "new.json", "sha256:new", `"findings"`} {
		if !strings.Contains(rendered, want) {
			t.Errorf("replacement lost %q:\n%s", want, rendered)
		}
	}
}

func TestInitialSpecChecksStructuredReferencesWithoutDuplicateSemanticReview(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindRequirements, "## REQ-001 — Findings\n\n**Priority:** must\n")
	contract := capability.ReferenceContract{
		SchemaVersion: capability.CapabilityConformanceV1, Operation: "inspect_review_findings",
		RequiredOutputFields: []string{"inspections"}, Source: "review.json", Digest: "sha256:abc",
	}
	calls := 0
	res, err := Run(context.Background(), Params{
		ProjectRoot: root, Stage: Spec, RunID: "r-contract", ReferenceContracts: []capability.ReferenceContract{contract},
		Execute: func(_ context.Context, script *strategy.Script, _ string) (string, error) {
			calls++
			if script.Name == "composition-review" {
				t.Fatal("a first draft already reviewed by its council received a duplicate semantic pass")
			}
			return "## SPEC-001 — Findings\n\nModel prose without control metadata.\n", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(res.CompositionMechanical) != 0 {
		t.Fatalf("initial contract result calls=%d findings=%v", calls, res.CompositionMechanical)
	}
	if !strings.Contains(artifact.RenderBody(res.Proposed), "ducklab-reference-contracts:begin") {
		t.Fatalf("initial spec lacks materialized reference contract:\n%s", artifact.RenderBody(res.Proposed))
	}
}

func TestReferenceContractBlockIgnoresTypedNarrativeMentions(t *testing.T) {
	contracts := []capability.ReferenceContract{
		{Operation: "inspect_plan_task", RequiredOutputFields: []string{"error", "inspections"}, Source: "plan.json", Digest: "sha256:plan"},
		{Operation: "observe_gate", RequiredOutputFields: []string{"findings"}, Source: "gate.json", Digest: "sha256:gate"},
	}
	candidate, err := artifact.Parse("## SPEC-001 — Operation outputs\n\n"+
		"- `inspect_plan_task`: `inspections: [Inspection]`, `error: String`\n"+
		"- `observe_gate`: `findings: [Finding]`\n\n"+
		"The narrative mentions `inspect_plan_task` again without creating another declaration.\n\n"+
		"```ducklab-reference-contracts\n"+
		"{\"inspect_plan_task\":{\"required\":[\"error\",\"inspections\"],\"optional\":[],\"additional_properties\":false},\"observe_gate\":{\"required\":[\"findings\"],\"optional\":[],\"additional_properties\":false}}\n"+
		"```\n", artifact.KindSpec)
	if err != nil {
		t.Fatal(err)
	}
	if got := referenceContractFindings(artifact.KindSpec, candidate, contracts); len(got) != 0 {
		t.Fatalf("typed narrative contaminated the authoritative block: %v", got)
	}
}

func TestReferenceContractBlockIsUniqueAndValidJSON(t *testing.T) {
	contract := []capability.ReferenceContract{{
		Operation: "observe_gate", RequiredOutputFields: []string{"findings"},
		Source: "gate.json", Digest: "sha256:gate",
	}}
	cases := []struct {
		name, body, want string
	}{
		{name: "missing", body: "## SPEC-001 — Gate\n\n`observe_gate`: `findings`\n", want: "no ducklab-reference-contracts block"},
		{name: "duplicate", body: "## SPEC-001 — Gate\n\n```ducklab-reference-contracts\n{}\n```\n```ducklab-reference-contracts\n{}\n```\n", want: "has 2 ducklab-reference-contracts blocks"},
		{name: "invalid JSON", body: "## SPEC-001 — Gate\n\n```ducklab-reference-contracts\n{no}\n```\n", want: "invalid ducklab-reference-contracts JSON"},
		{name: "duplicate operation", body: "## SPEC-001 — Gate\n\n```ducklab-reference-contracts\n{\"observe_gate\":{\"required\":[\"findings\"],\"optional\":[],\"additional_properties\":false},\"observe_gate\":{\"required\":[\"findings\"],\"optional\":[],\"additional_properties\":false}}\n```\n", want: `operation "observe_gate" is duplicated`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate, err := artifact.Parse(tc.body, artifact.KindSpec)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(referenceContractFindings(artifact.KindSpec, candidate, contract), "\n")
			if !strings.Contains(got, tc.want) {
				t.Fatalf("finding = %q, want %q", got, tc.want)
			}
		})
	}
}

func planWithTransport(body string) string {
	return "## M-001 — Core\n\n### T-001 — Transport\n\n**Implements:** SPEC-001\n**Work unit:** Transport\n\n" + body + "\n"
}
func amendmentCandidate(plan bool, body string) string {
	if !plan {
		return "## REQ-001 — Transport\n\n" + body + "\n"
	}
	return "## T-900 — Compatibility transport\n\n**Milestone:** M-001\n**Implements:** SPEC-001\n**Work unit:** Compatibility transport\n\n" + body + "\n"
}
func sha256Digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum)
}
