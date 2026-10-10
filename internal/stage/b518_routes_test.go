package stage

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/strategy"
)

// B-518 is a class, not one route: every path that amends an approved
// document — fragment (intake/spec/plan), section-wise, plan extend/split and
// the whole-document re-survey — must (1) hand its critics the approved
// before-image, (2) run the deterministic removal check where the request is
// the authority, and (3) spend unspent rounds repairing a blocked
// composition instead of ending FAILED.

type b518Route struct {
	name string
	plan bool
	// removals says whether the deterministic check applies on this route.
	removals bool
	// addOnly marks a route whose reply can only add (plan extend): its delta
	// has no before-image and nothing can be removed.
	addOnly bool
	run     func(t *testing.T, root string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) *Result
}

const b518RouteRequest = "Make the transport an in-process API."

func b518Routes() []b518Route {
	reqBase := "## REQ-001 — Transport\n\nThe service uses the approved socket transport.\n\nEvery request is logged with its caller identity.\n\n**Priority:** must\n"
	planBase := planWithTransport("The service uses the approved socket transport.\n\nEvery request is logged with its caller identity.")
	return []b518Route{
		{name: "fragment-intake", removals: true, run: func(t *testing.T, root string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) *Result {
			writeDoc(t, root, artifact.KindRequirements, reqBase)
			res, err := Run(context.Background(), Params{ProjectRoot: root, Stage: Intake, RunID: "r-f", Seed: b518RouteRequest, Execute: execute, OnEvent: event})
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
		{name: "fragment-spec", removals: true, run: func(t *testing.T, root string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) *Result {
			writeDoc(t, root, artifact.KindRequirements, reqBase)
			writeDoc(t, root, artifact.KindSpec, strings.Replace(reqBase, "REQ-001", "SPEC-001", 1))
			res, err := Run(context.Background(), Params{ProjectRoot: root, Stage: Spec, RunID: "r-s", Seed: b518RouteRequest, Execute: execute, OnEvent: event})
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
		{name: "fragment-plan", plan: true, removals: true, run: func(t *testing.T, root string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) *Result {
			writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Transport\n\nContract.\n")
			writeDoc(t, root, artifact.KindPlan, planBase)
			res, err := Run(context.Background(), Params{ProjectRoot: root, Stage: Plan, RunID: "r-p", Seed: b518RouteRequest, Execute: execute, OnEvent: event})
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
		{name: "adopt-resurvey", run: func(t *testing.T, root string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) *Result {
			writeDoc(t, root, artifact.KindRequirements, reqBase)
			res, err := Run(context.Background(), Params{ProjectRoot: root, Stage: Intake, RunID: "r-a", Adopt: true, Seed: b518RouteRequest, Execute: execute, OnEvent: event})
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
		{name: "sectioned", plan: true, removals: true, run: func(t *testing.T, root string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) *Result {
			writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Transport\n\nContract.\n")
			base, err := artifact.Parse(planBase, artifact.KindPlan)
			if err != nil {
				t.Fatal(err)
			}
			res, err := runSectioned(context.Background(), Params{ProjectRoot: root, Stage: Plan, RunID: "r-x", Execute: execute, OnEvent: event}, base, b518RouteRequest)
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
		{name: "extend", plan: true, removals: true, addOnly: true, run: func(t *testing.T, root string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) *Result {
			writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Transport\n\nContract.\n")
			base, err := artifact.Parse(planBase, artifact.KindPlan)
			if err != nil {
				t.Fatal(err)
			}
			res, err := runExtend(context.Background(), Params{ProjectRoot: root, Stage: Plan, RunID: "r-e", Extend: b518RouteRequest, Execute: execute, OnEvent: event}, base)
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
		{name: "split", plan: true, removals: true, run: func(t *testing.T, root string, execute func(context.Context, *strategy.Script, string) (string, error), event func(string, map[string]interface{})) *Result {
			writeDoc(t, root, artifact.KindSpec, "## SPEC-001 — Transport\n\nContract.\n")
			base, err := artifact.Parse(planBase, artifact.KindPlan)
			if err != nil {
				t.Fatal(err)
			}
			res, err := runExtend(context.Background(), Params{ProjectRoot: root, Stage: Plan, RunID: "r-sp", SplitTask: "T-001", Execute: execute, OnEvent: event}, base)
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
	}
}

// b518RouteReply is a candidate that changes the transport sentence and
// drops the unrelated logging sentence.
func b518RouteReply(route b518Route, script *strategy.Script) string {
	const body = "The service uses an in-process API.\n\n**Priority:** must"
	switch {
	case script.Name == "solo" && strings.Contains(route.name, "sectioned") && script.TurnIndexBase == 0:
		return "T-001"
	case route.name == "extend":
		return "## T-900 — In-process transport\n\n**Milestone:** M-001\n**Implements:** SPEC-001\n**Work unit:** In-process transport\n\nThe service uses an in-process API.\n"
	case route.name == "split":
		return "## T-900 — Transport API\n\n**Milestone:** M-001\n**Implements:** SPEC-001\n**Owns:** src/api/\n**Work unit:** Transport API\n\nThe service uses the approved socket transport.\n\n" +
			"## T-901 — Transport adapter\n\n**Milestone:** M-001\n**Implements:** SPEC-001\n**Owns:** src/adapter/\n**Work unit:** Transport adapter\n\nThe adapter wraps the transport.\n"
	case route.plan:
		return "## T-001 — Transport\n\n**Implements:** SPEC-001\n**Work unit:** Transport\n\nThe service uses an in-process API.\n"
	case route.name == "fragment-spec":
		return "## SPEC-001 — Transport\n\n" + body
	case route.name == "adopt-resurvey":
		return "## REQ-001 — Transport\n\n" + body + "\n"
	default:
		return "## REQ-001 — Transport\n\n" + body
	}
}

func TestB518EveryAmendmentRouteGuardsAndRepairs(t *testing.T) {
	for _, route := range b518Routes() {
		t.Run(route.name, func(t *testing.T) {
			var guarded []*strategy.Script
			var drafting []*strategy.Script
			var draftingPrompts []string
			compositions := 0
			var events []string
			execute := func(_ context.Context, script *strategy.Script, prompt string) (string, error) {
				switch script.Name {
				case "survey-inventory":
					return `{"items":[]}`, nil
				case "composition-review":
					compositions++
					if compositions == 1 {
						return `{"verdict":"request-changes","findings":[{"severity":"critical","file":"` +
							map[bool]string{true: "T-001", false: "REQ-001"}[route.plan] +
							`","issue":"COMPOSITION-FINDING-MARKER","fix":"repair it"}]}`, nil
					}
					return `{"verdict":"approve","findings":[]}`, nil
				}
				reply := b518RouteReply(route, script)
				if reply == "T-001" { // section-wise triage
					return reply, nil
				}
				drafting = append(drafting, script)
				draftingPrompts = append(draftingPrompts, prompt)
				if script.Amendment != nil {
					guarded = append(guarded, script)
				}
				script.RoundsUsed = 1 // what ExecuteScript records
				return reply, nil
			}
			event := func(kind string, _ map[string]interface{}) { events = append(events, kind) }
			res := route.run(t, t.TempDir(), execute, event)

			// (1) and (2): every drafting conversation carries the guard.
			if len(guarded) == 0 || len(guarded) != len(drafting) {
				t.Fatalf("guarded %d of %d drafting conversations", len(guarded), len(drafting))
			}
			guard := guarded[0].Amendment
			delta := guard.Delta(b518RouteReply(route, guarded[0]))
			if route.addOnly {
				if !strings.Contains(delta, "added — no previous text") {
					t.Errorf("critic delta does not present the addition:\n%s", delta)
				}
			} else if !strings.Contains(delta, "Approved previous text:") || !strings.Contains(delta, "Every request is logged with its caller identity.") {
				t.Errorf("critic delta lacks the approved before-image:\n%s", delta)
			}
			if (guard.Removals != nil) != route.removals {
				t.Fatalf("removal check present=%v, want %v", guard.Removals != nil, route.removals)
			}
			if route.removals && !route.addOnly {
				report := guard.Removals(b518RouteReply(route, guarded[0]), nil)
				if len(report.Findings) == 0 || !strings.Contains(strings.Join(report.Findings, "\n"), "logged with its caller identity") {
					t.Errorf("the dropped logging sentence was not flagged: %+v", report)
				}
			}

			// (3): one repair within the budget, then a clean composition.
			if compositions != 2 {
				t.Fatalf("composition reviews = %d, want 2 (blocked, then repaired); mechanical=%v", compositions, res.CompositionMechanical)
			}
			repair := draftingPrompts[len(draftingPrompts)-1]
			if !strings.Contains(repair, "## Post-composition review — repair round") || !strings.Contains(repair, "COMPOSITION-FINDING-MARKER") {
				t.Fatalf("the repair conversation did not receive the findings:\n%s", repair)
			}
			if first, last := drafting[0].MaxRounds, drafting[len(drafting)-1].MaxRounds; last != first-1 {
				t.Errorf("repair MaxRounds = %d, want %d (budget %d minus the round spent)", last, first-1, first)
			}
			if res.CompositionReview == nil || res.CompositionReview.Verdict != "approve" {
				t.Fatalf("final composition = %+v", res.CompositionReview)
			}
			if !strings.Contains(strings.Join(events, ","), "composition_repair_started") {
				t.Errorf("no composition_repair_started record: %v", events)
			}
		})
	}
}

// With the budget spent, every route stops at the gate exactly as before.
func TestB518EveryAmendmentRouteStopsAtTheGateWhenRoundsAreSpent(t *testing.T) {
	for _, route := range b518Routes() {
		t.Run(route.name, func(t *testing.T) {
			compositions := 0
			var events []string
			execute := func(_ context.Context, script *strategy.Script, _ string) (string, error) {
				switch script.Name {
				case "survey-inventory":
					return `{"items":[]}`, nil
				case "composition-review":
					compositions++
					return `{"verdict":"request-changes","findings":[{"severity":"critical","file":"T-001","issue":"still blocked","fix":"x"}]}`, nil
				}
				reply := b518RouteReply(route, script)
				if reply != "T-001" {
					script.RoundsUsed = script.MaxRounds
				}
				return reply, nil
			}
			res := route.run(t, t.TempDir(), execute, func(kind string, _ map[string]interface{}) { events = append(events, kind) })
			if compositions != 1 || res.CompositionReview == nil || res.CompositionReview.Verdict != "request-changes" {
				t.Fatalf("compositions=%d verdict=%+v, want one blocked review at the gate", compositions, res.CompositionReview)
			}
			joined := strings.Join(events, ",")
			if strings.Contains(joined, "composition_repair_started") || !strings.Contains(joined, "composition_repair_exhausted") {
				t.Fatalf("events = %v, want an exhausted record and no repair", events)
			}
		})
	}
}

// A deterministic finding is worth a model round only when it names a section
// the architect wrote; an environment fact is not, and must not burn budget.
func TestB518MechanicalOnlyBlocksRepairOnlyWhenTheyNameASection(t *testing.T) {
	doc, err := artifact.Parse("## REQ-001 — A\n\nBody.\n", artifact.KindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	p := Params{OnEvent: func(kind string, _ map[string]interface{}) { events = append(events, kind) }}
	budget := &roundBudget{max: 4, left: 3, known: true}
	if _, repair := budget.decide(p, doc, []string{"the approved specification could not be loaded"}, nil); repair {
		t.Fatal("an environment fact bought a repair round")
	}
	if strings.Join(events, ",") != "composition_repair_skipped" {
		t.Fatalf("events = %v, want one composition_repair_skipped", events)
	}
	if left, repair := budget.decide(p, doc, []string{"REQ-001: field Priority is missing"}, nil); !repair || left != 3 {
		t.Fatalf("a section-named contract finding did not buy a repair: left=%d repair=%v", left, repair)
	}
	if _, repair := budget.decide(p, doc, nil, &agent.Verdict{Verdict: "approve"}); repair {
		t.Fatal("an approved composition bought a repair")
	}
}
