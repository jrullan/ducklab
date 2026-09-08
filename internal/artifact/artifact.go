// Package artifact reads and writes the lifecycle documents: requirements,
// spec, plan, project memory.
//
// Artifacts are Markdown with YAML frontmatter (02 §5). The frontmatter is the
// machine's view and the body is the human's and the model's — one file, not a
// document plus a database record that drift apart.
package artifact

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind identifies an artifact.
type Kind string

const (
	KindIntent       Kind = "intent"
	KindRequirements Kind = "requirements"
	KindSpec         Kind = "spec"
	KindPlan         Kind = "plan"
	KindProject      Kind = "project"
)

// Prefix is the section id prefix an artifact's sections carry.
func (k Kind) Prefix() string {
	switch k {
	case KindIntent:
		return "INT"
	case KindRequirements:
		return "REQ"
	case KindSpec:
		return "SPEC"
	case KindPlan:
		return "M"
	}
	return ""
}

// Filename is the artifact's file, relative to .ducklab/docs.
func (k Kind) Filename() string { return string(k) + ".md" }

// ValidKind reports whether a string names an artifact.
func ValidKind(s string) bool {
	switch Kind(s) {
	case KindIntent, KindRequirements, KindSpec, KindPlan, KindProject:
		return true
	}
	return false
}

// Frontmatter is the machine-readable header every artifact carries.
type Frontmatter struct {
	Kind    Kind
	Project string
	// Grammar is the version of the artifact syntax, independent from Version,
	// which identifies this revision of the document.
	Grammar    int
	grammarSet bool
	Version    int
	UpdatedAt  string
	RunID      string
	Ducklings  []string
	// ConfiguredDucklings is the available roster at launch. Ducklings names
	// only models with actual LLM calls, so provenance cannot imply that an
	// unused configured seat participated.
	ConfiguredDucklings []string
	ApprovedBy          string
	// Origin records how the document came to be when that is not the normal
	// way. "adopted" marks a survey: sections DERIVED from the tree by a
	// model rather than decided by a person. The approval gate is the same;
	// a reader auditing a requirement's origin deserves the distinction.
	Origin string
	// BasedOn is the hash of the approved document this proposal was drafted
	// against. A proposal is a frozen photograph: if the approved document
	// moves while it waits — a task removed, a bug promotion appending one —
	// accepting it would overwrite those edits wholesale and in silence.
	// Promote compares this against the document as it stands and refuses on
	// drift, naming what would be erased.
	BasedOn string
}

// Approved reports whether a human has signed off on this version.
func (f Frontmatter) Approved() bool { return strings.TrimSpace(f.ApprovedBy) != "" }

// FieldScope identifies where a schema field may appear.
type FieldScope string

const (
	SectionScope       FieldScope = "section"
	PlanMilestoneScope FieldScope = "plan milestone"
	PlanTaskScope      FieldScope = "plan task"
)

// FieldDefinition is one canonical Markdown schema key. Prose may be localized,
// but these keys are canonical and are not translated.
type FieldDefinition struct {
	Canonical string
	Kind      Kind
	Scope     FieldScope
	Aliases   []string
}

// fieldVocabulary is the single authority for parser and syntax-lint field validation.
var fieldVocabulary = []FieldDefinition{
	{"Run", KindIntent, SectionScope, nil}, {"Submitted at", KindIntent, SectionScope, nil}, {"Outcome", KindIntent, SectionScope, nil}, {"Requirements", KindIntent, SectionScope, nil},
	{"Originates from", KindRequirements, SectionScope, nil}, {"Priority", KindRequirements, SectionScope, nil}, {"Status", KindRequirements, SectionScope, nil}, {"Acceptance", KindRequirements, SectionScope, nil}, {"Acceptance probes", KindRequirements, SectionScope, nil},
	{"Implements", KindSpec, SectionScope, nil}, {"Priority", KindSpec, SectionScope, nil}, {"Status", KindSpec, SectionScope, nil}, {"Complexity", KindSpec, SectionScope, nil}, {"Acceptance", KindSpec, SectionScope, nil}, {"Acceptance probes", KindSpec, SectionScope, nil}, {"Verification", KindSpec, SectionScope, nil}, {"Exercises", KindSpec, SectionScope, nil}, {"As-built", KindSpec, SectionScope, nil}, {"Covers", KindSpec, SectionScope, nil},
	{"Owns", KindPlan, PlanMilestoneScope, nil}, {"Milestone", KindPlan, PlanMilestoneScope, nil}, {"Work unit", KindPlan, PlanMilestoneScope, nil}, {"Acceptance slices", KindPlan, PlanMilestoneScope, nil}, {"Toolchain", KindPlan, PlanMilestoneScope, nil}, {"Implements", KindPlan, PlanMilestoneScope, nil},
	{"Implements", KindPlan, PlanTaskScope, nil}, {"Priority", KindPlan, PlanTaskScope, nil}, {"Status", KindPlan, PlanTaskScope, nil}, {"Complexity", KindPlan, PlanTaskScope, nil}, {"Depends on", KindPlan, PlanTaskScope, []string{"Dependencies"}}, {"Role hint", KindPlan, PlanTaskScope, nil}, {"Acceptance", KindPlan, PlanTaskScope, nil}, {"Acceptance slices", KindPlan, PlanTaskScope, nil}, {"Acceptance probes", KindPlan, PlanTaskScope, nil}, {"Work unit", KindPlan, PlanTaskScope, nil}, {"Owns", KindPlan, PlanTaskScope, nil}, {"Toolchain", KindPlan, PlanTaskScope, nil}, {"Produces", KindPlan, PlanTaskScope, nil}, {"Consumes", KindPlan, PlanTaskScope, nil}, {"Verification", KindPlan, PlanTaskScope, nil}, {"Exercises", KindPlan, PlanTaskScope, nil}, {"Out of scope", KindPlan, PlanTaskScope, nil}, {"Assumption", KindPlan, PlanTaskScope, nil},
}

// FieldVocabulary returns a copy of the canonical, scoped field schema.
func FieldVocabulary() []FieldDefinition {
	out := make([]FieldDefinition, len(fieldVocabulary))
	copy(out, fieldVocabulary)
	for i := range out {
		out[i].Aliases = append([]string(nil), out[i].Aliases...)
	}
	return out
}

// FieldError describes a schema key that cannot be consumed by the parser.
type FieldError struct{ ID, Key, Suggestion, Code string }

func (e FieldError) Error() string {
	if e.Code != "" {
		return e.Code
	}
	message := fmt.Sprintf("%s unknown field **%s:**", e.ID, e.Key)
	if e.Suggestion != "" {
		message += fmt.Sprintf("; use **%s:**", e.Suggestion)
	}
	return message
}

// Section is one addressable unit of an artifact.
type Section struct {
	ID    string
	Title string
	Body  string
	// Implements are the ids this section traces up to: a SPEC implements
	// REQs, a task implements a SPEC. This line is the machine-readable edge
	// that makes the traceability spine checkable rather than aspirational.
	Implements []string
	// Fields holds the `**Key:** value` lines, keyed lowercase.
	Fields map[string]string
	// Owns is the plan section's lane, declared as `**Owns:** path/, other/**`.
	// For plan documents this field is normally declared on the milestone and
	// inherited by its child tasks.
	Owns []string
	// FieldErrors are unknown or out-of-scope bold fields in this section.
	FieldErrors []FieldError
	// Children are nested sections (tasks under a milestone in plan.md).
	Children []Section
}

// Field returns a field value, or "".
func (s Section) Field(name string) string {
	if s.Fields == nil {
		return ""
	}
	return s.Fields[strings.ToLower(name)]
}

// Document is a parsed artifact.
type Document struct {
	Front    Frontmatter
	Preamble string
	Sections []Section
	// FieldErrors are syntax-lint diagnostics collected while parsing.
	FieldErrors []FieldError
	// Raw is the file exactly as read, so nothing is lost by round-tripping a
	// document ducklab did not fully understand.
	Raw string
}

// Section finds a section by id, including nested ones.
func (d *Document) Section(id string) *Section {
	for i := range d.Sections {
		if d.Sections[i].ID == id {
			return &d.Sections[i]
		}
		for j := range d.Sections[i].Children {
			if d.Sections[i].Children[j].ID == id {
				return &d.Sections[i].Children[j]
			}
		}
	}
	return nil
}

// IDs returns every section id, parents then children, in document order.
func (d *Document) IDs() []string {
	var out []string
	for _, s := range d.Sections {
		out = append(out, s.ID)
		for _, c := range s.Children {
			out = append(out, c.ID)
		}
	}
	return out
}

// Parse reads an artifact.
//
// A heading that does not match the id rule is kept in the body but not
// indexed: an artifact is a human document first, and refusing to load one
// because a model added an extra heading would make the whole cycle brittle.
func Parse(content string, kind Kind) (*Document, error) {
	doc := &Document{Raw: content}

	body := content
	if fm, rest, ok := splitFrontmatter(content); ok {
		doc.Front = parseFrontmatter(fm)
		doc.FieldErrors = append(doc.FieldErrors, grammarDiagnostic(doc.Front)...)
		body = rest
	}

	prefix := kind.Prefix()
	lines := strings.Split(body, "\n")

	var preamble []string
	var current *Section
	var currentChild *Section
	var buf []string

	flush := func() {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		buf = nil
		switch {
		case currentChild != nil:
			currentChild.Body = text
			parseSectionFields(currentChild, text, kind, PlanTaskScope)
		case current != nil:
			current.Body = text
			scope := SectionScope
			if kind == KindPlan {
				scope = PlanMilestoneScope
			}
			parseSectionFields(current, text, kind, scope)
		default:
			preamble = append(preamble, text)
		}
	}
	commitChild := func() {
		if currentChild != nil && current != nil {
			doc.FieldErrors = append(doc.FieldErrors, currentChild.FieldErrors...)
			current.Children = append(current.Children, *currentChild)
			currentChild = nil
		}
	}
	commitParent := func() {
		commitChild()
		if current != nil {
			doc.FieldErrors = append(doc.FieldErrors, current.FieldErrors...)
			doc.Sections = append(doc.Sections, *current)
			current = nil
		}
	}

	for _, line := range lines {
		if id, title, ok := parseHeading(line, "## ", prefix); ok {
			flush()
			commitParent()
			current = &Section{ID: id, Title: title}
			continue
		}
		// A plan's tasks are H3 under a milestone. The prefix is open on
		// purpose: a person's plan numbers tasks T-, but the bench suite
		// numbers its fixtures B- — and reading only T- made every bench
		// project a plan with zero tasks, which nothing noticed until
		// RunStart learned to refuse a task it could not find and refused
		// all nine.
		if current != nil {
			id, title, ok := parseHeading(line, "### ", "T")
			if !ok && kind == KindPlan {
				id, title, ok = parseAnyIDHeading(line, "### ")
			}
			if ok {
				flush()
				commitChild()
				currentChild = &Section{ID: id, Title: title}
				continue
			}
		}
		buf = append(buf, line)
	}
	flush()
	commitParent()

	doc.Preamble = strings.TrimSpace(strings.Join(preamble, "\n"))
	return doc, nil
}

// parseHeading recognises `<marker><PREFIX>-NNN — Title`.
//
// The separator may be an em dash or a hyphen: models produce both and the
// difference carries no meaning.
func parseHeading(line, marker, prefix string) (id, title string, ok bool) {
	if prefix == "" {
		return "", "", false
	}
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, marker) {
		return "", "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, marker))
	if !strings.HasPrefix(rest, prefix+"-") {
		return "", "", false
	}
	end := strings.IndexAny(rest, " \t")
	if end < 0 {
		if !validID(rest, prefix) {
			return "", "", false
		}
		return canonicalID(rest, prefix), "", true
	}
	id = rest[:end]
	if !validID(id, prefix) {
		return "", "", false
	}
	id = canonicalID(id, prefix)
	title = strings.TrimSpace(rest[end:])
	title = strings.TrimPrefix(title, "—")
	title = strings.TrimPrefix(title, "–")
	title = strings.TrimPrefix(title, "-")
	return id, strings.TrimSpace(title), true
}

// parseAnyIDHeading recognises `<marker><LETTERS>-NNN — Title` with any
// uppercase prefix.
func parseAnyIDHeading(line, marker string) (id, title string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, marker) {
		return "", "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, marker))
	dash := strings.Index(rest, "-")
	if dash <= 0 {
		return "", "", false
	}
	prefix := rest[:dash]
	for _, r := range prefix {
		if r < 'A' || r > 'Z' {
			return "", "", false
		}
	}
	return parseHeading(line, marker, prefix)
}

// validID requires PREFIX-<digits>, so a heading like "REQ-uirements" is prose
// rather than a section.
func validID(id, prefix string) bool {
	rest, ok := strings.CutPrefix(id, prefix+"-")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func canonicalID(id, prefix string) string {
	rest, _ := strings.CutPrefix(id, prefix+"-")
	n, _ := strconv.Atoi(rest)
	width := 3
	if prefix == "M" {
		width = 2
	}
	return fmt.Sprintf("%s-%0*d", prefix, width, n)
}

// SyntaxLint validates a candidate without writing or promoting it. Prose may
// be localized; Markdown schema keys must remain canonical and untranslated.
func SyntaxLint(content string, kind Kind) ([]FieldError, error) {
	doc, err := Parse(content, kind)
	if err != nil {
		return nil, err
	}
	return append([]FieldError(nil), doc.FieldErrors...), nil
}

// parseSectionFields extracts valid `**Key:** value` lines and trace edges.
func parseSectionFields(s *Section, body string, context ...interface{}) {
	kind, scope := fieldContext(s, context...)
	s.Fields = map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		key, value, bold, ok := parseFieldLine(line)
		if !ok {
			continue
		}
		canonical, valid := canonicalField(key, kind, scope)
		if !valid {
			if bold {
				s.FieldErrors = append(s.FieldErrors, FieldError{ID: s.ID, Key: key, Suggestion: fieldSuggestion(key, kind, scope)})
			}
			continue
		}
		key = strings.ToLower(canonical)
		s.Fields[key] = value
		if key == "owns" {
			for _, path := range strings.Split(value, ",") {
				path = strings.TrimSpace(strings.Trim(path, "`"))
				if path != "" {
					s.Owns = append(s.Owns, path)
				}
			}
		}
		if key == "implements" {
			s.Implements = append(s.Implements, splitIDs(value)...)
		}
	}
}

// parseFieldLine recognises a field and reports whether it was bold. Unbolded
// fields are accepted only when the scoped vocabulary recognizes their key.
func parseFieldLine(line string) (key, value string, bold, ok bool) {
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
	bold = strings.HasPrefix(t, "**")
	if bold {
		t = strings.TrimPrefix(t, "**")
	}
	i := strings.Index(t, ":")
	if i <= 0 || (!bold && (i > 24 || strings.Contains(t[:i], " and "))) {
		return "", "", false, false
	}
	key = strings.TrimSpace(strings.TrimSuffix(t[:i], "**"))
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t[i+1:]), "**"))
	return key, value, bold, true
}

func fieldContext(s *Section, context ...interface{}) (Kind, FieldScope) {
	if len(context) == 2 {
		return context[0].(Kind), context[1].(FieldScope)
	}
	if strings.HasPrefix(s.ID, "INT-") {
		return KindIntent, SectionScope
	}
	if strings.HasPrefix(s.ID, "REQ-") {
		return KindRequirements, SectionScope
	}
	if strings.HasPrefix(s.ID, "SPEC-") {
		return KindSpec, SectionScope
	}
	return KindPlan, PlanTaskScope
}

func canonicalField(key string, kind Kind, scope FieldScope) (string, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, definition := range fieldVocabulary {
		if definition.Kind != kind || definition.Scope != scope {
			continue
		}
		if strings.EqualFold(key, definition.Canonical) {
			return definition.Canonical, true
		}
		for _, alias := range definition.Aliases {
			if strings.EqualFold(key, alias) {
				return definition.Canonical, true
			}
		}
	}
	return "", false
}

func fieldSuggestion(key string, kind Kind, scope FieldScope) string {
	key = strings.ToLower(strings.TrimSpace(key))
	best, distance := "", 3
	for _, definition := range fieldVocabulary {
		if definition.Kind != kind || definition.Scope != scope {
			continue
		}
		if d := levenshtein(key, strings.ToLower(definition.Canonical)); d < distance {
			best, distance = definition.Canonical, d
		}
		for _, alias := range definition.Aliases {
			if d := levenshtein(key, strings.ToLower(alias)); d < distance {
				best, distance = definition.Canonical, d
			}
		}
	}
	return best
}

func levenshtein(a, b string) int {
	a, b = string([]rune(a)), string([]rune(b))
	ar, br := []rune(a), []rune(b)
	row := make([]int, len(br)+1)
	for j := range row {
		row[j] = j
	}
	for i, x := range ar {
		next := make([]int, len(br)+1)
		next[0] = i + 1
		for j, y := range br {
			cost := 0
			if x != y {
				cost = 1
			}
			next[j+1] = min(next[j]+1, row[j+1]+1, row[j]+cost)
		}
		row = next
	}
	return row[len(br)]
}

func min(values ...int) int {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

// splitIDs parses a comma-separated id list, ignoring anything that is not an
// id so a prose aside does not become a phantom edge.
func splitIDs(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		p := strings.TrimSpace(part)
		p = strings.Trim(p, "`")
		if p == "" || p == "none" || p == "-" {
			continue
		}
		if !looksLikeID(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func looksLikeID(s string) bool {
	for _, prefix := range []string{"INT-", "REQ-", "SPEC-", "M-", "T-", "B-", "ADR-"} {
		if strings.HasPrefix(s, prefix) {
			return validID(s, strings.TrimSuffix(prefix, "-"))
		}
	}
	return false
}

// --- frontmatter --------------------------------------------------------------

func splitFrontmatter(content string) (fm, rest string, ok bool) {
	// Strip a UTF-8 BOM: an editor that adds one would otherwise hide the
	// frontmatter behind an invisible byte.
	s := strings.TrimPrefix(content, "\uFEFF")
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return "", content, false
	}
	body := s[strings.Index(s, "\n")+1:]
	end := strings.Index(body, "\n---")
	if end < 0 {
		// An unterminated header is a malformed document, not a document with
		// no header: treating it as body would silently swallow the metadata.
		return "", content, false
	}
	fm = body[:end]
	after := body[end+len("\n---"):]
	if i := strings.Index(after, "\n"); i >= 0 {
		after = after[i+1:]
	} else {
		after = ""
	}
	return fm, after, true
}

func parseFrontmatter(fm string) Frontmatter {
	var f Frontmatter
	for _, line := range strings.Split(fm, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		val := strings.TrimSpace(v)
		val = strings.Trim(val, `"'`)
		switch key {
		case "kind":
			f.Kind = Kind(val)
		case "project":
			f.Project = val
		case "grammar":
			f.grammarSet = true
			n, _ := strconv.Atoi(val)
			f.Grammar = n
		case "version":
			n, _ := strconv.Atoi(val)
			f.Version = n
		case "updated_at":
			f.UpdatedAt = val
		case "run_id":
			f.RunID = val
		case "approved_by":
			f.ApprovedBy = val
		case "ducklings":
			f.Ducklings = parseList(val)
		case "configured_ducklings":
			f.ConfiguredDucklings = parseList(val)
		case "based_on":
			f.BasedOn = val
		case "origin":
			f.Origin = val
		}
	}
	return f
}

// grammarDiagnostic reports a grammar mismatch without rejecting a document:
// callers can still inspect and migrate legacy artifacts.
func grammarDiagnostic(f Frontmatter) []FieldError {
	if !f.grammarSet {
		return []FieldError{{Code: "legacy_grammar"}}
	}
	if f.Grammar != 2 {
		return []FieldError{{Code: "unsupported_grammar"}}
	}
	return nil
}

func parseList(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Render writes a document back out: frontmatter, preamble, sections.
func Render(doc *Document) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "kind: %s\n", doc.Front.Kind)
	if doc.Front.Project != "" {
		fmt.Fprintf(&b, "project: %s\n", doc.Front.Project)
	}
	if doc.Front.Grammar != 0 {
		fmt.Fprintf(&b, "grammar: %d\n", doc.Front.Grammar)
	}
	fmt.Fprintf(&b, "version: %d\n", doc.Front.Version)
	if doc.Front.UpdatedAt != "" {
		fmt.Fprintf(&b, "updated_at: %s\n", doc.Front.UpdatedAt)
	}
	if doc.Front.RunID != "" {
		fmt.Fprintf(&b, "run_id: %s\n", doc.Front.RunID)
	}
	if len(doc.Front.Ducklings) > 0 {
		fmt.Fprintf(&b, "ducklings: [%s]\n", strings.Join(doc.Front.Ducklings, ", "))
	}
	if len(doc.Front.ConfiguredDucklings) > 0 {
		fmt.Fprintf(&b, "configured_ducklings: [%s]\n", strings.Join(doc.Front.ConfiguredDucklings, ", "))
	}
	if doc.Front.BasedOn != "" {
		fmt.Fprintf(&b, "based_on: %s\n", doc.Front.BasedOn)
	}
	if doc.Front.Origin != "" {
		fmt.Fprintf(&b, "origin: %s\n", doc.Front.Origin)
	}
	fmt.Fprintf(&b, "approved_by: %s\n", doc.Front.ApprovedBy)
	b.WriteString("---\n\n")
	b.WriteString(RenderBody(doc))
	return b.String()
}

// RenderBody writes the exact human/model document without transport
// frontmatter. Document councils review this representation; proposal storage
// adds volatile run metadata later, so comparing whole-file bytes would make
// identity impossible even when the reviewed sections are identical.
func RenderBody(doc *Document) string {
	var b strings.Builder
	if doc.Preamble != "" {
		b.WriteString(doc.Preamble)
		b.WriteString("\n\n")
	}
	for _, s := range doc.Sections {
		fmt.Fprintf(&b, "## %s — %s\n\n", s.ID, s.Title)
		if s.Body != "" {
			b.WriteString(s.Body)
			b.WriteString("\n\n")
		}
		for _, c := range s.Children {
			fmt.Fprintf(&b, "### %s — %s\n\n", c.ID, c.Title)
			if c.Body != "" {
				b.WriteString(c.Body)
				b.WriteString("\n\n")
			}
		}
	}
	return b.String()
}
