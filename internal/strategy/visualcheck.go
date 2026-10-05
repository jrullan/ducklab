package strategy

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
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
	return v.Measure(ctx, round, phase)
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
	if v.Required {
		b.WriteString("It is required: a mismatch over the tolerance fails the run like a red test.\n")
	} else {
		b.WriteString("It is diagnostic: a mismatch over the tolerance is reported to the person as a caveat beside the images; it does not fail the run.\n")
	}
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
					"and it does not block your verdict by itself. Judge the appearance against the reference where you can see it, "+
					"and raise concrete appearance defects as findings.\n", id, visualFigures(results))
			}
		default:
			fmt.Fprintf(&b, "- slice %d: not measured on the current tree (the render failed or none ran); the final gate measures it.\n", id)
		}
	}
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
