package strategy

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
)

// B-516: TI-36X T-009 r-20261010-011504-4oml. The reviewer glm53flash, shown
// "calculator.png against REF-IMG-6c63e390: 32.4% of pixels differ (allowed
// 30.0%)" with no mode, filed the same invented invariant major three rounds
// running, and the run FAILED on dissent. These are its findings, verbatim
// where it matters.

const (
	t009Ref       = "REF-IMG-6c63e390"
	t009Invariant = "INV-2: the rendered device matches REF-IMG-6c63e390 within the 30% pixel-difference allowance."
)

func t009VisualMajor() agent.Finding {
	return agent.Finding{Severity: "major", File: "index.html", Line: 27,
		Issue:     "The rendered device still differs on 32.4% of pixels against REF-IMG-6c63e390 (allowed 30.0%), concentrated in the doubled/offset TI-36X Pro title.",
		Fix:       "Reconcile title, solar-panel, LCD, per-key grid coordinates and enclosure silhouette with the reference image until whole-device pixel difference is at or below 30%.",
		Invariant: t009Invariant}
}

func t009Minor() agent.Finding {
	return agent.Finding{Severity: "minor", File: "logic.mjs", Line: 1516,
		Issue: "The 'store' action opens a menu whose commit is never handled.", Fix: "Handle menu id 'store' in the commit path."}
}

// A real defect that merely mentions a percentage — even the allowance's.
func percentKeyMajor() agent.Finding {
	return agent.Finding{Severity: "major", File: "logic.mjs", Line: 812,
		Issue: "The % key divides twice: 30% of 50 shows 0.15 instead of 15.", Fix: "Divide by 100 once in the percent action.",
		Invariant: "Each key's activation dispatches exactly the named action."}
}

// Pixel talk with a number that is not one the run showed.
func pixelGapMajor() agent.Finding {
	return agent.Finding{Severity: "major", File: "index.html", Line: 40,
		Issue: "The key gap is 4 pixels and the legends render at 80% of the declared size, so labels clip.", Fix: "Use the declared font size."}
}

func t009Check(required bool) *VisualCheck {
	compares := []VisualCompare{{Capture: "calculator.png", Reference: t009Ref, Tolerance: 0.30, Threshold: 0.1}}
	return &VisualCheck{Compares: compares, Required: required}
}

func failing324() *VisualMeasurement {
	return &VisualMeasurement{Round: 3, Phase: "before review", Current: true, Results: []VisualResult{{
		Capture: "calculator.png", Reference: t009Ref, Mismatch: 0.324, Tolerance: 0.30,
	}}}
}

func TestOwnsVisualCheckIsTheTasksOwnAcceptance(t *testing.T) {
	compares := []VisualCompare{{Capture: "calculator.png", Reference: "REF-IMG-6C63E390"}}
	t009 := []string{
		"Every key in the grid and the d-pad is a `<button>` with an accessible name matching its primary legend.",
		"Each key's activation dispatches exactly the named action.",
	}
	cases := []struct {
		name         string
		deliverables []string
		listed       bool
		ownText      string
		want         bool
	}{
		{"a slice cites the compared reference (T-008)", []string{t008Slice, "Scales."}, true, "", true},
		// T-009: SPEC-002 carries the photo; neither slices nor body do.
		{"the reference arrives only through the spec (T-009)", t009, true, "Interactive controls\nImplements SPEC-002.", false},
		// A body that names the photo outside the slice list does not make
		// the slices cite it: the slices are the acceptance.
		{"listed slices decide over the body", t009, true, "Context: see REF-IMG-6c63e390.", false},
		{"no slice list: the body is the acceptance", []string{"Replica composition"}, false, "Replica composition\nCompose the device as REF-IMG-6c63e390 shows.", true},
		{"no slice list, body silent", []string{"Logic"}, false, "Logic\nAdd numbers.", false},
		{"a cited reference no comparison covers", []string{"Match REF-IMG-aaaaaaaa's colours."}, true, "", false},
	}
	for _, c := range cases {
		if got := OwnsVisualCheck(c.deliverables, c.listed, c.ownText, compares); got != c.want {
			t.Errorf("%s: OwnsVisualCheck = %v, want %v", c.name, got, c.want)
		}
	}
	if got := OwnReferences(nil, false, "REF-IMG-6C63E390 and ref-img-x, REF-IMG-6c63e390"); len(got) != 1 || got[0] != "REF-IMG-6c63e390" {
		t.Errorf("OwnReferences = %v", got)
	}
}

// Every prompt a figure reaches states the mode with the one renderer.
func TestEverySectionShowingTheFigureStatesTheMode(t *testing.T) {
	for _, required := range []bool{false, true} {
		check := t008Check(failing445(), nil)
		check.Required = required
		mode := VisualModeStatement(required)
		m := failing445()
		for name, section := range map[string]string{
			"implementer": check.forImplementer(m),
			"reviewer":    check.forReviewer(m),
			"advisor":     check.forAdvisor(m),
		} {
			if !strings.Contains(section, mode) {
				t.Errorf("required=%v %s section lacks the mode statement:\n%s", required, name, section)
			}
		}
		if !strings.Contains(check.forReviewer(m), VisualFindingRule(required)) {
			t.Errorf("required=%v reviewer section lacks the visual_check rule", required)
		}
	}
	diag := VisualModeStatement(false)
	for _, want := range []string{"NOT an acceptance criterion", "does not justify a major or critical finding by itself", "not an invariant"} {
		if !strings.Contains(diag, want) {
			t.Errorf("diagnostic statement lacks %q: %s", want, diag)
		}
	}
	if req := VisualModeStatement(true); strings.Contains(req, "NOT an acceptance criterion") || !strings.Contains(req, "fails the run") {
		t.Errorf("required statement: %s", req)
	}
	if !strings.Contains(VisualFindingRule(false), `"visual_check": true`) || strings.Contains(VisualFindingRule(true), "observation") {
		t.Errorf("finding rule: %q / %q", VisualFindingRule(false), VisualFindingRule(true))
	}
}

// The guard, cell by cell: mode × finding kind.
func TestTheDiagnosticGuardMatrix(t *testing.T) {
	field := agent.Finding{Severity: "major", File: "*", Invariant: "The device looks like the photo.",
		Issue: "The solar panel and the title block sit too high.", Fix: "Lower them.", VisualCheck: true}
	cases := []struct {
		name         string
		required     bool
		findings     []agent.Finding
		wantVerdict  string
		wantKept     int
		wantObserved []string // bases
	}{
		{"diagnostic, 4oml's invented invariant, no field", false, []agent.Finding{t009VisualMajor(), t009Minor()}, "approve", 1, []string{"figure"}},
		{"diagnostic, marked visual_check, no figure quoted", false, []agent.Finding{field}, "approve", 0, []string{"field"}},
		{"diagnostic, percent-key defect quoting 30%", false, []agent.Finding{percentKeyMajor()}, "request-changes", 1, nil},
		{"diagnostic, pixel talk with another number", false, []agent.Finding{pixelGapMajor()}, "request-changes", 1, nil},
		// Codex on #170: the tolerance's own number and the word "pixels" in a
		// real responsive-layout defect are not the comparison.
		{"diagnostic, layout defect quoting the tolerance's number and pixels", false, []agent.Finding{{Severity: "major", File: "index.html", Line: 40,
			Issue: "At 30% viewport width, the keypad overflows its container by 12 pixels.", Fix: "Let the grid shrink with the frame."}}, "request-changes", 1, nil},
		{"diagnostic, layout defect naming the reference and the tolerance's number", false, []agent.Finding{{Severity: "major", File: "index.html", Line: 40,
			Issue: "The d-pad is 30% smaller than in REF-IMG-6c63e390 and overlaps the mode key.", Fix: "Size it to the reference."}}, "request-changes", 1, nil},
		// A percentage bound to a difference word, plus the bare word "pixels",
		// is still not the comparison: the finding has to name it.
		{"diagnostic, a bound percentage without the comparison named", false, []agent.Finding{{Severity: "major", File: "index.html", Line: 60,
			Issue: "A 30% difference in key height leaves the grid 12 pixels short of the frame.", Fix: "Use one key height."}}, "request-changes", 1, nil},
		{"diagnostic, the csjo wording (capture, of pixels, allowed)", false, []agent.Finding{{Severity: "major", File: "index.html", Line: 22,
			Issue: "Pre-review round-2 capture measures 32.4% of pixels differing against the allowed 30.0%.", Fix: "Match the reference."}}, "approve", 0, []string{"figure"}},
		{"diagnostic, visual major beside a real critical", false, []agent.Finding{t009VisualMajor(), {Severity: "critical", File: "index.html", Line: 122, Issue: "dispatch is never reached", Fix: "import it"}}, "request-changes", 1, []string{"figure"}},
		{"required, 4oml's finding", true, []agent.Finding{t009VisualMajor(), t009Minor()}, "request-changes", 2, nil},
		{"required, marked visual_check", true, []agent.Finding{field}, "request-changes", 1, nil},
	}
	for _, c := range cases {
		v := &agent.Verdict{Verdict: "request-changes", Findings: append([]agent.Finding(nil), c.findings...)}
		observed, original := t009Check(c.required).demoteDiagnostic(v, failing324())
		var bases []string
		for _, o := range observed {
			bases = append(bases, o.Basis)
		}
		if v.Verdict != c.wantVerdict || len(v.Findings) != c.wantKept || strings.Join(bases, ",") != strings.Join(c.wantObserved, ",") {
			t.Errorf("%s: verdict %s, kept %d, observed %v; want %s, %d, %v", c.name, v.Verdict, len(v.Findings), bases, c.wantVerdict, c.wantKept, c.wantObserved)
		}
		if len(observed) > 0 && original != "request-changes" {
			t.Errorf("%s: original verdict = %q", c.name, original)
		}
	}
	// No comparison configured: nothing to guard.
	v := &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{t009VisualMajor()}}
	if observed, _ := (*VisualCheck)(nil).demoteDiagnostic(v, nil); observed != nil || v.Verdict != "request-changes" {
		t.Errorf("a nil check guarded: %v", observed)
	}
	// Precision: "32%" stands for 32.4, "32.0%" does not.
	figures := []float64{32.4}
	quote := func(p string) agent.Finding {
		return agent.Finding{Issue: "the capture differs on " + p + " of pixels", Fix: "fix"}
	}
	if !citesFigure(quote("32%"), figures) || citesFigure(quote("32.0%"), figures) || !citesFigure(quote("32.4 %"), figures) {
		t.Errorf("percentage precision is not honoured")
	}
}

// A figure an earlier measurement of the run showed is still the figure: a
// reviewer quoting round 1's 44.5% in round 3 is caught by the fallback.
func TestTheGuardRemembersEveryFigureTheRunShowed(t *testing.T) {
	check := t009Check(false)
	check.Measure = func(context.Context, int, string) *VisualMeasurement { return failing445() }
	finding := agent.Finding{Severity: "major", File: "*", Invariant: "the device looks like the photo",
		Issue: "the capture still differs from the photo on 44.5% of pixels", Fix: "match the photo"}
	v := &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{finding}}
	if observed, _ := check.demoteDiagnostic(v, nil); len(observed) != 0 {
		t.Fatalf("a figure the run never showed was matched: %v", observed)
	}
	check.measure(context.Background(), 1, "before review")
	if observed, _ := check.demoteDiagnostic(v, nil); len(observed) != 1 || v.Verdict != "approve" {
		t.Errorf("round 1's figure was forgotten: %v, verdict %s", observed, v.Verdict)
	}
}

// Through the real script: 4oml's reviewer files its visual major in every
// round. Under a diagnostic check the guard approves; under a required one
// the same verdicts are dissent.
func TestTheGuardDecidesThePairVerdict(t *testing.T) {
	run := func(required bool) (*ExecuteResult, []string, []map[string]interface{}) {
		rec := &recorder{}
		var kinds []string
		var observed []map[string]interface{}
		impl := &agent.Outcome{Text: "Wired the keys."}
		params := pairParams(rec, "green", impl,
			verdictOutcome("request-changes", t009VisualMajor(), t009Minor()), impl,
			verdictOutcome("request-changes", t009VisualMajor(), t009Minor()), impl,
			verdictOutcome("request-changes", t009VisualMajor(), t009Minor()))
		params.Rounds = 3
		check := t009Check(required)
		check.Measure = func(context.Context, int, string) *VisualMeasurement { return failing324() }
		params.Visual = check
		params.OnEvent = func(kind string, data map[string]interface{}) {
			kinds = append(kinds, kind)
			if kind == "visual_observation" {
				observed = append(observed, data)
			}
		}
		res, err := ExecuteScript(context.Background(), PairScript(), params)
		if err != nil {
			t.Fatal(err)
		}
		return res, kinds, observed
	}
	res, _, observed := run(false)
	if res.State.Verdict != "approve" || res.Rounds != 1 {
		t.Errorf("diagnostic: verdict %q after %d round(s), want approve after 1", res.State.Verdict, res.Rounds)
	}
	if len(observed) != 1 || observed[0]["original_verdict"] != "request-changes" || observed[0]["effective_verdict"] != "approve" {
		t.Fatalf("diagnostic: visual_observation = %v", observed)
	}
	if v, _ := res.Outcome.Parsed.(*agent.Verdict); v == nil || len(v.Findings) != 1 || v.Findings[0].Severity != "minor" {
		t.Errorf("diagnostic: the verdict kept %v, want only the minor finding", v)
	}
	if strings.Contains(res.Transcript.Render(false, ""), "INV-2") {
		t.Errorf("the demoted finding reached the transcript the next seats read")
	}

	res, kinds, observed := run(true)
	if res.State.Verdict != "request-changes" || len(observed) != 0 || strings.Contains(strings.Join(kinds, ","), "visual_observation") {
		t.Errorf("required: verdict %q, observations %v — the required check must be untouched", res.State.Verdict, observed)
	}
}

// Codex on #170: told that a diagnostic visual_check finding does not block,
// a compliant reviewer approves while carrying it as a major. The verdict
// parser must hand that to the guard, not reject it; and in required mode
// the same verdict must still block.
func TestAnApprovalCarryingOnlyVisualCheckMajorsReachesTheGuard(t *testing.T) {
	raw := `{"verdict":"approve","findings":[{"severity":"major","file":"index.html","line":27,` +
		`"issue":"The title block sits 20px above the reference.","fix":"Lower it.","visual_check":true},` +
		`{"severity":"minor","file":"logic.mjs","line":1516,"issue":"store menu never commits","fix":"Handle menu id store."}]}`
	for _, required := range []bool{false, true} {
		parsed, err := agent.ParseContract("verdict", raw)
		if err != nil {
			t.Fatalf("required=%v: approve + visual_check major rejected by the parser: %v", required, err)
		}
		v := parsed.(*agent.Verdict)
		t009Check(required).demoteDiagnostic(v, failing324())
		want := "approve"
		if required {
			want = "request-changes"
		}
		if v.Verdict != want {
			t.Errorf("required=%v: verdict %s, want %s", required, v.Verdict, want)
		}
	}
	// A real major without the field is still a contradiction the parser refuses.
	bad := `{"verdict":"approve","findings":[{"severity":"major","file":"index.html","line":1,"issue":"dispatch is never reached","fix":"import it"}]}`
	if _, err := agent.ParseContract("verdict", bad); err == nil {
		t.Errorf("approve with a non-visual major was accepted")
	}
}
