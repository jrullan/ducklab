package strategy

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// TI-36X T-008 r-20261005-012549-uvns: the slice the visual gate measured,
// as the plan wrote it, and the project's comparison.
const t008Slice = "At a 730 × 1500 viewport the calculator renders matching REF-IMG-6c63e390's layout: enclosure, d-pad and key grid in physical positions."

func t008Check(measured *VisualMeasurement, calls *[]string) *VisualCheck {
	compares := []VisualCompare{{Capture: "calculator.png", Reference: "REF-IMG-6c63e390", Tolerance: 0.02, Threshold: 0.1}}
	return &VisualCheck{
		Slices:   VisualSlices([]string{t008Slice, "Scales with the viewport."}, compares),
		Compares: compares,
		Measure: func(_ context.Context, _ int, phase string) *VisualMeasurement {
			if calls != nil {
				*calls = append(*calls, phase)
			}
			return measured
		},
	}
}

func failing445() *VisualMeasurement {
	return &VisualMeasurement{Round: 1, Phase: "before implementer turn", Current: true, Results: []VisualResult{{
		Capture: "calculator.png", Reference: "REF-IMG-6c63e390", Mismatch: 0.445, Tolerance: 0.02,
	}}}
}

func TestVisualSlicesAreTheSlicesCitingACoveredReference(t *testing.T) {
	compares := []VisualCompare{{Capture: "a.png", Reference: "REF-IMG-6C63E390"}}
	got := VisualSlices([]string{
		t008Slice,
		"Matches REF-IMG-aaaaaaaa's colours.", // cited, but no comparison covers it
		"Scales with the viewport.",
		"Both REF-IMG-6c63e390 and REF-IMG-6c63e390 again.",
	}, compares)
	if len(got) != 2 || len(got[1]) != 1 || got[1][0] != "REF-IMG-6c63e390" || len(got[4]) != 1 {
		t.Fatalf("VisualSlices = %v, want slices 1 and 4, each citing the compared reference once", got)
	}
}

// The incident's three reports, each "partial" on the measured slice only.
// Before: Undelivered [1] → distress → advisor, and the third report fired
// stuck_deliverable. Now neither; the implementer turn is measured each time.
func TestAPartialOnTheMeasuredSliceIsNeitherDistressNorStuck(t *testing.T) {
	run := func(visual *VisualCheck) []string {
		rec := &recorder{}
		var kinds []string
		report := &agent.Outcome{Text: `{"deliverables":[{"id":1,"status":"partial","note":"pixel similarity was not verified"},{"id":2,"status":"done"}]}`}
		params := &ExecuteParams{
			Prompt:       "Task T-008",
			Deliverables: []string{t008Slice, "Scales with the viewport."},
			Rounds:       3,
			Runner:       rec.runner(report, report, report),
			Gate:         func(context.Context) (string, string, error) { return "red", "", nil },
			Roster:       map[config.Role]config.DucklingID{config.RoleImplementer: "luna"},
			Visual:       visual,
			OnEvent:      func(kind string, _ map[string]interface{}) { kinds = append(kinds, kind) },
		}
		script := &Script{
			Name: "t008-fixture", MaxRounds: 3, Until: "round == 99",
			Turns: []Turn{{Role: config.RoleImplementer, Toolbelt: "full", MaxTurns: 4}},
		}
		if _, err := ExecuteScript(context.Background(), script, params); err != nil {
			t.Fatal(err)
		}
		return kinds
	}
	// The same reports with no visual check: the honesty mechanism, intact.
	without := strings.Join(run(nil), ",")
	if !strings.Contains(without, "advisor_consult") || !strings.Contains(without, "escalation_suggestion") {
		t.Fatalf("without a visual check the partial must still be distress and stuck: %s", without)
	}
	var phases []string
	with := strings.Join(run(t008Check(failing445(), &phases)), ",")
	if strings.Contains(with, "advisor_consult") || strings.Contains(with, "escalation_suggestion") {
		t.Errorf("a partial on the measured slice summoned the duck or escalated: %s", with)
	}
	if len(phases) != 3 || phases[0] != "before implementer turn" {
		t.Errorf("measurements = %v, want one before each implementer turn", phases)
	}
}

// B-505: every path that starts an implementer turn measures first — the
// round's turn, the report retry (#146), the advisor retry, the resumed turn
// (#155) — and so do the advisor and the reviewer. The measurement shows in
// each implementer prompt.
func TestEveryImplementerTurnPathIsMeasuredFirst(t *testing.T) {
	var phases []string
	rec := &recorder{}
	outcomes := []*agent.Outcome{
		{Text: "Working on it."}, // no report: report retry
		{Text: `{"deliverables":[{"id":1,"status":"done"},{"id":2,"status":"partial"}]}`}, // distress: advisor
		{Text: `{"action":"note","note":"Finish slice 2."}`, Parsed: map[string]interface{}{"action": "note", "note": "Finish slice 2."}},
		{Text: `{"deliverables":[{"id":1,"status":"partial"},{"id":2,"status":"done"}]}`}, // advisor retry
		verdictOutcome("approve"),
	}
	params := pairParams(rec, "green", outcomes...)
	params.Deliverables = []string{t008Slice, "Scales with the viewport."}
	params.Roster[config.RoleAdvisor] = "glm53flash"
	params.Visual = t008Check(failing445(), &phases)
	if _, err := ExecuteScript(context.Background(), PairScript(), params); err != nil {
		t.Fatal(err)
	}
	want := "before implementer turn,before implementer turn,before advisor consult,before implementer turn,before review"
	if got := strings.Join(phases, ","); got != want {
		t.Errorf("measurements = %s\nwant %s", got, want)
	}
	for i, role := range rec.roles {
		if role == config.RoleImplementer && !strings.Contains(rec.prompts[i], "Latest measurement (rendered before implementer turn, round 1): calculator.png against REF-IMG-6c63e390: 44.5%") {
			t.Errorf("implementer prompt %d lacks the figure:\n%s", i, rec.prompts[i])
		}
	}

	// The resumed implementer turn of a paused run.
	phases, rec = nil, &recorder{}
	params = pairParams(rec, "green", &agent.Outcome{Text: `{"deliverables":[{"id":1,"status":"done"},{"id":2,"status":"done"}]}`}, verdictOutcome("approve"))
	params.Deliverables = []string{t008Slice, "Scales with the viewport."}
	params.Visual = t008Check(failing445(), &phases)
	params.ResumeFrom = &ResumeTurn{Round: 1, Index: 0, Role: config.RoleImplementer, Notes: "half done"}
	if _, err := ExecuteScript(context.Background(), PairScript(), params); err != nil {
		t.Fatal(err)
	}
	if len(phases) == 0 || phases[0] != "before implementer turn" || !strings.Contains(rec.prompts[0], "Resume checkpoint") ||
		!strings.Contains(rec.prompts[0], "44.5% of pixels differ") {
		t.Errorf("the resumed turn was not measured first: %v\n%s", phases, rec.prompts[0])
	}
}

// A partial on any other slice is distress exactly as before, with a visual
// check in the run; its signals name only that slice.
func TestAPartialOnAnotherSliceIsStillDistress(t *testing.T) {
	rep := ParseDeliverablesReport(`{"deliverables":[{"id":1,"status":"partial"},{"id":2,"status":"partial"}]}`, 2)
	check := t008Check(failing445(), nil)
	signals := measureDistressWithReport(nil, reportWithoutItems(rep, 2, check.ids(), harnessMeasuredNote))
	if !signals.Distressed() || len(signals.Undelivered) != 1 || signals.Undelivered[0] != 2 {
		t.Fatalf("signals = %+v, want distress on slice 2 only", signals)
	}
	if missing := reportWithoutItems(rep, 2, check.ids(), harnessMeasuredNote).Missing(2); len(missing) != 0 {
		t.Errorf("an unmentioned measured slice reads missing: %v", missing)
	}
	silent := ParseDeliverablesReport(`{"deliverables":[{"id":2,"status":"done"}]}`, 2)
	if gap := incompleteDeliverables(reportWithoutItems(silent, 2, check.ids(), harnessMeasuredNote), 2); len(gap) != 0 {
		t.Errorf("a report silent on the measured slice left a gap: %v", gap)
	}
}

// The duck is told the contract and the figure, and nothing false.
func TestTheAdvisorIsToldTheComparisonContractAndTheFigure(t *testing.T) {
	params := &ExecuteParams{Prompt: "Task T-008", Deliverables: []string{t008Slice, "Scales with the viewport."}, Visual: t008Check(failing445(), nil)}
	outcome := &agent.Outcome{Text: `{"deliverables":[{"id":1,"status":"partial"},{"id":2,"status":"partial"}]}`}
	prompt := rubberDuckPrompt(params, "luna", outcome, distressSignals{Undelivered: []int{2}}, failing445())
	for _, want := range []string{
		"capture `calculator.png` against REF-IMG-6c63e390: at most 2.0% of its pixels may differ (a pixel counts as different past 10% of the largest colour distance)",
		"verify_run does NOT run it",
		"oracle_dispute is for oracle tests",
		"It is diagnostic",
		"Latest measurement (rendered before implementer turn, round 1): calculator.png against REF-IMG-6c63e390: 44.5% of pixels differ (allowed 2.0%).",
		"Slice 1 is measured by this comparison",
		"Never advise a change whose purpose is to move the percentage",
		"The implementer itself reports [2] undelivered",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("advisor prompt lacks %q:\n%s", want, prompt)
		}
	}
	params.Visual.Required = true
	if prompt := rubberDuckPrompt(params, "luna", outcome, distressSignals{}, nil); !strings.Contains(prompt, "It is required") ||
		!strings.Contains(prompt, "Latest measurement: none yet in this run.") {
		t.Errorf("required/unmeasured contract not stated:\n%s", prompt)
	}
	// No visual check: the duck's prompt is as it was.
	params.Visual = nil
	if prompt := rubberDuckPrompt(params, "luna", outcome, distressSignals{}, nil); strings.Contains(prompt, "visual comparison") ||
		!strings.Contains(prompt, "reports [1 2] undelivered") {
		t.Errorf("a run without a visual check changed the duck's prompt:\n%s", prompt)
	}
}

// A measurement the tree has moved past decides nothing: the required check
// does not block on it, and the reviewer is told it was not measured.
func TestAnOutdatedMeasurementDecidesNothing(t *testing.T) {
	check := t008Check(nil, nil)
	check.Required = true
	stale := failing445()
	stale.Current = false
	if gap := check.visualGap(stale); len(gap) != 0 {
		t.Errorf("an outdated figure blocked: %v", gap)
	}
	if gap := check.visualGap(failing445()); len(gap[1]) != 1 {
		t.Errorf("a current required mismatch did not block: %v", gap)
	}
	if s := check.forReviewer(stale); !strings.Contains(s, "slice 1: not measured on the current tree") {
		t.Errorf("reviewer section:\n%s", s)
	}
	errored := failing445()
	errored.Results[0].Error = "the capture command produced no calculator.png"
	if state, _ := check.sliceState(1, errored); state != visualFailed {
		t.Errorf("a comparison that could not run = %s, want failed", state)
	}
	passed := failing445()
	passed.Results[0].Passed, passed.Results[0].Mismatch = true, 0.01
	if state, _ := check.sliceState(1, passed); state != visualPassed {
		t.Errorf("a passing comparison = %s", state)
	}
	if state, _ := check.sliceState(2, passed); state != visualUnmeasured {
		t.Errorf("an unmeasured slice = %s", state)
	}
}
