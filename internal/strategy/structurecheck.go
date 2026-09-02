package strategy

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
)

var ErrStructureFailed = errors.New("document structure did not converge")
var ErrStructureRepairScope = errors.New("structure repair changed sections outside its assignment")

const (
	maxStructureAttempts   = 12
	maxRepairFindings      = 12
	maxStructureStagnation = 3
	maxRepairSections      = 1
	maxIndependentSections = 4
)

var requirementPriorityToken = regexp.MustCompile(`(?i)\*\*Priority:\*\*\s*(must|should|could|wont)\.?`)

// A document council's last architect turn is the one nobody reviews: after
// the critics speak, the revision goes straight to the gate. The plan that
// reached Neocapture's gate on 2026-08-29 had lost every Implements: line
// its previous draft carried, and a person had to send it back. Structure is
// deterministic; the harness checks it and returns the defects to the
// architect as an error that teaches, before spending a human turn.

// structureFindings lists what a revised draft lost or broke against the
// draft before it and against the contract of its document kind. Empty
// means the draft is structurally sound.
func structureFindings(prev, cur []agent.Section, contract string, known map[string]bool, small bool, raw string) []string {
	var out []string
	prefix := strings.TrimPrefix(contract, "markdown_sections:")
	needsImplements := prefix == "SPEC" || prefix == "M" || prefix == "T"
	isPlan := prefix == "M" || prefix == "T"

	// Plan rules live at task granularity: every task names what it
	// implements; a small seat's task carries at most three top-level
	// deliverables (the brief asks; the check enforces — T-001 arrived with
	// twelve, benchmark run 4); milestone lanes never overlap.
	if isPlan {
		var blocks []taskBlock
		for _, s := range cur {
			sectionBlocks := taskBlocks(s.Body)
			// A sectioned plan update uses markdown_sections:T, so its assigned
			// task is itself the parsed top-level section rather than an H3 inside
			// a milestone. Treat it as the task contract; otherwise Work unit and
			// Acceptance slices silently escape every deterministic check.
			if strings.HasPrefix(strings.ToUpper(s.ID), "T-") {
				sectionBlocks = []taskBlock{{id: strings.ToUpper(s.ID), body: s.Body}}
			}
			blocks = append(blocks, sectionBlocks...)
			for _, block := range sectionBlocks {
				if !strings.HasPrefix(block.id, "T-") {
					continue
				}
				implementsValue := strings.TrimSpace(markdownFieldValue(block.body, "Implements"))
				if implementsValue == "" {
					out = append(out, fmt.Sprintf("%s has no **Implements:** line", block.id))
				} else if strings.EqualFold(implementsValue, "none") {
					out = append(out, fmt.Sprintf("%s **Implements:** cannot be none — every task must deliver at least one accepted specification section", block.id))
				}
				if small {
					if strings.TrimSpace(markdownFieldValue(block.body, "Work unit")) == "" {
						out = append(out, fmt.Sprintf("%s has no **Work unit:** — name exactly one cohesive capability or concern; split independent concerns into separate tasks", block.id))
					}
					if strings.Contains(strings.ToLower(block.body), "**deliverables:**") {
						out = append(out, fmt.Sprintf("%s uses legacy **Deliverables:** — regenerate it with **Work unit:** and **Acceptance slices:** so outcomes are distinct from explanation", block.id))
					}
					n := topLevelChecklistItems(block.body, "Acceptance slices")
					if n == 0 {
						out = append(out, fmt.Sprintf("%s has no top-level **Acceptance slices:** bullets — name observable outcomes of its single Work unit", block.id))
					} else if n > 3 {
						out = append(out, fmt.Sprintf("%s has %d top-level **Acceptance slices:** bullets; a small implementer takes at most 3 — split the task", block.id, n))
					}
					if !strings.Contains(strings.ToLower(block.body), "**verification:**") {
						out = append(out, fmt.Sprintf("%s has no **Verification:** line — name the command or deterministic check that exercises this task's changed artifacts; a green project build that ignores them is not verification", block.id))
					} else if taskVerificationCommand(block.body) == "" {
						out = append(out, fmt.Sprintf("%s **Verification:** must put the executable command in backticks; prose is never executed", block.id))
					} else if invalidSingleOutputCompile(taskVerificationCommand(block.body)) {
						out = append(out, fmt.Sprintf("%s **Verification:** uses `-c` with multiple input files and one `-o`; GCC/Clang reject that command before compiling — compile one translation unit or omit the single output", block.id))
					}
					if !taskHasField(block.body, "Consumes") {
						out = append(out, fmt.Sprintf("%s has no **Consumes:** line — name prerequisite artifacts/capabilities, or write none", block.id))
					}
					produces := taskFieldItems(block.body, "Produces")
					exercises := taskFieldItems(block.body, "Exercises")
					if len(produces) == 0 {
						out = append(out, fmt.Sprintf("%s has no **Produces:** artifacts — name the paths, build targets, or capabilities this task creates", block.id))
					}
					if len(exercises) == 0 {
						out = append(out, fmt.Sprintf("%s has no **Exercises:** artifacts — name which Produced artifacts its Verification actually exercises", block.id))
					} else if len(produces) > 0 && !itemsOverlap(produces, exercises) {
						out = append(out, fmt.Sprintf("%s **Exercises:** none of its **Produces:** artifacts — its verification can be green without checking this task's delta", block.id))
					}
				}
			}
		}
		if small {
			out = append(out, taskGraphFindings(blocks)...)
		}
		if raw != "" {
			if doc, err := artifact.Parse(raw, artifact.KindPlan); err == nil {
				seenPair := map[string]bool{}
				for _, e := range artifact.LaneCollisions(doc) {
					key := e.ID + "|" + e.Detail
					if !seenPair[key] {
						seenPair[key] = true
						out = append(out, fmt.Sprintf("%s: lane collision — %s; make the **Owns:** lanes disjoint or drop them", e.ID, e.Detail))
					}
				}
			}
		}
	}
	implementsLine := regexp.MustCompile(`(?im)^\*\*Implements:\*\*\s*(.+)$`)
	idToken := regexp.MustCompile(`[A-Z]+-\d+`)

	seen := map[string]bool{}
	for _, s := range cur {
		if prefix == "REQ" {
			if n := priorityMarkerCount(s.Body); n > 1 {
				out = append(out, fmt.Sprintf("%s has %d **Priority:** markers — one requirement section represents one independently traceable decision and has exactly one Priority", s.ID, n))
			}
			priority := strings.ToLower(strings.TrimSpace(markdownFieldValue(s.Body, "Priority")))
			if priority == "" {
				out = append(out, fmt.Sprintf("%s has no **Priority:** line — use exactly must, should, could, or wont", s.ID))
			} else if !slices.Contains([]string{"must", "should", "could", "wont"}, priority) {
				shown := priority
				out = append(out, fmt.Sprintf("%s has invalid **Priority:** %s — use exactly must, should, could, or wont", s.ID, shown))
			}
			if priority == "must" && permissiveOnlyRequirement(s.Body) {
				out = append(out, fmt.Sprintf("%s says the capability is optional (`may` or `not required`) but retains **Priority:** must — make the body and Priority agree", s.ID))
			}
			if priority == "wont" && genericExclusionTitle(s.Title) {
				if n := topLevelBulletCount(s.Body); n > 1 {
					out = append(out, fmt.Sprintf("%s is a generic exclusion catch-all with %d independent bullets — split each excluded behavior into its own requirement so a later decision can transform it without changing unrelated exclusions", s.ID, n))
				}
			}
		}
		// Every Implements: target must exist in the project's documents;
		// an id that is not there is a dangling reference the spine will
		// report, and a task built against it has no contract.
		if len(known) > 0 {
			for _, m := range implementsLine.FindAllStringSubmatch(s.Body, -1) {
				for _, id := range idToken.FindAllString(m[1], -1) {
					if !known[id] {
						out = append(out, fmt.Sprintf("%s implements %s, which is not a section of any project document — name an id that exists, or drop it", s.ID, id))
					}
				}
			}
		}
		if seen[s.ID] {
			out = append(out, fmt.Sprintf("%s appears twice", s.ID))
		}
		seen[s.ID] = true
		if prefix != "T" && needsImplements && (strings.HasPrefix(s.ID, "SPEC-") || strings.HasPrefix(s.ID, "T-")) &&
			!strings.Contains(strings.ToLower(s.Body), "**implements:**") {
			out = append(out, fmt.Sprintf("%s has no **Implements:** line", s.ID))
		}
		// One Deliverables heading per TASK. A plan's parsed sections are
		// milestones whose bodies hold their tasks as H3 blocks — counted
		// per milestone, every plan was a false positive and the architect
		// was sent back four times for nothing (benchmark run 2).
		for _, block := range taskBlocks(s.Body) {
			if n := strings.Count(block.body, "**Deliverables:**"); n > 1 {
				out = append(out, fmt.Sprintf("%s has %d **Deliverables:** headings; one per task", block.id, n))
			}
		}
	}
	// Sub-numbered ids are invisible to the spine: a requirements draft with
	// `### REQ-003.1 …` sub-sections sent the spec reviewer on 21 searches for
	// ids that were never sections (Neocapture, 2026-08-30).
	if prefix != "" {
		subID := regexp.MustCompile(`(?m)^#{2,4}\s+` + regexp.QuoteMeta(prefix) + `-\d+[.\-]\d+\b`)
		for _, s := range cur {
			if m := subID.FindString(s.Body); m != "" {
				out = append(out, fmt.Sprintf("%s contains a sub-numbered heading (%q): sub-numbered ids are not sections and their traceability is lost — give the item its own %s-NNN H2 id, or fold it into the section's body as bullets", s.ID, strings.TrimSpace(m), prefix))
			}
		}
		// A visually titled item is still invisible when it is bold text or a
		// list item under a generic H2. Corrida 27 put REQ-013..018 under
		// "## Out of Scope" as `**REQ-013 — File saving**`; the reviewer saw
		// six requirements while the parser and traceability spine saw none.
		// Only flag title-shaped declarations whose id is absent from the
		// parsed sections, so ordinary references to existing ids remain valid.
		misplaced := regexp.MustCompile(`(?m)^[ \t]*(?:[-*+]\s+)?\*{0,2}(` + regexp.QuoteMeta(prefix) + `-\d+)\s+[—-]\s+`)
		misplacedSeen := map[string]bool{}
		for _, match := range misplaced.FindAllStringSubmatch(raw, -1) {
			id := strings.ToUpper(match[1])
			if seen[id] || misplacedSeen[id] {
				continue
			}
			misplacedSeen[id] = true
			out = append(out, fmt.Sprintf("%s is written as inline/list text instead of an H2 section — emit `## %s — Title`; otherwise the parser and traceability spine cannot see it", id, id))
		}
	}
	for _, p := range prev {
		if !seen[p.ID] {
			out = append(out, fmt.Sprintf("%s (%s) was in your previous draft and is gone — restore it, or state in it why it no longer applies (Priority: wont)", p.ID, p.Title))
		}
	}
	return out
}

// scopeArchitectSection discards sibling sections emitted during an isolated
// section pass before they can enter structure repair or reviewer context.
// The stage owns routing; a model reply cannot expand that assignment.
func scopeArchitectSection(outcome *agent.Outcome, contract, expectedID, expectedTitle string) (*agent.Outcome, error) {
	if outcome == nil || !strings.HasPrefix(contract, "markdown_sections:") {
		return outcome, nil
	}
	sections := sectionsOf(outcome)
	for _, sec := range sections {
		if !strings.EqualFold(sec.ID, expectedID) {
			continue
		}
		return rewriteScopedArchitectSection(outcome, contract, sec, expectedID)
	}
	if wanted := normalizedSectionTitle(expectedTitle); wanted != "" {
		var matched *agent.Section
		for i := range sections {
			if normalizedSectionTitle(sections[i].Title) != wanted {
				continue
			}
			if matched != nil {
				return nil, fmt.Errorf("isolated architect pass returned multiple sections titled %q", expectedTitle)
			}
			matched = &sections[i]
		}
		if matched != nil {
			return rewriteScopedArchitectSection(outcome, contract, *matched, expectedID)
		}
	}
	if len(sections) == 1 {
		// The section ID is an engine-owned routing coordinate. A reviewer may
		// mistakenly ask the architect to renumber an existing task; preserve
		// the one returned semantic replacement under its assigned ID instead
		// of turning that recoverable protocol error into a failed run.
		return rewriteScopedArchitectSection(outcome, contract, sections[0], expectedID)
	}
	return nil, fmt.Errorf("isolated architect pass returned no section %s", expectedID)
}

func normalizedSectionTitle(title string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.NewReplacer("&", " and ", "-", " ", "_", " ").Replace(title))), " ")
}

func rewriteScopedArchitectSection(outcome *agent.Outcome, contract string, sec agent.Section, expectedID string) (*agent.Outcome, error) {
	text := "## " + expectedID + " — " + strings.TrimSpace(sec.Title) + "\n\n" + strings.TrimSpace(sec.Body)
	parsed, err := agent.ParseContract(contract, text)
	if err != nil {
		return nil, err
	}
	scoped := *outcome
	scoped.Text, scoped.Parsed = text, parsed
	return &scoped, nil
}

// normalizeRequirementPriorities compiles a mechanically unambiguous Priority
// into one canonical field. Small seats frequently put the marker at the end
// of a prose line; set_field then added a second marker because Markdown fields
// are line-oriented, turning a trivial formatting issue into a repair loop.
func normalizeRequirementPriorities(outcome *agent.Outcome, contract string) (*agent.Outcome, int, error) {
	if outcome == nil || contract != "markdown_sections:REQ" {
		return outcome, 0, nil
	}
	sections := sectionsOf(outcome)
	if len(sections) == 0 {
		return outcome, 0, nil
	}
	changed := 0
	parts := make([]string, 0, len(sections)+1)
	if preamble := documentPreamble(outcome.Text); preamble != "" {
		parts = append(parts, preamble)
	}
	for _, section := range sections {
		body := strings.TrimSpace(section.Body)
		matches := requirementPriorityToken.FindAllStringSubmatch(body, -1)
		priority := ""
		consistent := true
		for _, match := range matches {
			value := strings.ToLower(match[1])
			if priority != "" && priority != value {
				consistent = false
			}
			priority = value
		}
		withoutMarkers := strings.TrimSpace(requirementPriorityToken.ReplaceAllString(body, ""))
		desired := inferredRequirementPriority(section.Title, withoutMarkers)
		if desired == "" && consistent {
			desired = priority
		}
		canonical := len(matches) == 1 && regexp.MustCompile(`(?im)^\*\*Priority:\*\*\s*`+regexp.QuoteMeta(priority)+`\s*$`).MatchString(body)
		if desired != "" && consistent && (!canonical || priority != desired) {
			body = "**Priority:** " + desired
			if withoutMarkers != "" {
				body += "\n" + strings.TrimSpace(withoutMarkers)
			}
			changed++
		}
		parts = append(parts, "## "+section.ID+" — "+strings.TrimSpace(section.Title)+"\n\n"+strings.TrimSpace(body))
	}
	if changed == 0 {
		return outcome, 0, nil
	}
	text := strings.Join(parts, "\n\n")
	parsed, err := agent.ParseContract(contract, text)
	if err != nil {
		return outcome, 0, err
	}
	normalized := *outcome
	normalized.Text, normalized.Parsed = text, parsed
	return &normalized, changed, nil
}

func inferredRequirementPriority(title, body string) string {
	if genericExclusionTitle(title) || strings.Contains(strings.ToLower(title), "out of scope:") {
		return "wont"
	}
	prose := strings.ToLower(body)
	prose = regexp.MustCompile(`\bnot\s+required\b`).ReplaceAllString(prose, "")
	if regexp.MustCompile(`\bshall\b|\bmust\b|\brequired\b`).MatchString(prose) {
		return "must"
	}
	return ""
}

func genericExclusionTitle(title string) bool {
	title = strings.ToLower(strings.TrimSpace(title))
	if strings.Contains(title, ":") {
		// A qualified title such as "Out of Scope: File Saving" names one
		// behavior and is not a catch-all merely because its decision is wont.
		return false
	}
	return strings.Contains(title, "out of scope") || strings.Contains(title, "exclusions")
}

func topLevelBulletCount(body string) int {
	n := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			n++
		}
	}
	return n
}

func permissiveOnlyRequirement(body string) bool {
	prose := strings.ToLower(body)
	prose = regexp.MustCompile(`(?im)^\s*\*\*priority:\*\*.*$`).ReplaceAllString(prose, "")
	permissive := regexp.MustCompile(`\bmay\b|\bnot\s+required\b`).MatchString(prose)
	if !permissive {
		return false
	}
	// Do not count "not required" itself as a strong normative marker.
	prose = regexp.MustCompile(`\bnot\s+required\b`).ReplaceAllString(prose, "")
	return !regexp.MustCompile(`\bshall\b|\bmust\b|\brequired\b`).MatchString(prose)
}

// ProposalStructureFindings validates the complete, materialized document
// that will be offered at the gate. Council structure checks see the author's
// wire response; during an amendment that response may contain only a subset
// of changed sections. A defect introduced in an earlier fragment therefore
// has to be checked again after all fragments and tombstones are merged.
func ProposalStructureFindings(doc *artifact.Document) []string {
	if doc == nil {
		return nil
	}
	isRequirements := doc.Front.Kind == artifact.KindRequirements
	if doc.Front.Kind == "" && len(doc.Sections) > 0 {
		isRequirements = strings.HasPrefix(doc.Sections[0].ID, "REQ-")
	}
	if !isRequirements {
		if doc.Front.Kind == artifact.KindPlan || (doc.Front.Kind == "" && len(doc.Sections) > 0 && strings.HasPrefix(doc.Sections[0].ID, "M-")) {
			var out []string
			for _, milestone := range doc.Sections {
				for _, task := range milestone.Children {
					if command := taskVerificationCommand(task.Body); invalidSingleOutputCompile(command) {
						out = append(out, fmt.Sprintf("%s **Verification:** uses `-c` with multiple input files and one `-o`; GCC/Clang reject that command before compiling — compile one translation unit or omit the single output", task.ID))
					}
				}
			}
			return out
		}
		return nil
	}
	var out []string
	for _, sec := range doc.Sections {
		if n := priorityMarkerCount(sec.Body); n > 1 {
			out = append(out, fmt.Sprintf("%s has %d **Priority:** markers — one requirement section represents one independently traceable decision and has exactly one Priority", sec.ID, n))
		}
		priority := strings.ToLower(strings.TrimSpace(sec.Field("priority")))
		if priority == "" {
			out = append(out, fmt.Sprintf("%s has no **Priority:** line — use exactly must, should, could, or wont", sec.ID))
		} else if !slices.Contains([]string{"must", "should", "could", "wont"}, priority) {
			out = append(out, fmt.Sprintf("%s has invalid **Priority:** %s — use exactly must, should, could, or wont", sec.ID, priority))
		}
		if priority == "must" && permissiveOnlyRequirement(sec.Body) {
			out = append(out, fmt.Sprintf("%s says the capability is optional (`may` or `not required`) but retains **Priority:** must — make the body and Priority agree", sec.ID))
		}
	}
	return out
}

func priorityMarkerCount(body string) int {
	return len(regexp.MustCompile(`(?i)\*\*priority:\*\*`).FindAllStringIndex(body, -1))
}

func markdownFieldValue(body, name string) string {
	re := regexp.MustCompile(`(?im)^\s*\*\*` + regexp.QuoteMeta(name) + `:\*\*\s*(.+)$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func taskFieldItems(body, name string) []string {
	re := regexp.MustCompile(`(?im)^\*\*` + regexp.QuoteMeta(name) + `:\*\*\s*(.+)$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	var out []string
	for _, item := range strings.Split(m[1], ",") {
		item = strings.TrimSpace(strings.Trim(item, "`"))
		if item != "" && item != "none" && item != "-" {
			out = append(out, item)
		}
	}
	return out
}

func taskHasField(body, name string) bool {
	re := regexp.MustCompile(`(?im)^\*\*` + regexp.QuoteMeta(name) + `:\*\*`)
	return re.MatchString(body)
}

func taskVerificationCommand(body string) string {
	re := regexp.MustCompile("(?im)^\\*\\*Verification:\\*\\*\\s*`([^`]+)`")
	m := re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func invalidSingleOutputCompile(command string) bool {
	fields := strings.Fields(command)
	hasCompile, hasOutput, inputs := false, false, 0
	for i, field := range fields {
		field = strings.Trim(field, "'\"")
		switch {
		case field == "-c":
			hasCompile = true
		case field == "-o" || strings.HasPrefix(field, "-o") && len(field) > 2:
			hasOutput = true
		case i > 0 && (strings.HasSuffix(field, ".c") || strings.HasSuffix(field, ".cc") || strings.HasSuffix(field, ".cpp") || strings.HasSuffix(field, ".h") || strings.HasSuffix(field, ".hpp")):
			inputs++
		}
	}
	return hasCompile && hasOutput && inputs > 1
}

func itemsOverlap(a, b []string) bool {
	seen := map[string]bool{}
	for _, item := range a {
		seen[item] = true
	}
	for _, item := range b {
		if seen[item] {
			return true
		}
	}
	return false
}

func taskGraphFindings(blocks []taskBlock) []string {
	producer := map[string]string{}
	var out []string
	for _, block := range blocks {
		if !strings.HasPrefix(block.id, "T-") {
			continue
		}
		for _, item := range taskFieldItems(block.body, "Produces") {
			if prior := producer[item]; prior != "" && prior != block.id {
				out = append(out, fmt.Sprintf("%s and %s both **Produce:** %s — one artifact needs one owner", prior, block.id, item))
			} else {
				producer[item] = block.id
			}
		}
	}
	for _, block := range blocks {
		if !strings.HasPrefix(block.id, "T-") {
			continue
		}
		deps := map[string]bool{}
		for _, id := range taskFieldItems(block.body, "Depends on") {
			deps[id] = true
		}
		for _, item := range taskFieldItems(block.body, "Consumes") {
			if p := producer[item]; p != "" && p != block.id && !deps[p] {
				out = append(out, fmt.Sprintf("%s consumes %s produced by %s but has no **Depends on:** %s", block.id, item, p, p))
			}
		}
	}
	return out
}

var orderedChecklistItem = regexp.MustCompile(`^[0-9]+[.)]\s+`)

// topLevelChecklistItems counts un-indented ordered or unordered items under
// a named bold checklist heading, up to the next field or Markdown heading.
// Numbering is presentation, not task complexity; rejecting `1.` while
// accepting `-` caused valid atomic slices to loop without semantic change.
func topLevelChecklistItems(body, label string) int {
	n := 0
	in := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimRight(line, " \t")
		switch {
		case strings.EqualFold(strings.TrimSpace(t), "**"+label+":**"):
			in = true
			continue
		case in && (strings.HasPrefix(strings.TrimSpace(t), "**") || strings.HasPrefix(t, "#")):
			in = false
		}
		if in && (strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || orderedChecklistItem.MatchString(t)) {
			n++
		}
	}
	return n
}

type taskBlock struct {
	id   string
	body string
}

// taskBlocks splits a milestone body into its H3 task blocks; a body with no
// tasks is one block under the section's own id.
func taskBlocks(body string) []taskBlock {
	lines := strings.Split(body, "\n")
	var out []taskBlock
	cur := taskBlock{id: "", body: ""}
	var b strings.Builder
	flush := func() {
		cur.body = b.String()
		out = append(out, cur)
		b.Reset()
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "### ") {
			if cur.id != "" || strings.TrimSpace(b.String()) != "" {
				flush()
			}
			id := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			if i := strings.Index(id, " —"); i > 0 {
				id = id[:i]
			}
			cur = taskBlock{id: strings.TrimSpace(id)}
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	for i := range out {
		if out[i].id == "" {
			out[i].id = "(section)"
		}
	}
	return out
}

// structureNote renders findings for the architect's retry prompt.
func structureNote(findings []string) string {
	var b strings.Builder
	b.WriteString("## Structure check — fix these before anything else\n\n")
	b.WriteString("ducklab checked your revision against the document rules and your previous draft. " +
		"It is not yet acceptable. Return the WHOLE document again with every item below repaired, " +
		"changing nothing else:\n\n")
	for _, f := range findings {
		b.WriteString("- " + f + "\n")
	}
	return b.String()
}

// structureRepairNote turns a large document rewrite into a bounded section
// repair. Neocapture's 25-task plan produced 52 findings; asking a 35B seat to
// return the whole plan for that flat list exhausted a 20k output cap. The
// harness owns the complete checkpoint, so the seat only needs to return the
// H2 section being repaired and the harness can merge it deterministically.
func structureRepairNote(findings []string, sections []agent.Section) string {
	note, _ := structureRepairInstruction(findings, sections)
	return note
}

func structureRepairInstruction(findings []string, sections []agent.Section) (string, []string) {
	batch, ids := structureRepairBatch(findings, sections)
	var b strings.Builder
	exampleID := "SECTION-ID"
	if len(ids) > 0 {
		exampleID = ids[0]
	}
	b.WriteString("## Structure check — return a bounded JSON patch\n\n")
	b.WriteString("ducklab has checkpointed the complete draft. Return ONLY one JSON object; do not repeat the document. Schema:\n\n")
	b.WriteString("```json\n{\"sections\":[\"" + exampleID + "\"],\"operations\":[{\"op\":\"set_field\",\"target\":\"" + exampleID + "\",\"field\":\"Implements\",\"value\":\"REQ-001\"}]}\n```\n\n")
	b.WriteString("Allowed operations are `set_field`, `remove_field`, `replace_block`, `append_block`, and `delete_block`. " +
		"Prefer `set_field` for every `**Field:**` correction; it does not depend on reproducing old Markdown byte-for-byte. " +
		"`replace_block` may replace an assigned H2 requirement/spec section or one existing H3 task, with `markdown` beginning with that same heading; plan milestones themselves are not replaceable. `append_block` targets the assigned H2 and adds one H3 task. " +
		"Every operation target must be listed below; an identifier merely mentioned in a finding is not necessarily writable.\n\n")
	for _, sec := range sections {
		if !slices.Contains(ids, sec.ID) {
			continue
		}
		var targets []string
		for _, block := range taskBlocks(sec.Body) {
			if strings.HasPrefix(block.id, "T-") {
				targets = append(targets, block.id)
			}
		}
		b.WriteString("- Assigned H2 `" + sec.ID + "`; writable targets: `" + sec.ID + "`")
		if len(targets) > 0 {
			b.WriteString(", `" + strings.Join(targets, "`, `") + "`")
		}
		b.WriteString(".\n")
	}
	b.WriteString("\n")
	b.WriteString("Fix these findings and nothing unrelated:\n\n")
	for _, f := range batch {
		b.WriteString("- " + f + "\n")
	}
	joinedFindings := strings.Join(batch, "\n")
	if strings.Contains(joinedFindings, "**Verification:**") {
		b.WriteString("\nVerification field repair: use `set_field` with field `Verification` and a value containing ONLY one executable shell command enclosed in Markdown backticks, for example `cc -fsyntax-only src/main.c`. Do not write instructions such as Run/inspect/verify and do not omit the backticks.\n")
	}
	if strings.Contains(joinedFindings, "**Exercises:**") {
		b.WriteString("\nExercises field repair: use `set_field` with field `Exercises`; its comma-separated artifact values must literally overlap the paths or targets in `Produces` that the Verification command checks. Do not name prose activities.\n")
	}
	if len(findings) > len(batch) {
		b.WriteString(fmt.Sprintf("\n%d additional findings remain checkpointed; they will be handled in later bounded repairs.\n", len(findings)-len(batch)))
	}
	for _, sec := range sections {
		if slices.Contains(ids, sec.ID) {
			b.WriteString("\n### Current " + sec.ID + " checkpoint\n\n## " + sec.ID)
			if sec.Title != "" {
				b.WriteString(" — " + sec.Title)
			}
			b.WriteString("\n\n" + sec.Body + "\n")
		}
	}
	return b.String(), ids
}

func structureRepairBatch(findings []string, sections []agent.Section) ([]string, []string) {
	taskParents := map[string][]string{}
	for _, sec := range sections {
		taskParents[sec.ID] = append(taskParents[sec.ID], sec.ID)
		for _, block := range taskBlocks(sec.Body) {
			if strings.HasPrefix(block.id, "T-") {
				if !slices.Contains(taskParents[block.id], sec.ID) {
					taskParents[block.id] = append(taskParents[block.id], sec.ID)
				}
			}
		}
	}
	idRE := regexp.MustCompile(`[A-Z]+-\d+`)
	consumerFindingRE := regexp.MustCompile(`^(T-\d+)\s+consumes\b`)
	parentsOf := func(f string) []string {
		// A missing dependency is repaired on the consumer. The producer id is
		// context explaining which edge is absent, not an alternative writable
		// endpoint. Treating both endpoints as peers made a T-008 finding select
		// M-05 (the producer's milestone), while T-008 itself lived in M-06; the
		// bounded patch could then never express the only valid correction.
		if match := consumerFindingRE.FindStringSubmatch(f); len(match) == 2 {
			parents := append([]string(nil), taskParents[match[1]]...)
			sort.Strings(parents)
			return parents
		}
		seen := map[string]bool{}
		var parents []string
		for _, id := range idRE.FindAllString(f, -1) {
			for _, parent := range taskParents[id] {
				if parent != "" && !seen[parent] {
					seen[parent] = true
					parents = append(parents, parent)
				}
			}
		}
		sort.Strings(parents)
		return parents
	}
	// Independent instances of the same mechanical defect should travel in
	// one transaction. A 17-section spec with one missing Implements field per
	// section otherwise needs at least 17 model calls and is mathematically
	// unable to converge under the 12-attempt cap. Keep graph findings on the
	// single-hub path below; this batching is only for identical one-parent
	// findings such as "SPEC-NNN has no **Implements:** line".
	type independentGroup struct {
		parents  map[string]bool
		findings []string
	}
	groups := map[string]*independentGroup{}
	for _, f := range findings {
		parents := parentsOf(f)
		if len(parents) != 1 {
			continue
		}
		space := strings.IndexByte(f, ' ')
		if space <= 0 {
			continue
		}
		// The finding must be about the H2 itself. Task findings are already
		// batched naturally by their common milestone checkpoint.
		if f[:space] != parents[0] {
			continue
		}
		signature := f[space+1:]
		group := groups[signature]
		if group == nil {
			group = &independentGroup{parents: map[string]bool{}}
			groups[signature] = group
		}
		group.parents[parents[0]] = true
		group.findings = append(group.findings, f)
	}
	var independent *independentGroup
	independentSignature := ""
	for signature, group := range groups {
		if len(group.parents) < 2 {
			continue
		}
		if independent == nil || len(group.parents) > len(independent.parents) ||
			(len(group.parents) == len(independent.parents) && signature < independentSignature) {
			independent, independentSignature = group, signature
		}
	}
	if independent != nil {
		var targets []string
		for parent := range independent.parents {
			targets = append(targets, parent)
		}
		sort.Strings(targets)
		targets = targets[:min(len(targets), maxIndependentSections)]
		targetSet := map[string]bool{}
		for _, target := range targets {
			targetSet[target] = true
		}
		var batch []string
		for _, f := range independent.findings {
			parents := parentsOf(f)
			if len(parents) == 1 && targetSet[parents[0]] {
				batch = append(batch, f)
			}
		}
		return batch, targets
	}
	// Repair the highest-leverage H2 first. The old first-finding policy chose
	// M-01/M-09 while M-10's broad `src/` lane caused seven of ten findings in
	// Neocapture corrida 12. Coverage makes one small patch remove the largest
	// connected part of the defect graph.
	coverage := map[string]int{}
	for _, f := range findings {
		for _, parent := range parentsOf(f) {
			coverage[parent]++
		}
	}
	var ranked []string
	for parent := range coverage {
		ranked = append(ranked, parent)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if coverage[ranked[i]] != coverage[ranked[j]] {
			return coverage[ranked[i]] > coverage[ranked[j]]
		}
		return ranked[i] < ranked[j]
	})
	targets := ranked[:min(len(ranked), maxRepairSections)]
	targetSet := map[string]bool{}
	for _, target := range targets {
		targetSet[target] = true
	}
	var batch []string
	for _, f := range findings {
		parents := parentsOf(f)
		belongs := len(targets) == 0
		if len(targets) > 0 && len(parents) > 0 {
			belongs = false
			for _, parent := range parents {
				if targetSet[parent] {
					belongs = true
				}
			}
		}
		if belongs {
			batch = append(batch, f)
			if len(batch) == maxRepairFindings {
				break
			}
		}
	}
	if len(batch) == 0 {
		batch = append(batch, findings[:min(len(findings), maxRepairFindings)]...)
	}
	sort.Strings(targets)
	return batch, targets
}

type structurePatch struct {
	Sections   []string             `json:"sections"`
	Operations []structureOperation `json:"operations"`
}

type structureOperation struct {
	Op       string `json:"op"`
	Target   string `json:"target"`
	Field    string `json:"field,omitempty"`
	Value    string `json:"value,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	Old      string `json:"old,omitempty"`
	New      string `json:"new,omitempty"`
}

func applyStructurePatch(base, patch *agent.Outcome, contract string, allowed []string) (*agent.Outcome, error) {
	if base == nil || patch == nil {
		return base, fmt.Errorf("%w: missing checkpoint or patch", ErrStructureRepairScope)
	}
	raw, err := json.Marshal(patch.Parsed)
	if err != nil {
		return base, fmt.Errorf("%w: encode patch: %v", ErrStructureRepairScope, err)
	}
	var p structurePatch
	if err := json.Unmarshal(raw, &p); err != nil {
		return base, fmt.Errorf("%w: decode patch: %v", ErrStructureRepairScope, err)
	}
	if len(p.Operations) == 0 || len(p.Operations) > 12 {
		return base, fmt.Errorf("%w: patch must contain 1-12 operations", ErrStructureRepairScope)
	}
	parents := map[string]string{}
	levels := map[string]int{}
	for _, sec := range sectionsOf(base) {
		parents[sec.ID], levels[sec.ID] = sec.ID, 2
		for _, block := range taskBlocks(sec.Body) {
			if strings.HasPrefix(block.id, "T-") {
				parents[block.id], levels[block.id] = sec.ID, 3
			}
		}
	}
	want := map[string]bool{}
	for _, id := range allowed {
		want[id] = true
	}
	text := base.Text
	for _, op := range p.Operations {
		if !want[parents[op.Target]] {
			return base, fmt.Errorf("%w: target %s is outside %s", ErrStructureRepairScope, op.Target, strings.Join(allowed, ", "))
		}
		switch op.Op {
		case "set_field":
			if strings.TrimSpace(op.Field) == "" || strings.TrimSpace(op.Value) == "" {
				return base, fmt.Errorf("%w: set_field needs field and value", ErrStructureRepairScope)
			}
			text, err = setMarkdownField(text, op.Target, op.Field, op.Value)
		case "remove_field":
			if strings.TrimSpace(op.Field) == "" {
				return base, fmt.Errorf("%w: remove_field needs field", ErrStructureRepairScope)
			}
			text, err = removeMarkdownField(text, op.Target, op.Field)
		case "replace_text":
			text, err = replaceMarkdownText(text, op.Target, op.Old, op.New)
		case "replace_block":
			if levels[op.Target] != 3 && !(levels[op.Target] == 2 && contract != "markdown_sections:M") {
				return base, fmt.Errorf("%w: replace_block may replace an H3 task or a non-plan H2 section, not %s", ErrStructureRepairScope, op.Target)
			}
			text, err = replaceMarkdownBlock(text, op.Target, op.Markdown)
		case "append_block":
			if levels[op.Target] != 2 {
				return base, fmt.Errorf("%w: append_block target must be an H2 section, not %s", ErrStructureRepairScope, op.Target)
			}
			text, err = appendMarkdownBlock(text, op.Target, op.Markdown)
		case "delete_block":
			if levels[op.Target] != 3 {
				return base, fmt.Errorf("%w: delete_block may delete an H3 task, not %s", ErrStructureRepairScope, op.Target)
			}
			text, err = replaceMarkdownBlock(text, op.Target, "")
		default:
			return base, fmt.Errorf("%w: unsupported operation %q", ErrStructureRepairScope, op.Op)
		}
		if err != nil {
			return base, err
		}
	}
	parsed, err := agent.ParseContract(contract, text)
	if err != nil {
		return base, fmt.Errorf("%w: patched document: %v", ErrStructureRepairScope, err)
	}
	merged := *patch
	merged.Text, merged.Parsed = text, parsed
	return &merged, nil
}

func appendMarkdownBlock(text, sectionID, markdown string) (string, error) {
	if !strings.HasPrefix(strings.TrimSpace(markdown), "### T-") || strings.Contains(markdown, "\n## ") {
		return text, fmt.Errorf("%w: append_block must contain H3 task markdown and no H2", ErrStructureRepairScope)
	}
	lines, _, end, _, err := markdownBlockRange(text, sectionID)
	if err != nil {
		return text, err
	}
	insert := []string{"", strings.TrimSpace(markdown), ""}
	out := append([]string{}, lines[:end]...)
	out = append(out, insert...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}

func planManifestFindings(manifest *agent.PlanManifest, outcome *agent.Outcome) []string {
	if manifest == nil || outcome == nil {
		return nil
	}
	type actualTask struct {
		parent string
		body   string
	}
	actualMilestones := map[string]bool{}
	actualTasks := map[string]actualTask{}
	for _, sec := range sectionsOf(outcome) {
		actualMilestones[sec.ID] = true
		for _, block := range taskBlocks(sec.Body) {
			if strings.HasPrefix(block.id, "T-") {
				actualTasks[block.id] = actualTask{parent: sec.ID, body: block.body}
			}
		}
	}
	expectedTasks := map[string]string{}
	var findings []string
	for _, milestone := range manifest.Milestones {
		if !actualMilestones[milestone.ID] {
			findings = append(findings, fmt.Sprintf("%s from the validated plan manifest is missing", milestone.ID))
			continue
		}
		for _, task := range milestone.Tasks {
			expectedTasks[task.ID] = milestone.ID
			actual, ok := actualTasks[task.ID]
			if !ok {
				findings = append(findings, fmt.Sprintf("%s from the validated plan manifest is missing from %s — append that H3 task", task.ID, milestone.ID))
				continue
			}
			if actual.parent != milestone.ID {
				findings = append(findings, fmt.Sprintf("%s belongs to %s in the validated manifest, not %s", task.ID, milestone.ID, actual.parent))
			}
			for _, id := range task.Implements {
				if !slices.Contains(taskFieldItems(actual.body, "Implements"), id) {
					findings = append(findings, fmt.Sprintf("%s must **Implement:** %s from the validated manifest", task.ID, id))
				}
			}
			if !sameStringSet(taskFieldItems(actual.body, "Produces"), task.Produces) {
				findings = append(findings, fmt.Sprintf("%s **Produces:** differs from the validated manifest — set it to %s", task.ID, strings.Join(task.Produces, ", ")))
			}
			if !sameStringSet(taskFieldItems(actual.body, "Consumes"), task.Consumes) {
				value := strings.Join(task.Consumes, ", ")
				if value == "" {
					value = "none"
				}
				findings = append(findings, fmt.Sprintf("%s **Consumes:** differs from the validated manifest — set it to %s", task.ID, value))
			}
			if strings.TrimSpace(taskVerificationCommand(actual.body)) != strings.Trim(strings.TrimSpace(task.Verification), "`") {
				findings = append(findings, fmt.Sprintf("%s **Verification:** differs from the validated manifest — set the executable command to `%s`", task.ID, strings.Trim(strings.TrimSpace(task.Verification), "`")))
			}
		}
	}
	for id, actual := range actualTasks {
		if expectedTasks[id] == "" {
			findings = append(findings, fmt.Sprintf("%s in %s is absent from the validated plan manifest — delete that unplanned H3 task", id, actual.parent))
		}
	}
	sort.Strings(findings)
	return findings
}

// reconcilePlanManifest compiles the rendered plan back onto the topology
// that Ducklab already validated. The prose architect is free to explain a
// task, but it is not free to rename, duplicate, move, drop, or rewire it.
// Leaving that reconciliation to another model turn made small seats repair
// dozens of mechanically knowable mismatches and, worse, invent new task IDs
// while doing so (Neocapture corrida 20).
func reconcilePlanManifest(outcome *agent.Outcome, manifest *agent.PlanManifest, contract string) (*agent.Outcome, int, error) {
	if outcome == nil || manifest == nil || contract != "markdown_sections:M" {
		return outcome, 0, nil
	}
	type renderedTask struct {
		parent string
		body   string
	}
	byID := map[string][]renderedTask{}
	sectionByID := map[string]agent.Section{}
	for _, sec := range sectionsOf(outcome) {
		sectionByID[sec.ID] = sec
		for _, block := range taskBlocks(sec.Body) {
			if id, ok := canonicalPlanTaskID(block.id); ok {
				byID[id] = append(byID[id], renderedTask{parent: sec.ID, body: block.body})
			}
		}
	}

	preamble := documentPreamble(outcome.Text)
	var rendered []string
	if preamble != "" {
		rendered = append(rendered, preamble)
	}
	for _, milestone := range manifest.Milestones {
		var part strings.Builder
		part.WriteString("## " + milestone.ID + " — " + strings.TrimSpace(milestone.Title))
		if sec, ok := sectionByID[milestone.ID]; ok {
			if head := milestoneBodyPreamble(sec.Body); head != "" {
				part.WriteString("\n\n" + head)
			}
		}
		for _, task := range milestone.Tasks {
			body := ""
			for _, candidate := range byID[task.ID] {
				if candidate.parent == milestone.ID {
					body = candidate.body
					break
				}
			}
			if body == "" && len(byID[task.ID]) > 0 {
				body = byID[task.ID][0].body
			}
			if strings.TrimSpace(body) == "" {
				body = "Implement " + strings.TrimSpace(task.Title) + " according to the accepted specification.\n\n**Deliverables:**\n- The artifacts listed in **Produces:** below."
			}
			block := "### " + task.ID + " — " + strings.TrimSpace(task.Title) + "\n\n" + strings.TrimSpace(body)
			fields := []struct{ name, value string }{
				{"Implements", strings.Join(task.Implements, ", ")},
				{"Produces", manifestItems(task.Produces)},
				{"Consumes", manifestItems(task.Consumes)},
				{"Verification", "`" + strings.Trim(strings.TrimSpace(task.Verification), "`") + "`"},
			}
			var err error
			for _, field := range fields {
				block, err = setMarkdownField(block, task.ID, field.name, field.value)
				if err != nil {
					return outcome, 0, err
				}
			}
			if !itemsOverlap(task.Produces, taskFieldItems(block, "Exercises")) {
				block, err = setMarkdownField(block, task.ID, "Exercises", manifestItems(task.Produces))
				if err != nil {
					return outcome, 0, err
				}
			}
			part.WriteString("\n\n" + strings.TrimSpace(block))
		}
		rendered = append(rendered, part.String())
	}
	text := strings.Join(rendered, "\n\n")
	if text == outcome.Text {
		return outcome, 0, nil
	}
	parsed, err := agent.ParseContract(contract, text)
	if err != nil {
		return outcome, 0, err
	}
	normalized := *outcome
	normalized.Text, normalized.Parsed = text, parsed
	return &normalized, 1, nil
}

func manifestItems(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

func canonicalPlanTaskID(id string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(id), "T-")
	if !ok || rest == "" {
		return "", false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return "", false
	}
	return fmt.Sprintf("T-%03d", n), true
}

func milestoneBodyPreamble(body string) string {
	if i := regexp.MustCompile(`(?m)^###\s+T-`).FindStringIndex(body); i != nil {
		return strings.TrimSpace(body[:i[0]])
	}
	return strings.TrimSpace(body)
}

func documentPreamble(text string) string {
	if i := regexp.MustCompile(`(?m)^##\s+M-\d+`).FindStringIndex(text); i != nil {
		return strings.TrimSpace(text[:i[0]])
	}
	return ""
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa, bb := sortedCopy(a), sortedCopy(b)
	return slices.Equal(aa, bb)
}

func replaceMarkdownText(text, id, old, replacement string) (string, error) {
	if old == "" || old == replacement {
		return text, fmt.Errorf("%w: replace_text needs distinct old and new strings", ErrStructureRepairScope)
	}
	lines, start, end, _, err := markdownBlockRange(text, id)
	if err != nil {
		return text, err
	}
	body := strings.Join(lines[start:end], "\n")
	if strings.Count(body, old) != 1 {
		return text, fmt.Errorf("%w: replace_text old value occurs %d times in %s, want exactly once", ErrStructureRepairScope, strings.Count(body, old), id)
	}
	body = strings.Replace(body, old, replacement, 1)
	out := append([]string{}, lines[:start]...)
	out = append(out, strings.Split(body, "\n")...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func markdownBlockRange(text, id string) ([]string, int, int, int, error) {
	lines := strings.Split(text, "\n")
	start, level := -1, 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "##") {
			continue
		}
		hashes := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		if hashes != 2 && hashes != 3 {
			continue
		}
		rest := strings.TrimSpace(trimmed[hashes:])
		if rest == id || strings.HasPrefix(rest, id+" ") {
			start, level = i, hashes
			break
		}
	}
	if start < 0 {
		return lines, 0, 0, 0, fmt.Errorf("%w: target %s not found", ErrStructureRepairScope, id)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "##") {
			continue
		}
		hashes := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		if hashes <= level {
			end = i
			break
		}
	}
	return lines, start, end, level, nil
}

func setMarkdownField(text, id, field, value string) (string, error) {
	lines, start, end, level, err := markdownBlockRange(text, id)
	if err != nil {
		return text, err
	}
	fieldRE := regexp.MustCompile(`(?i)^\s*\*\*` + regexp.QuoteMeta(strings.TrimSpace(field)) + `:\*\*`)
	searchEnd := end
	if level == 2 {
		for i := start + 1; i < end; i++ {
			if strings.HasPrefix(strings.TrimSpace(lines[i]), "### ") {
				searchEnd = i
				break
			}
		}
	}
	line := "**" + strings.TrimSpace(field) + ":** " + strings.TrimSpace(value)
	for i := start + 1; i < searchEnd; i++ {
		if fieldRE.MatchString(lines[i]) {
			lines[i] = line
			return strings.Join(lines, "\n"), nil
		}
	}
	insert := start + 1
	lines = append(lines[:insert], append([]string{"", line}, lines[insert:]...)...)
	return strings.Join(lines, "\n"), nil
}

func removeMarkdownField(text, id, field string) (string, error) {
	lines, start, end, level, err := markdownBlockRange(text, id)
	if err != nil {
		return text, err
	}
	fieldRE := regexp.MustCompile(`(?i)^\s*\*\*` + regexp.QuoteMeta(strings.TrimSpace(field)) + `:\*\*`)
	for i := start + 1; i < end; i++ {
		if level == 2 && strings.HasPrefix(strings.TrimSpace(lines[i]), "### ") {
			break
		}
		if fieldRE.MatchString(lines[i]) {
			lines = append(lines[:i], lines[i+1:]...)
			return strings.Join(lines, "\n"), nil
		}
	}
	return text, fmt.Errorf("%w: %s has no %s field", ErrStructureRepairScope, id, field)
}

func replaceMarkdownBlock(text, id, markdown string) (string, error) {
	lines, start, end, level, err := markdownBlockRange(text, id)
	if err != nil {
		return text, err
	}
	var replacement []string
	if strings.TrimSpace(markdown) != "" {
		replacement = strings.Split(strings.TrimSpace(markdown), "\n")
		heading := strings.Repeat("#", level) + " " + id
		if len(replacement) == 0 || !(strings.HasPrefix(replacement[0], heading+" ") || replacement[0] == heading) {
			return text, fmt.Errorf("%w: replacement must begin with %s", ErrStructureRepairScope, heading)
		}
	}
	out := append([]string{}, lines[:start]...)
	out = append(out, replacement...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}

// normalizePlanGraph compiles redundant prose fields from the artifact graph.
// A small model should not have to keep Produces, Consumes, Depends on and
// Owns mutually consistent by hand. Exact produced paths define milestone
// lanes; producer/consumer matches define task dependencies. The generated
// fields remain in Markdown for people and downstream readers, but their truth
// comes from one graph.
func normalizePlanGraph(outcome *agent.Outcome, contract string) (*agent.Outcome, int, error) {
	if outcome == nil || contract != "markdown_sections:M" {
		return outcome, 0, nil
	}
	sections := sectionsOf(outcome)
	if len(sections) == 0 {
		return outcome, 0, nil
	}
	text, changes := outcome.Text, 0

	producer := map[string]string{}
	for _, sec := range sections {
		for _, block := range taskBlocks(sec.Body) {
			if !strings.HasPrefix(block.id, "T-") {
				continue
			}
			for _, item := range taskFieldItems(block.body, "Produces") {
				if producer[item] == "" {
					producer[item] = block.id
				}
			}
		}
	}
	for _, sec := range sections {
		for _, block := range taskBlocks(sec.Body) {
			if !strings.HasPrefix(block.id, "T-") {
				continue
			}
			deps := taskFieldItems(block.body, "Depends on")
			seen := map[string]bool{}
			for _, dep := range deps {
				seen[dep] = true
			}
			for _, item := range taskFieldItems(block.body, "Consumes") {
				if p := producer[item]; p != "" && p != block.id && !seen[p] {
					deps = append(deps, p)
					seen[p] = true
				}
			}
			if len(deps) > 0 && !slices.Equal(deps, taskFieldItems(block.body, "Depends on")) {
				var err error
				text, err = setMarkdownField(text, block.id, "Depends on", strings.Join(deps, ", "))
				if err != nil {
					return outcome, changes, err
				}
				changes++
			}
		}
	}

	claimed := map[string]bool{}
	for _, sec := range sections {
		var files, dirs []string
		blocks := taskBlocks(sec.Body)
		for _, block := range blocks {
			for _, item := range taskFieldItems(block.body, "Produces") {
				kind, value, ok := strings.Cut(item, ":")
				if !ok {
					continue
				}
				value = strings.TrimSpace(strings.Trim(value, "`"))
				switch kind {
				case "file":
					files = append(files, value)
				case "dir":
					dirs = append(dirs, value)
				}
			}
		}
		lanes := files
		if len(lanes) == 0 {
			lanes = dirs
		}
		var unique []string
		for _, lane := range lanes {
			lane = strings.TrimRight(strings.TrimSpace(lane), "/")
			if lane == "" || claimed[lane] || slices.Contains(unique, lane) {
				continue
			}
			claimed[lane] = true
			unique = append(unique, lane)
		}
		if len(unique) == 0 {
			continue
		}
		current := taskFieldItems(sec.Body, "Owns")
		if !slices.Equal(current, unique) {
			var err error
			text, err = setMarkdownField(text, sec.ID, "Owns", strings.Join(unique, ", "))
			if err != nil {
				return outcome, changes, err
			}
			changes++
		}
	}
	if changes == 0 {
		return outcome, 0, nil
	}
	parsed, err := agent.ParseContract(contract, text)
	if err != nil {
		return outcome, 0, err
	}
	normalized := *outcome
	normalized.Text, normalized.Parsed = text, parsed
	return &normalized, changes, nil
}

// mergeStructureRepair replaces only H2 sections returned by a bounded
// repair. Returning a whole document remains compatible: every returned
// section simply replaces its checkpoint counterpart.
func mergeStructureRepair(base, patch *agent.Outcome, contract string) *agent.Outcome {
	merged, err := mergeStructureRepairScoped(base, patch, contract, nil)
	if err != nil {
		return base
	}
	return merged
}

// materializePartialRevision keeps an ordinary council revision from
// accidentally becoming the whole document. Neocapture's spec architect
// answered a two-finding review with only SPEC-004; treating that response as
// a replacement erased eight already-reviewed sections before the structure
// repair loop even began. A non-empty strict subset of known H2 sections is a
// transactional patch over the last complete draft. Unknown or duplicate
// sections remain ordinary revisions so the structure checker can reject them.
func materializePartialRevision(base, revision *agent.Outcome, contract string) (*agent.Outcome, []string) {
	if base == nil || revision == nil {
		return revision, nil
	}
	baseSections, revisedSections := sectionsOf(base), sectionsOf(revision)
	if len(revisedSections) == 0 || len(revisedSections) >= len(baseSections) {
		return revision, nil
	}
	known := map[string]bool{}
	for _, sec := range baseSections {
		known[sec.ID] = true
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(revisedSections))
	for _, sec := range revisedSections {
		if !known[sec.ID] || seen[sec.ID] {
			return revision, nil
		}
		seen[sec.ID] = true
		ids = append(ids, sec.ID)
	}
	merged, err := mergeStructureRepairScoped(base, revision, contract, ids)
	if err != nil {
		return revision, nil
	}
	return merged, ids
}

// mergeStructureRepairScoped treats a bounded repair as a transaction. The
// Neocapture plan run returned unrelated milestones during a one-H2 repair;
// merging every returned H2 duplicated task IDs across untouched siblings.
func mergeStructureRepairScoped(base, patch *agent.Outcome, contract string, allowed []string) (*agent.Outcome, error) {
	if base == nil || patch == nil {
		return patch, nil
	}
	patches := sectionsOf(patch)
	if len(patches) == 0 {
		return base, fmt.Errorf("%w: response contained no H2 section", ErrStructureRepairScope)
	}
	if allowed != nil {
		if len(allowed) == 0 {
			return base, fmt.Errorf("%w: no section was assigned", ErrStructureRepairScope)
		}
		want := map[string]bool{}
		for _, id := range allowed {
			want[id] = true
		}
		seen := map[string]bool{}
		for _, sec := range patches {
			if !want[sec.ID] {
				return base, fmt.Errorf("%w: got %s; expected only %s", ErrStructureRepairScope, sec.ID, strings.Join(allowed, ", "))
			}
			seen[sec.ID] = true
		}
		for _, id := range allowed {
			if !seen[id] {
				return base, fmt.Errorf("%w: missing assigned section %s", ErrStructureRepairScope, id)
			}
		}
	}
	text := base.Text
	prefix := strings.TrimPrefix(contract, "markdown_sections:")
	for _, sec := range patches {
		text = replaceH2Section(text, prefix, sec)
	}
	parsed, err := agent.ParseContract(contract, text)
	if err != nil {
		return base, fmt.Errorf("%w: merged document: %v", ErrStructureRepairScope, err)
	}
	merged := *patch
	merged.Text = text
	merged.Parsed = parsed
	return &merged, nil
}

func replaceH2Section(text, prefix string, patch agent.Section) string {
	lines := strings.Split(text, "\n")
	start, end := -1, len(lines)
	heading := regexp.MustCompile(`^##\s+` + regexp.QuoteMeta(prefix) + `-\d+(?:\s|$)`)
	for i, line := range lines {
		id, _, ok := parseRepairHeading(line, prefix)
		if !ok {
			continue
		}
		if start >= 0 && heading.MatchString(strings.TrimSpace(line)) {
			end = i
			break
		}
		if id == patch.ID {
			start = i
		}
	}
	if start < 0 {
		return text
	}
	title := "## " + patch.ID
	if strings.TrimSpace(patch.Title) != "" {
		title += " — " + strings.TrimSpace(patch.Title)
	}
	replacement := strings.Split(title+"\n\n"+strings.TrimSpace(patch.Body), "\n")
	out := append([]string{}, lines[:start]...)
	out = append(out, replacement...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n")
}

func parseRepairHeading(line, prefix string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "## "+prefix+"-") {
		return "", "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
	parts := strings.Fields(rest)
	if len(parts) == 0 {
		return "", "", false
	}
	return parts[0], strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(rest, parts[0])), "—")), true
}

func structureProgressNote(findings, previous []string) string {
	open := map[string]bool{}
	for _, f := range findings {
		open[f] = true
	}
	var resolved []string
	for _, f := range previous {
		if !open[f] {
			resolved = append(resolved, f)
		}
	}
	note := structureNote(findings)
	if len(resolved) == 0 {
		return note
	}
	var b strings.Builder
	b.WriteString(note)
	b.WriteString("\n## Resolved since the previous attempt — do not reintroduce\n\n")
	for _, f := range resolved {
		b.WriteString("- " + f + "\n")
	}
	return b.String()
}

// kindOfContract maps a council's document contract to its artifact kind.
// A critic's own contract is "verdict", so the architect turns' contract is
// consulted through the script.
func kindOfContract(contract string, script *Script) string {
	prefix := strings.TrimPrefix(contract, "markdown_sections:")
	if prefix == contract && script != nil {
		for _, t := range script.Turns {
			if strings.HasPrefix(t.Contract, "markdown_sections:") {
				prefix = strings.TrimPrefix(t.Contract, "markdown_sections:")
				break
			}
		}
	}
	switch prefix {
	case "REQ":
		return "requirements"
	case "SPEC":
		return "spec"
	case "M", "T":
		return "plan"
	}
	return ""
}

// sectionsOf returns a parsed document's sections, or nil.
func sectionsOf(outcome *agent.Outcome) []agent.Section {
	if outcome == nil {
		return nil
	}
	secs, _ := outcome.Parsed.([]agent.Section)
	return secs
}
