package stage

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/strategy"
)

// B-518 (TI-36X r-20261010-121543-eett, r-20261010-115519-tpfn). The fixture
// is eett's own record: base.md is REQ-001..013 as approved, request.md the
// two-item amendment, candidate.md what architect k3 composed — REQ-008's
// fraction paragraph and REQ-009's `Ans` sentence gone, approved by an
// in-loop critic that never saw the previous text, blocked only afterwards by
// the composition reviewer, and FAILED at the gate with three rounds unspent.

type b518Event struct {
	kind string
	data map[string]interface{}
}

// b518Seats drives the REAL scheduler (strategy.ExecuteScript) with scripted
// seat replies, so these tests exercise the same wiring the service does.
type b518Seats struct {
	t                  *testing.T
	architect          []string
	critic             []string
	composition        []string
	architectPrompts   []string
	criticPrompts      []string
	compositionPrompts []string
	scripts            []*strategy.Script
	events             []b518Event
}

func (s *b518Seats) onEvent(kind string, data map[string]interface{}) {
	s.events = append(s.events, b518Event{kind, data})
}

func (s *b518Seats) pop(queue *[]string, who string) string {
	s.t.Helper()
	if len(*queue) == 0 {
		s.t.Fatalf("no scripted reply left for %s", who)
	}
	next := (*queue)[0]
	*queue = (*queue)[1:]
	return next
}

func (s *b518Seats) execute(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
	if script.Name == "composition-review" {
		s.compositionPrompts = append(s.compositionPrompts, prompt)
		return s.pop(&s.composition, "composition reviewer"), nil
	}
	s.scripts = append(s.scripts, script)
	res, err := strategy.ExecuteScript(ctx, script, &strategy.ExecuteParams{
		Prompt:  prompt,
		OnEvent: s.onEvent,
		Runner: func(_ context.Context, turn *strategy.Turn, _ config.DucklingID, prompt string, _ []string, _ strategy.TurnContext) (*agent.Outcome, error) {
			switch turn.Role {
			case config.RoleArchitect:
				s.architectPrompts = append(s.architectPrompts, prompt)
				return &agent.Outcome{Text: s.pop(&s.architect, "architect")}, nil
			case config.RoleReviewer:
				s.criticPrompts = append(s.criticPrompts, prompt)
				text := s.pop(&s.critic, "critic")
				parsed, err := agent.ParseContract("verdict", text)
				if err != nil {
					return nil, err
				}
				return &agent.Outcome{Text: text, Parsed: parsed}, nil
			}
			return nil, fmt.Errorf("unexpected role %s", turn.Role)
		},
	})
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

func (s *b518Seats) eventsOf(kind string) []b518Event {
	var out []b518Event
	for _, e := range s.events {
		if e.kind == kind {
			out = append(out, e)
		}
	}
	return out
}

const b518Approve = `{"verdict":"approve","findings":[]}`

func b518Fixture(t *testing.T) (base, candidate *artifact.Document, request string) {
	t.Helper()
	read := func(name string) string {
		data, err := os.ReadFile("testdata/b518/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	var err error
	if base, err = artifact.Parse(read("base.md"), artifact.KindRequirements); err != nil {
		t.Fatal(err)
	}
	if candidate, err = artifact.Parse(read("candidate.md"), artifact.KindRequirements); err != nil {
		t.Fatal(err)
	}
	return base, candidate, read("request.md")
}

func b518Section(t *testing.T, doc *artifact.Document, id string) string {
	t.Helper()
	sec := doc.Section(id)
	if sec == nil {
		t.Fatalf("fixture has no %s", id)
	}
	return renderSectionMarkdown(*sec)
}

const (
	b518Fraction = "The calculator shall support fraction entry and evaluation, mixed-number display where applicable, and conversion between exact fractional and decimal representations."
	b518Ans      = "The calculator shall retain the most recent successful result as `Ans`, insertable into a new expression."
)

// eett's architect fragment, verbatim: REQ-008 and REQ-009 as k3 emitted them.
func b518DeletingFragment(t *testing.T, candidate *artifact.Document) string {
	return b518Section(t, candidate, "REQ-008") + "\n\n" + b518Section(t, candidate, "REQ-009")
}

// The repair a correct architect returns: both requested fixes applied, the
// fraction paragraph REWORDED (not copied) and Ans restored.
const b518RepairedFragment = `## REQ-008 — Fraction, complex, and numeric-format operations

**Priority:** must

Fraction entry and evaluation shall be supported by the calculator, together with mixed-number display where applicable and conversion between exact fractional and decimal representations.

The calculator shall support complex numbers as first-class values: entry of the imaginary unit ` + "`i`" + `, rectangular and polar forms, and arithmetic that promotes real operands to complex when either operand is complex. A complex result shall be convertible between rectangular and polar form via the NUM menu, with the polar angle rendered in the active angle unit; real-to-polar conversion of a real scalar is a valid operation. A conversion that does not apply to the current result shall surface an LCD error and shall preserve the existing result unchanged.

**Assumption:** Exact symbolic algebra beyond rational fractions is not required; irrational or transcendental sub-expressions may be evaluated numerically and degrade the result from an exact rational to a real value.

## REQ-009 — Memory, variables, and answer value

**Priority:** must

The calculator shall retain the most recent successful result as ` + "`Ans`" + `, insertable into a new expression.

The calculator shall provide the named user variables printed on the keypad, with store, recall, and clear-variable operations that clear stored variables to the chosen label without prescribing a confirmation mechanism. Recalling a variable inserts its current value into the entry; storing assigns the current result or entry value to the chosen variable. Stored values shall remain available until explicitly cleared or the page is reloaded; no persistence across reloads is required.`

func b518Params(t *testing.T, seats *b518Seats) Params {
	return Params{ProjectRoot: t.TempDir(), Stage: Intake, RunID: "r-b518", Mode: "council", Execute: seats.execute, OnEvent: seats.onEvent}
}

// The eett shape, end to end: the deterministic check catches both removals
// before any critic runs, the architect repairs them, and the critic's prompt
// carries every touched section's approved text beside the candidate.
func TestB518AmendmentRemovalIsCaughtBeforeReviewAndCriticSeesPreviousText(t *testing.T) {
	base, candidate, request := b518Fixture(t)
	seats := &b518Seats{t: t,
		architect:   []string{b518DeletingFragment(t, candidate), b518RepairedFragment},
		critic:      []string{b518Approve},
		composition: []string{b518Approve},
	}
	res, err := runFragment(context.Background(), b518Params(t, seats), base, request)
	if err != nil {
		t.Fatal(err)
	}

	checks := seats.eventsOf("structure_check")
	if len(checks) != 1 || checks[0].data["category"] != "removal" {
		t.Fatalf("structure_check events = %+v, want one removal check", checks)
	}
	findings := fmt.Sprint(checks[0].data["findings"])
	for _, want := range []string{"REQ-008", b518Fraction, "REQ-009", "retain the most recent successful result"} {
		if !strings.Contains(findings, want) {
			t.Errorf("removal findings lack %q:\n%s", want, findings)
		}
	}
	if len(seats.architectPrompts) != 2 || !strings.Contains(seats.architectPrompts[1], "Removal check — repair or justify") ||
		!strings.Contains(seats.architectPrompts[1], b518Fraction) {
		t.Fatalf("the architect was not sent back with the removed passages:\n%s", strings.Join(seats.architectPrompts, "\n=====\n"))
	}

	if len(seats.criticPrompts) != 1 {
		t.Fatalf("critic turns = %d, want 1", len(seats.criticPrompts))
	}
	critic := seats.criticPrompts[0]
	for _, want := range []string{
		"## Amendment delta — approved text vs candidate",
		"Check every removal against the human request",
		"### REQ-008 — changed", "Approved previous text:", b518Fraction,
		"### REQ-009 — changed", b518Ans,
	} {
		if !strings.Contains(critic, want) {
			t.Errorf("critic prompt lacks %q", want)
		}
	}
	if strings.Contains(critic, "### REQ-001 —") {
		t.Errorf("an untouched section was presented as touched")
	}
	if strings.Contains(critic, "Approved content the engine could not find") {
		t.Errorf("the repaired candidate still reports missing content:\n%s", critic)
	}
	body := artifact.RenderBody(res.Proposed)
	if !strings.Contains(body, "Fraction entry and evaluation shall be supported") || !strings.Contains(body, "`Ans`") {
		t.Fatalf("the proposal lost the restored content:\n%s", body)
	}
	if res.CompositionReview == nil || res.CompositionReview.Verdict != "approve" {
		t.Fatalf("composition verdict = %+v", res.CompositionReview)
	}
}

// An architect that neither restores nor justifies within the bounded repair
// attempts does not stall the run: the critic receives the removals as
// blocking engine findings, its approval is lowered, and the council's normal
// revision round fixes them.
func TestB518UnrepairedRemovalBlocksTheCriticApproval(t *testing.T) {
	base, candidate, request := b518Fixture(t)
	deleting := b518DeletingFragment(t, candidate)
	seats := &b518Seats{t: t,
		// Draft, one removal repair that changes nothing (same signature ends
		// the repair chain), then the council revision after the lowered verdict.
		architect:   []string{deleting, deleting, b518RepairedFragment},
		critic:      []string{b518Approve, b518Approve},
		composition: []string{b518Approve},
	}
	res, err := runFragment(context.Background(), b518Params(t, seats), base, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(seats.eventsOf("removal_unresolved")); got != 1 {
		t.Fatalf("removal_unresolved events = %d, want 1", got)
	}
	lowered := seats.eventsOf("removal_verdict_lowered")
	if len(lowered) != 1 || lowered[0].data["original_verdict"] != "approve" {
		t.Fatalf("removal_verdict_lowered = %+v, want the round-1 approval lowered", lowered)
	}
	if !strings.Contains(seats.criticPrompts[0], "## Engine-detected unauthorized removals — blocking") {
		t.Errorf("the critic was not told the removals block")
	}
	if !strings.Contains(seats.architectPrompts[2], "An amendment keeps approved content its request does not remove") {
		t.Errorf("the revision did not receive the lowered verdict's findings:\n%s", seats.architectPrompts[2])
	}
	for _, skipped := range seats.eventsOf("revision_skipped") {
		if skipped.data["round"] == 1 {
			t.Errorf("an unrepaired removal let the council skip its round-1 revision")
		}
	}
	if !strings.Contains(artifact.RenderBody(res.Proposed), "`Ans`") {
		t.Fatalf("the revision's restoration is not the proposal")
	}
}

// Justify, not only repair: a removal the request authorizes in so many words
// survives the check when the architect quotes the request. The quote line is
// protocol and never reaches the document; the critic is asked to verify it.
func TestB518QuotedRequestJustifiesARemoval(t *testing.T) {
	base, _, _ := b518Fixture(t)
	request := "Simplify REQ-009. Drop the answer-memory sentence: `Ans` recall is handled by REQ-005 prior-answer recall."
	without := strings.Replace(b518Section(t, base, "REQ-009"), b518Ans+"\n\n", "", 1)
	if without == b518Section(t, base, "REQ-009") {
		t.Fatal("fixture did not remove the Ans sentence")
	}
	justified := "Removal authorized (REQ-009): \"Drop the answer-memory sentence\"\n\n" + without
	seats := &b518Seats{t: t,
		architect:   []string{without, justified},
		critic:      []string{b518Approve},
		composition: []string{b518Approve},
	}
	res, err := runFragment(context.Background(), b518Params(t, seats), base, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(seats.eventsOf("structure_check")) != 1 || len(seats.eventsOf("removal_justified")) != 1 {
		t.Fatalf("want one removal check then a justification; events: %+v", seats.events)
	}
	if !strings.Contains(seats.criticPrompts[0], "## Removals the architect justified — verify each quote") ||
		!strings.Contains(seats.criticPrompts[0], "Drop the answer-memory sentence") {
		t.Errorf("the critic was not asked to verify the justification")
	}
	if body := artifact.RenderBody(res.Proposed); strings.Contains(body, "Removal authorized") || strings.Contains(body, "`Ans`, insertable") {
		t.Fatalf("proposal kept the protocol line or the removed sentence:\n%s", body)
	}
}

// A paraphrased "quote" is not the request's words: the check stands.
func TestB518ParaphrasedJustificationIsRejected(t *testing.T) {
	base, candidate, request := b518Fixture(t)
	report := removalReport(base, candidate, request, "", []strategy.RemovalJustification{
		{Section: "REQ-008", Quote: "remove the fraction paragraph"},
	})
	if len(report.Justified) != 0 || !strings.Contains(strings.Join(report.Findings, "\n"), b518Fraction) {
		t.Fatalf("paraphrase accepted: %+v", report)
	}
}

// Legitimate rewording is not a removal: reorder, synonym, split into two
// sentences, merge two into one, move to another touched section, list item
// rephrased. The tolerant rule must not send an architect back for these.
func TestB518RewordingIsNotARemoval(t *testing.T) {
	base, _, _ := b518Fixture(t)
	edit := func(id, from, to string) *artifact.Document {
		t.Helper()
		doc, err := artifact.Parse(artifact.RenderBody(base), artifact.KindRequirements)
		if err != nil {
			t.Fatal(err)
		}
		sec := doc.Section(id)
		if !strings.Contains(sec.Body, from) {
			t.Fatalf("%s lacks %q", id, from)
		}
		sec.Body = strings.Replace(sec.Body, from, to, 1)
		return doc
	}
	cases := map[string]*artifact.Document{
		"reordered passive": edit("REQ-008", b518Fraction,
			"Fraction entry and evaluation shall be supported by the calculator, with mixed-number display where applicable and conversion between exact fractional and decimal representations."),
		// Neither half carries 60% of the original alone; together they do.
		"split in two": edit("REQ-008", b518Fraction,
			"The calculator shall support fraction entry and evaluation with mixed-number display. Where applicable it converts between exact fractional and decimal representations."),
		"synonyms": edit("REQ-008", b518Fraction,
			"The calculator shall accept fraction entry and evaluation, show mixed-number display where applicable, and offer conversion between exact fractional and decimal forms."),
		"merged": edit("REQ-009", b518Ans+"\n\nThe calculator shall provide the named user variables",
			"The calculator shall retain the most recent successful result as `Ans` (insertable into a new expression) and shall provide the named user variables"),
	}
	moved := edit("REQ-008", b518Fraction+"\n\n", "")
	moved.Section("REQ-007").Body += "\n\n" + b518Fraction
	cases["moved to another section"] = moved

	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if report := removalReport(base, candidate, "Unrelated request.", "", nil); len(report.Findings) > 0 {
				t.Fatalf("legitimate edit flagged: %v", report.Findings)
			}
		})
	}

	// And the real deletions still are flagged under the same rule.
	deleted := edit("REQ-008", b518Fraction+"\n\n", "")
	if report := removalReport(base, deleted, "Unrelated request.", "", nil); len(report.Findings) != 1 {
		t.Fatalf("deletion findings = %v, want exactly the fraction sentence", report.Findings)
	}
}

// The request itself can authorize: a sentence of the request that asks for
// removal AND talks about that content.
func TestB518RequestAuthorizedRemovalIsNotAFinding(t *testing.T) {
	base, _, _ := b518Fixture(t)
	doc, _ := artifact.Parse(artifact.RenderBody(base), artifact.KindRequirements)
	doc.Section("REQ-009").Body = strings.Replace(doc.Section("REQ-009").Body, b518Ans+"\n\n", "", 1)
	authorizing := "Remove the requirement that the calculator retain the most recent successful result as Ans."
	if report := removalReport(base, doc, authorizing, "", nil); len(report.Findings) != 0 {
		t.Fatalf("request-authorized removal flagged: %v", report.Findings)
	}
	// Mentioning the content without asking for its removal authorizes nothing.
	mentioning := "Clarify that the most recent successful result is stored as Ans after every evaluation."
	if report := removalReport(base, doc, mentioning, "", nil); len(report.Findings) != 1 {
		t.Fatalf("a mention without a removal cue authorized the removal: %+v", report)
	}
}

// The composition reviewer blocks with rounds left: the architect gets a
// repair round with those findings, inside the same round budget, and the
// repaired composition is reviewed again.
func TestB518BlockedCompositionWithRoundsLeftRunsARepairRound(t *testing.T) {
	base, _, request := b518Fixture(t)
	// A REQ-009-only draft the in-loop critic approves but the composition
	// reviewer rejects (it never touched REQ-008's requested fix).
	partial := b518Section(t, base, "REQ-009")
	partial = strings.Replace(partial, "and a confirmed clear-variable operation", "and a clear-variable operation that clears stored variables to the chosen label", 1)
	blocked := `{"verdict":"request-changes","findings":[{"severity":"critical","file":"REQ-008","issue":"the real-scalar polar error example the request removes is still present","fix":"apply the REQ-008 item of the request"}]}`
	seats := &b518Seats{t: t,
		// The repair re-emits ONLY REQ-008: REQ-009's change from the first
		// conversation must survive it (the repair revises the composition).
		architect:   []string{partial, strings.SplitN(b518RepairedFragment, "\n\n## REQ-009", 2)[0]},
		critic:      []string{b518Approve, b518Approve},
		composition: []string{blocked, b518Approve},
	}
	res, err := runFragment(context.Background(), b518Params(t, seats), base, request)
	if err != nil {
		t.Fatal(err)
	}
	started := seats.eventsOf("composition_repair_started")
	if len(started) != 1 || started[0].data["rounds_left"] != 3 {
		t.Fatalf("composition_repair_started = %+v, want one with 3 of 4 rounds left", started)
	}
	if len(seats.scripts) != 2 || seats.scripts[0].RoundsUsed != 1 || seats.scripts[1].MaxRounds != 3 {
		t.Fatalf("round accounting: scripts=%d first used=%d repair max=%d",
			len(seats.scripts), seats.scripts[0].RoundsUsed, seats.scripts[len(seats.scripts)-1].MaxRounds)
	}
	repair := seats.architectPrompts[1]
	for _, want := range []string{"## Post-composition review — repair round", "the real-scalar polar error example the request removes is still present",
		"### REQ-009 — changed", "Approved previous text:"} {
		if !strings.Contains(repair, want) {
			t.Errorf("repair prompt lacks %q", want)
		}
	}
	if len(seats.compositionPrompts) != 2 || res.CompositionReview == nil || res.CompositionReview.Verdict != "approve" {
		t.Fatalf("composition reviews = %d, final = %+v", len(seats.compositionPrompts), res.CompositionReview)
	}
	body := artifact.RenderBody(res.Proposed)
	if strings.Contains(body, "(for example, polar conversion of a real scalar)") || !strings.Contains(body, "clear-variable operation that clears stored variables to the chosen label") {
		t.Fatalf("the repaired composition is not the proposal:\n%s", body)
	}
	// The composition is still judged against the APPROVED base, not the
	// first candidate the repair revised.
	if !strings.Contains(seats.compositionPrompts[1], "and a confirmed clear-variable operation") {
		t.Errorf("the second composition review lost the approved base")
	}
}

// Rounds exhausted: the blocked composition still stops at the gate.
func TestB518BlockedCompositionWithoutRoundsStopsAtTheGate(t *testing.T) {
	base, _, request := b518Fixture(t)
	blocked := `{"verdict":"request-changes","findings":[{"severity":"major","file":"REQ-008","issue":"still wrong","fix":"fix it"}]}`
	seats := &b518Seats{t: t,
		architect:   []string{b518RepairedFragment},
		critic:      []string{b518Approve},
		composition: []string{blocked},
	}
	p := b518Params(t, seats)
	p.Rounds = 1
	res, err := runFragment(context.Background(), p, base, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(seats.eventsOf("composition_repair_started")) != 0 || len(seats.eventsOf("composition_repair_exhausted")) != 1 {
		t.Fatalf("want no repair and one exhausted record; events: %+v", seats.events)
	}
	if res.CompositionReview == nil || res.CompositionReview.Verdict != "request-changes" {
		t.Fatalf("the blocked verdict must reach the gate: %+v", res.CompositionReview)
	}
}

// Solo has no critic, but its architect still faces the deterministic check.
func TestB518SoloAmendmentFacesTheRemovalCheck(t *testing.T) {
	base, candidate, request := b518Fixture(t)
	seats := &b518Seats{t: t,
		architect:   []string{b518DeletingFragment(t, candidate), b518RepairedFragment},
		composition: []string{b518Approve},
	}
	p := b518Params(t, seats)
	p.Mode = "solo"
	res, err := runFragment(context.Background(), p, base, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(seats.eventsOf("structure_check")) != 1 || len(seats.architectPrompts) != 2 {
		t.Fatalf("solo architect was not sent back: checks=%d architect turns=%d", len(seats.eventsOf("structure_check")), len(seats.architectPrompts))
	}
	if !strings.Contains(artifact.RenderBody(res.Proposed), "Fraction entry and evaluation shall be supported") {
		t.Fatalf("solo proposal lost the restored paragraph")
	}
}

// The section-wise route (a small architect) under the real scheduler: the
// isolated REQ-008 pass faces the same check, and its critic sees REQ-008's
// approved text — and no sibling section, which its scope forbids it to name.
func TestB518SectionWisePassFacesTheCheckAndItsCriticSeesOnlyItsSection(t *testing.T) {
	base, candidate, request := b518Fixture(t)
	repaired := strings.SplitN(b518RepairedFragment, "\n\n## REQ-009", 2)[0]
	seats := &b518Seats{t: t,
		architect:   []string{"REQ-008", b518Section(t, candidate, "REQ-008"), repaired},
		critic:      []string{b518Approve},
		composition: []string{b518Approve},
	}
	p := b518Params(t, seats)
	p.SectionWise = true
	res, err := runFragment(context.Background(), p, base, request)
	if err != nil {
		t.Fatal(err)
	}
	if checks := seats.eventsOf("structure_check"); len(checks) != 1 || !strings.Contains(fmt.Sprint(checks[0].data["findings"]), b518Fraction) {
		t.Fatalf("section pass removal check = %+v", checks)
	}
	critic := seats.criticPrompts[0]
	if !strings.Contains(critic, "### REQ-008 — changed") || !strings.Contains(critic, b518Fraction) || strings.Contains(critic, "### REQ-009") {
		t.Fatalf("section critic delta is not scoped to REQ-008:\n%s", critic)
	}
	if !strings.Contains(artifact.RenderBody(res.Proposed), "Fraction entry and evaluation shall be supported") {
		t.Fatalf("the section pass's restoration is not the proposal")
	}
}

// A later section pass's critic sees its own section only, although the
// composition it is placed into already carries an earlier pass's change.
func TestB518SectionWiseDeltaIsScopedToThePass(t *testing.T) {
	base, _, request := b518Fixture(t)
	parts := strings.SplitN(b518RepairedFragment, "\n\n## REQ-009", 2)
	seats := &b518Seats{t: t,
		architect:   []string{"REQ-008\nREQ-009", parts[0], "## REQ-009" + parts[1]},
		critic:      []string{b518Approve, b518Approve},
		composition: []string{b518Approve},
	}
	p := b518Params(t, seats)
	p.SectionWise = true
	if _, err := runFragment(context.Background(), p, base, request); err != nil {
		t.Fatal(err)
	}
	if len(seats.criticPrompts) != 2 {
		t.Fatalf("critic turns = %d, want 2", len(seats.criticPrompts))
	}
	second := seats.criticPrompts[1]
	if !strings.Contains(second, "### REQ-009 — changed") || strings.Contains(second, "### REQ-008") {
		t.Fatalf("REQ-009 pass critic delta is not scoped:\n%s", second)
	}
}

// A requested split moves a concern to a NEW pass the narrowed section's pass
// cannot see; the deterministic check does not flag that move.
func TestB518SectionWiseSplitIsNotARemoval(t *testing.T) {
	base, _, _ := b518Fixture(t)
	request := "Split REQ-009 into answer memory and named variables."
	narrowed := strings.Replace(b518Section(t, base, "REQ-009"), b518Ans+"\n\n", "", 1)
	seats := &b518Seats{t: t,
		architect: []string{"REQ-009\nNEW: Answer memory", narrowed,
			"## REQ-900 — Answer memory\n\n**Priority:** must\n\n" + b518Ans},
		critic:      []string{b518Approve, b518Approve},
		composition: []string{b518Approve},
	}
	p := b518Params(t, seats)
	p.SectionWise = true
	if _, err := runFragment(context.Background(), p, base, request); err != nil {
		t.Fatal(err)
	}
	if checks := seats.eventsOf("structure_check"); len(checks) != 0 {
		t.Fatalf("a requested split was flagged as a removal: %+v", checks)
	}
}

// Engine-owned provenance and mechanical metadata are not prose: an
// Originates-from link alone is not a touched section, and a replaced
// Verification or Produces value is not removed content.
func TestB518MetadataIsNotRemovedContent(t *testing.T) {
	base, _, _ := b518Fixture(t)
	linked, _ := artifact.Parse(artifact.RenderBody(base), artifact.KindRequirements)
	sec := linked.Section("REQ-001")
	sec.Body = strings.Replace(sec.Body, "**Originates from:** INT-001", "**Originates from:** INT-001, INT-003", 1)
	if changes := amendmentChanges(base, linked, ""); len(changes) != 0 {
		t.Fatalf("a provenance link counted as an edit: %+v", changes)
	}

	plan := func(verification, produces string) *artifact.Document {
		doc, err := artifact.Parse("## M-001 — Core\n\n### T-001 — Transport\n\n**Implements:** SPEC-001\n**Produces:** "+produces+
			"\n**Verification:** "+verification+"\n**Work unit:** Transport\n\nThe service uses the approved socket transport layer.\n", artifact.KindPlan)
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	before := plan("go test ./internal/transport/... -run TestSocketTransport", "internal/transport/socket.go, internal/transport/socket_test.go")
	after := plan("make check-api", "internal/api/inproc.go")
	if report := removalReport(before, after, "Rewire the task.", "", nil); len(report.Findings) != 0 {
		t.Fatalf("mechanical metadata was judged as removed prose: %v", report.Findings)
	}
}

// Words every section uses carry no identity. Without discounting them, a
// deleted sentence "survives" in any sibling that shares the document's
// vocabulary.
func TestB518DocumentVocabularyDoesNotHideARemoval(t *testing.T) {
	sections := []string{
		"## REQ-001 — Keys\n\nThe calculator shall support the keypad keys.\n",
		"## REQ-002 — Memory\n\nThe calculator shall support memory registers.\n",
		"## REQ-003 — Modes\n\nThe calculator shall support angle modes.\n",
		"## REQ-004 — Display\n\nThe calculator shall support percent conversion on the calculator display panel.\n\nThe calculator shall support display of the panel brightness.\n",
	}
	base, err := artifact.Parse(strings.Join(sections, "\n"), artifact.KindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	candidate, _ := artifact.Parse(artifact.RenderBody(base), artifact.KindRequirements)
	sec := candidate.Section("REQ-004")
	sec.Body = "The calculator shall support display of the panel brightness."
	report := removalReport(base, candidate, "Adjust the display.", "", nil)
	if len(report.Findings) != 1 || !strings.Contains(report.Findings[0], "percent conversion") {
		t.Fatalf("findings = %v, want the percent-conversion sentence", report.Findings)
	}
}
