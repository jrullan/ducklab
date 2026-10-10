package strategy

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jrullan/ducklab/internal/agent"
)

// Harness-measured acceptance slices (B-505, B-506, B-507).
//
// TI-36X T-008, r-20261005-012549-uvns: slice 1 asked for the calculator
// "matching REF-IMG-6c63e390's layout", and the project's [[render.compare]]
// held calculator.png against that photo. The implementer luna could see,
// but no turn inside a round was ever rendered, so it reported the slice
// "partial — pixel similarity was not verified" — honestly, three times. The
// report counted as distress (Undelivered [1]): three advisor consults, the
// blind advisor groping for PNGs and calling verify_run "your pixel oracle",
// then a stuck_deliverable escalation. A reviewer approval would have been
// turned into request-changes on the same self-report.
//
// The slice is the visual check's to measure, not the implementer's to
// prove. Here the strategy learns which slices those are and asks the harness
// for its latest measurement of the tree as it is: the implementer is told
// the slice is measured and shown the figure, its self-report on the slice is
// neither distress nor a stuck item, and the reviewer-approval conversion
// uses the measurement. Every other slice keeps the honesty mechanism.

// VisualCheck is the run's visual comparison as the strategy sees it. A nil
// *VisualCheck is valid: no slice is harness-measured and nothing renders.
type VisualCheck struct {
	// Slices maps a one-based deliverable id to the reference ids it cites
	// that a comparison covers.
	Slices map[int][]string
	// Compares is the comparison contract, in the project's order.
	Compares []VisualCompare
	// Required is enforcement "required": a mismatch fails the run like a red
	// test. Otherwise the check is diagnostic: a mismatch is a caveat.
	Required bool
	// Measure returns the harness's measurement of the tree as it is now,
	// rendering first when the tree changed since the last one (nothing is
	// rendered twice for the same tree). nil when none could be taken.
	Measure func(ctx context.Context, round int, phase string) *VisualMeasurement

	// mu guards seen: every mismatch figure a measurement of this run put in
	// front of a seat, which the diagnostic guard's fallback matches (B-516).
	mu   sync.Mutex
	seen []float64
}

// VisualCompare is one capture held against one reference.
type VisualCompare struct {
	Capture   string
	Reference string
	// Tolerance is the largest fraction of pixels allowed to differ;
	// Threshold how different one pixel must look to count.
	Tolerance float64
	Threshold float64
}

// VisualMeasurement is one render of the tree held against its references.
type VisualMeasurement struct {
	Round int
	Phase string
	// Current is false when the tree changed after this render and the
	// render that followed failed: the figure describes an earlier tree.
	Current bool
	Results []VisualResult
}

// VisualResult is one comparison's figure.
type VisualResult struct {
	Capture   string
	Reference string
	Mismatch  float64
	Tolerance float64
	Passed    bool
	Error     string
}

// Visual slice states, as the reviewer conversion and the record name them.
const (
	visualPassed     = "passed"
	visualFailed     = "failed"
	visualUnmeasured = "unmeasured"
)

var refImageIDRe = regexp.MustCompile(`REF-IMG-[0-9a-fA-F]{8}`)

// VisualSlices maps each deliverable that cites a reference image covered by
// a comparison to the references it cites. The citation must be the slice's
// own: a task that reaches the photo only through its SPEC and REQ sections
// is shown the photo (B-504) but its slices remain the implementer's to
// prove.
func VisualSlices(deliverables []string, compares []VisualCompare) map[int][]string {
	covered := map[string]bool{}
	for _, c := range compares {
		covered[strings.ToLower(strings.TrimSpace(c.Reference))] = true
	}
	out := map[int][]string{}
	for i, d := range deliverables {
		seen := map[string]bool{}
		for _, m := range refImageIDRe.FindAllString(d, -1) {
			id := "REF-IMG-" + strings.ToLower(strings.TrimPrefix(m, "REF-IMG-"))
			if covered[strings.ToLower(id)] && !seen[id] {
				seen[id] = true
				out[i+1] = append(out[i+1], id)
			}
		}
	}
	return out
}

// Who is shown the figure (B-516).
//
// TI-36X T-009 r-20261010-011504-4oml: "Interactive controls with
// pressed-state feedback". Neither its body nor its slices cite
// REF-IMG-6c63e390; the photo reached it through SPEC-002. #163 armed the
// render for every task citing a reference anywhere in its chain, so every
// T-009 review carried "32.4% of pixels differ (allowed 30.0%)" with no
// mode. The reviewer made an invariant of the allowance ("INV-2: the
// rendered device matches REF-IMG-6c63e390 within the 30% pixel-difference
// allowance" — in no project document), filed it major three rounds running,
// and the run FAILED on dissent over T-008's accepted appearance, which T-009
// cannot and should not change.
//
// The rule: the figure, the capture, the diff — and the renders that produce
// them — belong to a task whose OWN acceptance cites a reference a
// [[render.compare]] covers. That task is the one responsible for the
// appearance. A task reaching the reference only through the SPEC and REQ
// sections it implements is shown the reference as context (B-504), and
// nothing is rendered for it.
//
// "Own acceptance" is the task's acceptance slices. A task with no slice list
// is held to its body — the implementer's work contract falls back to it
// (ExtractDeliverables numbers the bare title) — so for such a task its own
// title and body are its acceptance, and they decide. The SPEC/REQ chain
// never does: it describes the product, which several tasks build.

// OwnReferences lists, once each and normalised, the REF-IMG ids a task's own
// acceptance cites under the rule above. listed says the task has an
// acceptance slice list of its own; when it does not, ownText (its title and
// body) is read instead.
func OwnReferences(deliverables []string, listed bool, ownText string) []string {
	texts := deliverables
	if !listed {
		texts = []string{ownText}
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range texts {
		for _, m := range refImageIDRe.FindAllString(t, -1) {
			id := "REF-IMG-" + strings.ToLower(strings.TrimPrefix(m, "REF-IMG-"))
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// OwnsVisualCheck reports whether a task owns the visual check: its own
// acceptance (OwnReferences) cites a reference one of compares covers.
func OwnsVisualCheck(deliverables []string, listed bool, ownText string, compares []VisualCompare) bool {
	covered := map[string]bool{}
	for _, c := range compares {
		covered[strings.ToLower(strings.TrimSpace(c.Reference))] = true
	}
	for _, id := range OwnReferences(deliverables, listed, ownText) {
		if covered[strings.ToLower(id)] {
			return true
		}
	}
	return false
}

// VisualModeStatement is what every prompt showing a visual-check figure says
// about the check's mode: the implementer's, reviewer's and advisor's
// sections here, the service's "Visual check of the candidate" section on
// every turn through the runner (retries, resumes, judges), and the figure
// carried from a failed run. One renderer, so no two prompts can disagree
// about what the figure means (B-516).
func VisualModeStatement(required bool) string {
	if required {
		return "Mode: required. A capture over its tolerance fails the run like a red test: for a slice that cites the " +
			"reference, the figure is an acceptance criterion and blocks approval."
	}
	return "Mode: diagnostic. This figure is NOT an acceptance criterion: it is a caveat shown to the person beside the " +
		"images and never fails the run. It does not justify a major or critical finding by itself, and its tolerance is " +
		"not an invariant of this task. A concrete appearance defect you can point to in the capture or the code is judged " +
		"on its own, without the percentage."
}

// VisualFindingRule is the reviewer's half of the contract: a finding whose
// basis is the figure says so, so the harness can tell it apart.
func VisualFindingRule(required bool) string {
	rule := `If a finding of yours rests on this figure, set "visual_check": true on it. Choose your verdict by your other ` +
		`findings: the harness decides by the check's mode what a visual_check finding does to the verdict.`
	if !required {
		rule += " In diagnostic mode it is recorded as an observation for the person and does not block; " +
			"neither does a finding that cites the measured percentage or the allowance."
	}
	return rule
}

func (v *VisualCheck) armed() bool { return v != nil && v.Measure != nil }

// ids are the harness-measured slice ids.
func (v *VisualCheck) ids() map[int]bool {
	if v == nil || len(v.Slices) == 0 {
		return nil
	}
	out := map[int]bool{}
	for id := range v.Slices {
		out[id] = true
	}
	return out
}

func (v *VisualCheck) sortedIDs() []int {
	var ids []int
	for id := range v.ids() {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// measure asks the harness for the tree's measurement. Never fails the run:
// the figure is evidence, and the final gate still renders on its own.
func (v *VisualCheck) measure(ctx context.Context, round int, phase string) *VisualMeasurement {
	if !v.armed() {
		return nil
	}
	m := v.Measure(ctx, round, phase)
	if m != nil {
		v.mu.Lock()
		for _, r := range m.Results {
			if r.Error == "" {
				v.seen = append(v.seen, r.Mismatch)
			}
		}
		v.mu.Unlock()
	}
	return m
}

// sliceState is the measurement's verdict on slice id: passed when every
// comparison of a reference it cites passed, failed when one failed or could
// not run, unmeasured when no current measurement covers it.
func (v *VisualCheck) sliceState(id int, m *VisualMeasurement) (string, []VisualResult) {
	refs := v.Slices[id]
	if m == nil || !m.Current || len(refs) == 0 {
		return visualUnmeasured, nil
	}
	cites := map[string]bool{}
	for _, r := range refs {
		cites[strings.ToLower(r)] = true
	}
	var results []VisualResult
	state := visualPassed
	for _, r := range m.Results {
		if !cites[strings.ToLower(r.Reference)] {
			continue
		}
		results = append(results, r)
		if r.Error != "" || !r.Passed {
			state = visualFailed
		}
	}
	if len(results) == 0 {
		return visualUnmeasured, nil
	}
	return state, results
}

func visualFigure(r VisualResult) string {
	if r.Error != "" {
		return fmt.Sprintf("%s against %s could not be compared: %s", r.Capture, r.Reference, r.Error)
	}
	return fmt.Sprintf("%s against %s: %.1f%% of pixels differ (allowed %.1f%%)", r.Capture, r.Reference, r.Mismatch*100, r.Tolerance*100)
}

func visualFigures(results []VisualResult) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		parts = append(parts, visualFigure(r))
	}
	return strings.Join(parts, "; ")
}

// contractLines states the comparison contract: what is compared, how, when
// it runs, and what a mismatch does. The advisor in the incident was told
// none of it and filled the gap with guesses.
func (v *VisualCheck) contractLines() string {
	var b strings.Builder
	for _, c := range v.Compares {
		fmt.Fprintf(&b, "- capture `%s` against %s: at most %.1f%% of its pixels may differ (a pixel counts as different past %.0f%% of the largest colour distance)\n",
			c.Capture, c.Reference, c.Tolerance*100, c.Threshold*100)
	}
	b.WriteString("\nThe harness runs this comparison itself: it renders the tree with the project's capture command before every " +
		"implementer, advisor and reviewer turn that follows a change to the tree, after each round's gate, and at the final gate. " +
		"verify_run does NOT run it — verify_run runs the project's tests — and no tool a seat holds runs it; oracle_dispute is for " +
		"oracle tests, not for this comparison.\n")
	b.WriteString(VisualModeStatement(v.Required) + "\n")
	return b.String()
}

// latestLine is the latest measurement in one paragraph.
func latestLine(m *VisualMeasurement) string {
	if m == nil {
		return "Latest measurement: none yet in this run.\n"
	}
	var parts []string
	for _, r := range m.Results {
		parts = append(parts, visualFigure(r))
	}
	line := fmt.Sprintf("Latest measurement (rendered %s, round %d): %s.", m.Phase, m.Round, strings.Join(parts, "; "))
	if !m.Current {
		line += " The tree has changed since, and the render after the change failed: this figure describes an earlier tree."
	}
	return line + "\n"
}

func idList(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return strings.Join(parts, ", ")
}

// forImplementer tells the implementer which slices the harness measures and
// what it measured last.
func (v *VisualCheck) forImplementer(m *VisualMeasurement) string {
	if !v.armed() {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Visual check — measured by the harness\n\n")
	if ids := v.sortedIDs(); len(ids) > 0 {
		fmt.Fprintf(&b, "Slice %s cites a reference image the project's visual check compares a capture against, so the harness measures it, not you:\n\n", idList(ids))
	} else {
		b.WriteString("This task's reference image is compared with a capture of the product by the project's visual check:\n\n")
	}
	b.WriteString(v.contractLines())
	b.WriteString("\n" + latestLine(m))
	if len(v.Slices) > 0 {
		b.WriteString("\nBuild what the reference shows and report the slice by what you built. Its pass or fail is the harness's " +
			"figure, not your report: you are not asked to prove it, and a status you give it is not counted against you. " +
			"Do not change the product only to move the percentage.\n")
	}
	return b.String()
}

// forReviewer replaces the implementer's self-report on the measured slices
// with the measurement, and says what each state means for the verdict.
func (v *VisualCheck) forReviewer(m *VisualMeasurement) string {
	if !v.armed() || len(v.Slices) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Harness-measured slices\n\n")
	b.WriteString("These slices are measured by the project's visual check, not by the implementer; its status for them above is not evidence either way.\n\n")
	for _, id := range v.sortedIDs() {
		state, results := v.sliceState(id, m)
		switch state {
		case visualPassed:
			fmt.Fprintf(&b, "- slice %d: passed — %s.\n", id, visualFigures(results))
		case visualFailed:
			if v.Required {
				fmt.Fprintf(&b, "- slice %d: FAILED — %s. The check is required: this blocks approval.\n", id, visualFigures(results))
			} else {
				fmt.Fprintf(&b, "- slice %d: over tolerance — %s. The check is diagnostic: the person sees this figure as a caveat, "+
					"and it does not block your verdict. Judge the appearance against the reference where you can see it, "+
					"and raise concrete appearance defects as findings.\n", id, visualFigures(results))
			}
		default:
			fmt.Fprintf(&b, "- slice %d: not measured on the current tree (the render failed or none ran); the final gate measures it.\n", id)
		}
	}
	b.WriteString("\n" + VisualModeStatement(v.Required) + " " + VisualFindingRule(v.Required) + "\n")
	return b.String()
}

// forAdvisor states the contract and the figure for the duck, and what the
// measured slices are not.
func (v *VisualCheck) forAdvisor(m *VisualMeasurement) string {
	if !v.armed() {
		return ""
	}
	var b strings.Builder
	b.WriteString("### The visual comparison (owned by the harness)\n\n")
	b.WriteString(v.contractLines())
	b.WriteString("\n" + latestLine(m))
	if ids := v.sortedIDs(); len(ids) > 0 {
		fmt.Fprintf(&b, "\nSlice %s is measured by this comparison: the implementer cannot prove it, and a 'partial' on it is not a gap "+
			"for you to close. ", idList(ids))
	} else {
		b.WriteString("\n")
	}
	b.WriteString("Never advise a change whose purpose is to move the percentage rather than to make the product look more like the reference.\n\n")
	return b.String()
}

// visualGap lists the measured slices that block an approval: those that
// failed a required check. A diagnostic mismatch is the person's caveat, and
// an unmeasured slice is the final gate's.
func (v *VisualCheck) visualGap(m *VisualMeasurement) map[int][]VisualResult {
	out := map[int][]VisualResult{}
	if !v.armed() {
		return out
	}
	for id := range v.Slices {
		if state, results := v.sliceState(id, m); state == visualFailed && v.Required {
			out[id] = results
		}
	}
	return out
}

// The diagnostic guard (B-516).
//
// The mode statement is advice; 4oml's reviewer had a figure and no mode,
// and nothing in the harness stopped its invented invariant from failing the
// run. Under a diagnostic check a reviewer finding whose basis is the figure
// cannot block: it is taken out of the verdict, recorded as an observation
// (visual_observation, shown on the reviewer's verdict in the desktop), and
// the verdict is decided as if it had never blocked. A required check is
// untouched: there the figure is an acceptance criterion.
//
// Detection is deterministic and has two arms:
//   - the contract field: the reviewer marks a finding "visual_check": true
//     (VisualFindingRule asks it to, wherever the figure is shown);
//   - a conservative fallback for a model that ignores the field: the
//     finding's issue, invariant or fix quotes a percentage equal (at the
//     precision written) to a figure this run actually showed — a
//     comparison's tolerance, or a mismatch a measurement produced — AND
//     that percentage is bound to the comparison where it is written: "of
//     (the) pixels" right after it, or an allowance, tolerance, difference
//     or mismatch word right beside it; AND the finding names the comparison
//     itself: the visual check or comparison, a pixel difference, the
//     difference image, a share "of pixels", or a REF-IMG id. A percentage
//     alone never matches ("30% of 50 shows 0.15" is a calculator defect),
//     and the bare word "pixels" is not a comparison: Codex on #170
//     reproduced "At 30% viewport width, the keypad overflows its container
//     by 12 pixels" — a real layout defect, under a 30% tolerance — being
//     taken out of the verdict when "pixels" anywhere counted.
//
// The verdict afterwards: request-changes with no critical or major finding
// left becomes approve, keeping its minor findings. That is today's rule, not
// a new one — the verdict contract rejects request-changes without a blocking
// finding and tells the reviewer to "use approve to preserve minor
// observations" (agent.parseVerdict) — applied to what remains.

// VisualObservation is one reviewer finding the guard took out of a verdict.
type VisualObservation struct {
	Finding agent.Finding `json:"finding"`
	// Basis is how the finding was recognised: "field" when the reviewer
	// marked it visual_check, "figure" when it quotes the measured figure.
	Basis string `json:"basis"`
}

var (
	percentRe     = regexp.MustCompile(`(\d+(?:\.(\d+))?)\s*%`)
	visualVocabRe = regexp.MustCompile(`(?i)pixel[- ]difference|\bpixels? differ|\bof (?:the )?pixels\b|visual[- ](?:check|comparison)|difference image|\bdiff image\b|REF-IMG-[0-9a-f]{8}`)
	// figureAfterRe and figureNearRe bind one percentage to the comparison:
	// "32.4% of pixels", "(allowed 30.0%)", "the 30% allowance", "differs
	// by 33.5%". They are matched on the few words around that percentage
	// only, never on the whole finding.
	figureAfterRe = regexp.MustCompile(`(?i)^\s*(?:of (?:the |its |all )?pixels\b|pixels?\b|pixel[- ]difference|(?:pixel[- ])?(?:allowance|tolerance|mismatch|difference))`)
	figureNearRe  = regexp.MustCompile(`(?i)\b(?:allowed|allowance|tolerance|mismatch|differ(?:s|ing|ence)?)\b`)
)

// guards reports whether the diagnostic guard applies: a comparison is
// configured and it is not required.
func (v *VisualCheck) guards() bool { return v != nil && !v.Required && len(v.Compares) > 0 }

// figures are the percentages this run put in front of a seat.
func (v *VisualCheck) figures(m *VisualMeasurement) []float64 {
	var out []float64
	for _, c := range v.Compares {
		out = append(out, c.Tolerance*100)
	}
	v.mu.Lock()
	for _, x := range v.seen {
		out = append(out, x*100)
	}
	v.mu.Unlock()
	if m != nil {
		for _, r := range m.Results {
			if r.Error == "" {
				out = append(out, r.Mismatch*100, r.Tolerance*100)
			}
		}
	}
	return out
}

// citesFigure reports whether a finding quotes one of figures while speaking
// of the comparison.
func citesFigure(f agent.Finding, figures []float64) bool {
	text := f.Issue + "\n" + f.Invariant + "\n" + f.Fix
	if !visualVocabRe.MatchString(text) {
		return false
	}
	for _, idx := range percentRe.FindAllStringSubmatchIndex(text, -1) {
		if !figureBound(text, idx[0], idx[1]) {
			continue
		}
		m := []string{text[idx[0]:idx[1]], text[idx[2]:idx[3]], ""}
		if idx[4] >= 0 {
			m[2] = text[idx[4]:idx[5]]
		}
		p, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		// "30%" stands for anything that rounds to 30; "32.4%" for what
		// rounds to 32.4 — the precision the reviewer wrote.
		half := 0.5 / math.Pow(10, float64(len(m[2])))
		for _, x := range figures {
			if math.Abs(p-x) <= half+1e-9 {
				return true
			}
		}
	}
	return false
}

// figureBound reports whether the percentage at text[start:end] is written as
// the comparison's figure: "of pixels" (or a pixel/allowance word) right
// after it, or an allowance/tolerance/difference/mismatch word within a few
// words on either side, inside the same clause.
func figureBound(text string, start, end int) bool {
	after := text[end:]
	if figureAfterRe.MatchString(after) {
		return true
	}
	clause := func(s string) string {
		if i := strings.IndexAny(s, ".;:\n"); i >= 0 {
			return s[:i]
		}
		return s
	}
	lo := start - 28
	if lo < 0 {
		lo = 0
	}
	before := text[lo:start]
	if i := strings.LastIndexAny(before, ".;:\n"); i >= 0 {
		before = before[i+1:]
	}
	hi := 28
	if hi > len(after) {
		hi = len(after)
	}
	return figureNearRe.MatchString(before) || figureNearRe.MatchString(clause(after[:hi]))
}

// demoteDiagnostic applies the guard to a reviewer's verdict in place and
// returns what it took out, and the verdict as the reviewer gave it.
func (v *VisualCheck) demoteDiagnostic(verdict *agent.Verdict, m *VisualMeasurement) ([]VisualObservation, string) {
	if !v.guards() || verdict == nil {
		return nil, ""
	}
	original := verdict.Verdict
	figures := v.figures(m)
	kept := []agent.Finding{}
	var observed []VisualObservation
	for _, f := range verdict.Findings {
		switch {
		case f.VisualCheck:
			observed = append(observed, VisualObservation{Finding: f, Basis: "field"})
		case citesFigure(f, figures):
			observed = append(observed, VisualObservation{Finding: f, Basis: "figure"})
		default:
			kept = append(kept, f)
		}
	}
	if len(observed) == 0 {
		return nil, original
	}
	verdict.Findings = kept
	if verdict.Verdict == "request-changes" && len(verdict.Blocking()) == 0 {
		verdict.Verdict = "approve"
	}
	return observed, original
}
