package stage

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/strategy"
)

// Amendment preservation (B-518).
//
// An amendment edits an approved document. The scheduler already shows a
// critic the candidate; this file supplies the other half — what each touched
// section said before — and a deterministic check that approved content did
// not disappear without the request asking for it.
//
// Granularity. A section body is cut into CONTENT UNITS: each list item
// (with its continuation lines), each sentence of prose, each fenced block.
// Not paragraphs: eett's architect kept REQ-008's first word ("The calculator
// shall support…") and its second paragraph while dropping a whole clause
// family, and REQ-009 lost one sentence of a two-sentence paragraph — a
// paragraph-level check needs the whole paragraph gone and misses both. Not
// words or lines: those flag every rewording. Field lines are units by their
// value, except engine-owned and mechanical metadata (Originates from,
// Priority, Implements, Owns, Produces, …), which structure checks own.
//
// Similarity. A unit is PRESERVED when some window of one or two consecutive
// units anywhere in the composed candidate contains at least
// preservedOverlap of the unit's distinctive tokens. Tokens are lowercase
// words of three or more letters, minus stopwords, cut to a five-rune prefix
// (fraction/fractional/fractions, evaluate/evaluation, convert/conversion
// meet). Words used by most sections of the approved document ("calculator"
// in a calculator's requirements) carry no identity and are dropped. The
// window of two tolerates a sentence split in two; searching the whole
// candidate tolerates text moved to another section; the token bag tolerates
// reordering and synonyms up to 40% of the unit. Units with fewer than
// minUnitTokens distinctive tokens are not judged: too short to tell a
// rewording from a removal, and a false positive costs an architect turn.
//
// Authorization. A removed unit is authorized when one or two consecutive
// sentences of the request both name a removal (remove, delete, replace,
// restate, …) and cover at least requestOverlap of the unit's tokens — the
// request talks about THAT content and asks for it to go. Anything else must
// be restored, or justified by quoting the request verbatim, which the
// critic then judges.

const (
	preservedOverlap = 0.6
	requestOverlap   = 0.5
	minUnitTokens    = 3
	stemRunes        = 5
)

var unitStopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`the and for are but not you all any can her was one our out has have had
		his how its may new now old see two way who did get let put say she too use that this with from into
		onto than then they them their there these those what when where which while will would shall should
		could must also each every other such only some more most less very just been being were does done
		upon about above below over under between after before during within without through via per
		its it's your yours ours here both either neither nor yet so if else whether because since unless
		until than whose whom who's let's must not none`) {
		unitStopwords[w] = true
	}
}

// metadataFields are owned by the engine or by mechanical checks; a changed
// value there is not a removal of approved prose.
var metadataFields = map[string]bool{
	"originates from": true, "priority": true, "implements": true, "depends on": true,
	"owns": true, "produces": true, "consumes": true, "modifies": true, "exercises": true,
	"milestone": true, "toolchain": true, "verification": true, "as-built": true, "delete": true,
	"status": true, "superseded by": true,
}

var (
	fieldLineRe = regexp.MustCompile(`^\s*\*\*([^*:]+):\*\*\s*(.*)$`)
	listItemRe  = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+(.*)$`)
	headingRe   = regexp.MustCompile(`^\s*#{1,6}\s`)
	sentenceEnd = regexp.MustCompile(`[.!?;]["')\]]*\s+`)
)

// removalCues are the request words that ask for content to go or be
// replaced. Matched as word prefixes, in English and Spanish.
var removalCues = []string{
	"remov", "delet", "drop", "elimin", "omit", "strip", "retir", "deprecat", "replac",
	"instead", "rather", "restat", "rewrit", "reword", "narrow", "exclud", "no longer",
	"not required", "out of scope", "get rid", "cut ", "quitar", "quita ", "borr", "suprim", "sacar",
	"reemplaz", "sustitu", "ya no",
}

type contentUnit struct {
	text   string
	tokens []string
}

func unitTokens(text string, common map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if utf8.RuneCountInString(word) < 3 || unitStopwords[word] {
			continue
		}
		stem := word
		if utf8.RuneCountInString(stem) > stemRunes {
			stem = string([]rune(stem)[:stemRunes])
		}
		if common[stem] || seen[stem] {
			continue
		}
		seen[stem] = true
		out = append(out, stem)
	}
	return out
}

// contentUnits cuts a section body into the units described above.
func contentUnits(body string, common map[string]bool) []contentUnit {
	var texts []string
	var prose []string
	var item []string
	flushProse := func() {
		if len(prose) > 0 {
			texts = append(texts, splitSentences(strings.Join(prose, " "))...)
			prose = nil
		}
	}
	flushItem := func() {
		if len(item) > 0 {
			texts = append(texts, strings.Join(item, " "))
			item = nil
		}
	}
	inFence := false
	var fence []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				texts = append(texts, strings.Join(fence, "\n"))
				fence = nil
			} else {
				flushProse()
				flushItem()
			}
			inFence = !inFence
			continue
		}
		if inFence {
			fence = append(fence, line)
			continue
		}
		if trimmed == "" || headingRe.MatchString(line) {
			flushProse()
			flushItem()
			continue
		}
		if m := fieldLineRe.FindStringSubmatch(line); m != nil {
			flushProse()
			flushItem()
			if metadataFields[strings.ToLower(strings.TrimSpace(m[1]))] {
				continue
			}
			if value := strings.TrimSpace(m[2]); value != "" {
				prose = append(prose, value)
			}
			continue
		}
		if m := listItemRe.FindStringSubmatch(line); m != nil {
			flushProse()
			flushItem()
			item = append(item, strings.TrimSpace(m[1]))
			continue
		}
		if len(item) > 0 {
			item = append(item, trimmed) // a continuation of the item
			continue
		}
		prose = append(prose, trimmed)
	}
	flushProse()
	flushItem()
	if len(fence) > 0 {
		texts = append(texts, strings.Join(fence, "\n"))
	}
	units := make([]contentUnit, 0, len(texts))
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		units = append(units, contentUnit{text: text, tokens: unitTokens(text, common)})
	}
	return units
}

// splitSentences cuts prose at sentence punctuation followed by space. A
// semicolon counts: requirement prose chains independent obligations with it.
func splitSentences(text string) []string {
	var out []string
	last := 0
	for _, loc := range sentenceEnd.FindAllStringIndex(text, -1) {
		out = append(out, strings.TrimSpace(text[last:loc[1]]))
		last = loc[1]
	}
	if rest := strings.TrimSpace(text[last:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

// covered reports the share of want's tokens present in have.
func covered(want []string, have map[string]bool) float64 {
	if len(want) == 0 {
		return 1
	}
	n := 0
	for _, token := range want {
		if have[token] {
			n++
		}
	}
	return float64(n) / float64(len(want))
}

// windowBags returns the token sets of every one- and two-unit window.
func windowBags(units []contentUnit) []map[string]bool {
	var bags []map[string]bool
	for i := range units {
		one := map[string]bool{}
		for _, t := range units[i].tokens {
			one[t] = true
		}
		bags = append(bags, one)
		if i+1 < len(units) {
			two := map[string]bool{}
			for t := range one {
				two[t] = true
			}
			for _, t := range units[i+1].tokens {
				two[t] = true
			}
			bags = append(bags, two)
		}
	}
	return bags
}

func bestCover(want []string, bags []map[string]bool) float64 {
	best := 0.0
	for _, bag := range bags {
		if c := covered(want, bag); c > best {
			best = c
		}
	}
	return best
}

func hasRemovalCue(text string) bool {
	lower := " " + strings.ToLower(text)
	for _, cue := range removalCues {
		if strings.Contains(lower, " "+cue) {
			return true
		}
	}
	return false
}

// flatSections lists every section and task of a document, in order.
func flatSections(doc *artifact.Document) []artifact.Section {
	if doc == nil {
		return nil
	}
	var out []artifact.Section
	for _, sec := range doc.Sections {
		out = append(out, sec)
		out = append(out, sec.Children...)
	}
	return out
}

var originatesLineRe = regexp.MustCompile(`(?mi)^\s*\*\*originates from:\*\*.*$\n?`)

// sameSection ignores engine-owned provenance: intake links every changed
// section to the run's intent, and that edge is not an author's edit.
func sameSection(a, b artifact.Section) bool {
	norm := func(s string) string {
		return strings.Join(strings.Fields(originatesLineRe.ReplaceAllString(s, "")), " ")
	}
	return strings.EqualFold(strings.TrimSpace(a.Title), strings.TrimSpace(b.Title)) && norm(a.Body) == norm(b.Body)
}

// renderSectionMarkdown is a section as a reply would carry it.
func renderSectionMarkdown(sec artifact.Section) string {
	return "## " + renderSection(sec)
}

func renderSection(sec artifact.Section) string {
	return strings.TrimSpace(fmt.Sprintf("%s — %s\n\n%s", sec.ID, sec.Title, strings.TrimSpace(sec.Body)))
}

// sectionChange is one touched section of a composed candidate.
type sectionChange struct {
	ID     string
	Before *artifact.Section // nil: added
	After  *artifact.Section // nil: deleted
}

// amendmentChanges lists the sections a candidate adds, changes or deletes
// relative to the approved base, in base order then candidate order.
func amendmentChanges(base, candidate *artifact.Document, only string) []sectionChange {
	all := allAmendmentChanges(base, candidate)
	if only == "" {
		return all
	}
	var out []sectionChange
	for _, change := range all {
		if strings.EqualFold(change.ID, only) {
			out = append(out, change)
		}
	}
	return out
}

func allAmendmentChanges(base, candidate *artifact.Document) []sectionChange {
	after := map[string]artifact.Section{}
	var order []string
	for _, sec := range flatSections(candidate) {
		key := strings.ToUpper(sec.ID)
		if _, dup := after[key]; !dup {
			order = append(order, key)
		}
		after[key] = sec
	}
	seen := map[string]bool{}
	var out []sectionChange
	for _, sec := range flatSections(base) {
		key := strings.ToUpper(sec.ID)
		seen[key] = true
		before := sec
		next, ok := after[key]
		if !ok {
			out = append(out, sectionChange{ID: sec.ID, Before: &before})
			continue
		}
		if !sameSection(before, next) {
			out = append(out, sectionChange{ID: sec.ID, Before: &before, After: &next})
		}
	}
	for _, key := range order {
		if seen[key] {
			continue
		}
		next := after[key]
		out = append(out, sectionChange{ID: next.ID, After: &next})
	}
	return out
}

// commonTokens are the stems used by more than half of the approved
// document's sections (with at least four sections to judge from).
func commonTokens(base *artifact.Document) map[string]bool {
	sections := flatSections(base)
	if len(sections) < 4 {
		return nil
	}
	df := map[string]int{}
	for _, sec := range sections {
		seen := map[string]bool{}
		for _, unit := range contentUnits(sec.Title+".\n\n"+sec.Body, nil) {
			for _, t := range unit.tokens {
				if !seen[t] {
					seen[t] = true
					df[t]++
				}
			}
		}
	}
	common := map[string]bool{}
	for t, n := range df {
		if n*2 > len(sections) {
			common[t] = true
		}
	}
	return common
}

// removedUnit is approved content absent from the composed candidate.
type removedUnit struct {
	Section    string
	Text       string
	Authorized bool // the request asks for this content to go
}

// removedContent runs the preservation rule over every touched section.
func removedContent(base, candidate *artifact.Document, request, only string) []removedUnit {
	common := commonTokens(base)
	var scope []contentUnit
	for _, sec := range flatSections(candidate) {
		scope = append(scope, contentUnits(sec.Title+".\n\n"+sec.Body, common)...)
	}
	bags := windowBags(scope)
	var requestUnits []contentUnit
	for _, sentence := range splitSentences(strings.Join(strings.Fields(request), " ")) {
		requestUnits = append(requestUnits, contentUnit{text: sentence, tokens: unitTokens(sentence, common)})
	}
	var out []removedUnit
	for _, change := range amendmentChanges(base, candidate, only) {
		if change.Before == nil {
			continue
		}
		for _, unit := range contentUnits(change.Before.Body, common) {
			if len(unit.tokens) < minUnitTokens || bestCover(unit.tokens, bags) >= preservedOverlap {
				continue
			}
			out = append(out, removedUnit{
				Section: change.Before.ID, Text: unit.text,
				Authorized: requestAuthorizes(unit.tokens, requestUnits),
			})
		}
	}
	return out
}

func requestAuthorizes(tokens []string, request []contentUnit) bool {
	for i := range request {
		for width := 1; width <= 2 && i+width <= len(request); width++ {
			var text strings.Builder
			bag := map[string]bool{}
			for _, unit := range request[i : i+width] {
				text.WriteString(unit.text + " ")
				for _, t := range unit.tokens {
					bag[t] = true
				}
			}
			if hasRemovalCue(text.String()) && covered(tokens, bag) >= requestOverlap {
				return true
			}
		}
	}
	return false
}

func normalizeQuote(s string) string {
	s = strings.NewReplacer("“", "\"", "”", "\"", "’", "'", "‘", "'", "`", "").Replace(strings.ToLower(s))
	return strings.Join(strings.Fields(s), " ")
}

// verifiedJustification returns the section's justification whose quote is
// verbatim from the request and long enough to say something.
func verifiedJustification(section, request string, justified []strategy.RemovalJustification) (string, bool) {
	normalized := normalizeQuote(request)
	for _, j := range justified {
		if !strings.EqualFold(j.Section, section) {
			continue
		}
		quote := normalizeQuote(j.Quote)
		if len(strings.Fields(quote)) >= 3 && strings.Contains(normalized, quote) {
			return j.Quote, true
		}
	}
	return "", false
}

func clipUnit(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) > 400 {
		return string([]rune(text)[:400]) + "…"
	}
	return text
}

// removalReport is the deterministic check the scheduler runs before review.
func removalReport(base, candidate *artifact.Document, request, only string, justified []strategy.RemovalJustification) strategy.RemovalReport {
	var report strategy.RemovalReport
	for _, unit := range removedContent(base, candidate, request, only) {
		if unit.Authorized {
			continue
		}
		passage := fmt.Sprintf("%s: approved content absent from the candidate: %q", unit.Section, clipUnit(unit.Text))
		if quote, ok := verifiedJustification(unit.Section, request, justified); ok {
			report.Justified = append(report.Justified, fmt.Sprintf("%s — justified by the request's words %q", passage, quote))
			continue
		}
		report.Findings = append(report.Findings, passage)
	}
	return report
}

// amendmentDelta renders the before/after of every touched section for a
// critic, with the engine's view of what went missing.
func amendmentDelta(base, candidate *artifact.Document, request, only string) string {
	changes := amendmentChanges(base, candidate, only)
	if len(changes) == 0 {
		return ""
	}
	removed := map[string][]string{}
	for _, unit := range removedContent(base, candidate, request, only) {
		key := strings.ToUpper(unit.Section)
		note := clipUnit(unit.Text)
		if unit.Authorized {
			note += " (the request appears to ask for this)"
		}
		removed[key] = append(removed[key], note)
	}
	var b strings.Builder
	b.WriteString("## Amendment delta — approved text vs candidate (engine-computed)\n\n")
	b.WriteString("The approved document this amendment edits is not in the candidate above. For EVERY section the candidate touches, " +
		"its approved previous text and its candidate text follow. Check every removal against the human request: a sentence, list item " +
		"or paragraph present before and absent now is a defect unless the request explicitly asks for that removal or it directly " +
		"contradicts the requested change. Rewording that keeps the meaning, or text moved to another section, is not a removal.\n")
	for _, change := range changes {
		state := "changed"
		switch {
		case change.Before == nil:
			state = "added — no previous text"
		case change.After == nil:
			state = "deleted"
		}
		fmt.Fprintf(&b, "\n### %s — %s\n", change.ID, state)
		if change.Before != nil {
			b.WriteString("\nApproved previous text:\n\n```markdown\n" + renderSection(*change.Before) + "\n```\n")
		}
		if change.After != nil && change.Before != nil {
			b.WriteString("\nCandidate text:\n\n```markdown\n" + renderSection(*change.After) + "\n```\n")
		}
		if gone := removed[strings.ToUpper(change.ID)]; len(gone) > 0 {
			b.WriteString("\nApproved content the engine could not find anywhere in the candidate:\n\n")
			for _, g := range gone {
				b.WriteString("- " + g + "\n")
			}
		}
	}
	return b.String()
}

// newAmendmentGuard wires a stage's composition into the scheduler hooks.
// compose turns whatever the scheduler holds — a fragment fold, a section
// reply, a full candidate — into the document the amendment would persist.
// checkRemovals false keeps the critic's delta but skips the deterministic
// check, for a route where the request is not the authority over removals.
func newAmendmentGuard(base *artifact.Document, request string, compose func(string) (*artifact.Document, error), checkRemovals bool) *strategy.AmendmentGuard {
	return newScopedAmendmentGuard(base, request, compose, checkRemovals, "")
}

// newScopedAmendmentGuard limits the delta and the check to one section: an
// isolated section pass's critic is told never to mention another id, and
// sibling passes own their own sections.
func newScopedAmendmentGuard(base *artifact.Document, request string, compose func(string) (*artifact.Document, error), checkRemovals bool, only string) *strategy.AmendmentGuard {
	if base == nil || len(base.Sections) == 0 || compose == nil {
		return nil
	}
	guard := &strategy.AmendmentGuard{
		Delta: func(text string) string {
			candidate, err := compose(text)
			if err != nil || candidate == nil {
				return ""
			}
			return amendmentDelta(base, candidate, request, only)
		},
	}
	if checkRemovals {
		guard.Removals = func(text string, justified []strategy.RemovalJustification) strategy.RemovalReport {
			candidate, err := compose(text)
			if err != nil || candidate == nil {
				return strategy.RemovalReport{}
			}
			return removalReport(base, candidate, request, only, justified)
		}
	}
	return guard
}

// sectionIDsInFindings names the existing sections a review's findings point
// at, for a repair that can only revisit named sections.
func sectionIDsInFindings(doc *artifact.Document, texts []string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, text := range texts {
		for _, m := range anySectionIDRe.FindAllString(strings.ToUpper(text), -1) {
			if seen[m] || findSection(doc, m) == nil {
				continue
			}
			seen[m] = true
			ids = append(ids, m)
		}
	}
	return ids
}

var anySectionIDRe = regexp.MustCompile(`\b[A-Z]+-\d+\b`)

// sectionGuard is the amendment guard of one section-wise pass. The pass's
// critic reads the raw section reply; compose places it into the current
// composition so moved text is still found, and the delta and check are
// limited to that section. A NEW-section pass has no approved text to
// compare and gets none.
func sectionGuard(base, proposed *artifact.Document, kind artifact.Kind, id, request string, checkRemovals bool) *strategy.AmendmentGuard {
	if id == "" || findSection(base, id) == nil {
		return nil
	}
	compose := func(text string) (*artifact.Document, error) {
		doc, err := artifact.Parse(artifact.RenderBody(proposed), kind)
		if err != nil {
			return nil, err
		}
		repl, ok := parseSectionReply(text, kind, id)
		if !ok {
			return nil, fmt.Errorf("section %s: no replacement in the reply", id)
		}
		sec := findSection(doc, id)
		if sec == nil {
			return nil, fmt.Errorf("section %s is not in the composition", id)
		}
		repl.ID = sec.ID
		if kind == artifact.KindPlan {
			repl.Body = stripMilestoneField(repl.Body)
			repl.Children = sec.Children
		}
		*sec = repl
		return doc, nil
	}
	return newScopedAmendmentGuard(base, request, compose, checkRemovals, id)
}
