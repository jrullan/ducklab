package stage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
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
