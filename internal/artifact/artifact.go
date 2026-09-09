// Package artifact reads and writes the lifecycle documents: requirements,
// spec, plan, project memory.
//
// Artifacts are Markdown with YAML frontmatter (02 §5). The frontmatter is the
// machine's view and the body is the human's and the model's — one file, not a
// document plus a database record that drift apart.
package artifact

import (
	"fmt"
	"regexp"
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

// CurrentGrammar is the common artifact grammar written by Ducklab. It is
// independent from Frontmatter.Version, which remains the document revision.
const CurrentGrammar = 2

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
	grammarRaw string
	Version    int
	versionSet bool
	versionRaw string
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
	// Shape and cardinality are part of the same authority as the field name.
	// Empty Shape means the field is vocabulary-only and has no grammar-v2
	// contract beyond being in scope.
	Shape    FieldShape
	Required bool
	MinItems int
	MaxItems int
}

// FieldShape names the deterministic value grammar for a schema field.
type FieldShape string

const (
	ShapeInline           FieldShape = "inline text"
	ShapeSpecIDs          FieldShape = "comma-separated literal SPEC-NNN ids"
	ShapeChecklist        FieldShape = "flat markdown list"
	ShapeCommandChecklist FieldShape = "flat markdown list with one backtick command per item"
	ShapeArtifacts        FieldShape = "comma-separated artifact list"
	ShapeCommand          FieldShape = "one backtick command"
)

// fieldVocabulary is the single authority for parser and syntax-lint field validation.
var fieldVocabulary = []FieldDefinition{
	{Canonical: "Run", Kind: KindIntent, Scope: SectionScope}, {Canonical: "Submitted at", Kind: KindIntent, Scope: SectionScope}, {Canonical: "Outcome", Kind: KindIntent, Scope: SectionScope}, {Canonical: "Requirements", Kind: KindIntent, Scope: SectionScope},
	{Canonical: "Originates from", Kind: KindRequirements, Scope: SectionScope}, {Canonical: "Priority", Kind: KindRequirements, Scope: SectionScope}, {Canonical: "Status", Kind: KindRequirements, Scope: SectionScope}, {Canonical: "Acceptance", Kind: KindRequirements, Scope: SectionScope}, {Canonical: "Acceptance probes", Kind: KindRequirements, Scope: SectionScope}, {Canonical: "Assumption", Kind: KindRequirements, Scope: SectionScope},
	{Canonical: "Implements", Kind: KindSpec, Scope: SectionScope}, {Canonical: "Priority", Kind: KindSpec, Scope: SectionScope}, {Canonical: "Status", Kind: KindSpec, Scope: SectionScope}, {Canonical: "Complexity", Kind: KindSpec, Scope: SectionScope}, {Canonical: "Acceptance", Kind: KindSpec, Scope: SectionScope}, {Canonical: "Acceptance probes", Kind: KindSpec, Scope: SectionScope}, {Canonical: "Verification", Kind: KindSpec, Scope: SectionScope}, {Canonical: "Exercises", Kind: KindSpec, Scope: SectionScope}, {Canonical: "As-built", Kind: KindSpec, Scope: SectionScope}, {Canonical: "Covers", Kind: KindSpec, Scope: SectionScope},
	{Canonical: "Owns", Kind: KindPlan, Scope: PlanMilestoneScope}, {Canonical: "Milestone", Kind: KindPlan, Scope: PlanMilestoneScope}, {Canonical: "Work unit", Kind: KindPlan, Scope: PlanMilestoneScope}, {Canonical: "Acceptance slices", Kind: KindPlan, Scope: PlanMilestoneScope}, {Canonical: "Toolchain", Kind: KindPlan, Scope: PlanMilestoneScope}, {Canonical: "Implements", Kind: KindPlan, Scope: PlanMilestoneScope},
	{Canonical: "Implements", Kind: KindPlan, Scope: PlanTaskScope, Shape: ShapeSpecIDs, Required: true},
	{Canonical: "Priority", Kind: KindPlan, Scope: PlanTaskScope}, {Canonical: "Status", Kind: KindPlan, Scope: PlanTaskScope}, {Canonical: "Complexity", Kind: KindPlan, Scope: PlanTaskScope},
	{Canonical: "Depends on", Kind: KindPlan, Scope: PlanTaskScope, Aliases: []string{"Dependencies"}}, {Canonical: "Role hint", Kind: KindPlan, Scope: PlanTaskScope}, {Canonical: "Acceptance", Kind: KindPlan, Scope: PlanTaskScope},
	{Canonical: "Acceptance slices", Kind: KindPlan, Scope: PlanTaskScope, Shape: ShapeChecklist, Required: true, MinItems: 1, MaxItems: 3},
	{Canonical: "Acceptance probes", Kind: KindPlan, Scope: PlanTaskScope, Shape: ShapeCommandChecklist, Required: true},
	{Canonical: "Work unit", Kind: KindPlan, Scope: PlanTaskScope, Shape: ShapeInline, Required: true},
	{Canonical: "Owns", Kind: KindPlan, Scope: PlanTaskScope}, {Canonical: "Toolchain", Kind: KindPlan, Scope: PlanTaskScope},
	{Canonical: "Produces", Kind: KindPlan, Scope: PlanTaskScope, Shape: ShapeArtifacts, Required: true, MinItems: 1},
	{Canonical: "Consumes", Kind: KindPlan, Scope: PlanTaskScope, Shape: ShapeArtifacts, Required: true},
	{Canonical: "Verification", Kind: KindPlan, Scope: PlanTaskScope, Shape: ShapeCommand, Required: true},
	{Canonical: "Exercises", Kind: KindPlan, Scope: PlanTaskScope, Shape: ShapeArtifacts, Required: true, MinItems: 1},
	{Canonical: "Out of scope", Kind: KindPlan, Scope: PlanTaskScope}, {Canonical: "Assumption", Kind: KindPlan, Scope: PlanTaskScope},
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
type FieldError struct {
	ID, Key, Suggestion, Code string
	Detail                    string
	// RelatedIDs are syntactically recognizable SPEC ids inside a malformed
	// token. They are evidence for suppressing only the graph findings derived
	// from that primary parse failure; they never become trace edges.
	RelatedIDs []string
}

func (e FieldError) Error() string {
	if e.Code != "" {
		if e.Detail != "" {
			return e.Code + ": " + e.Detail
		}
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
		doc.FieldErrors = append(doc.FieldErrors, frontmatterDiagnostics(doc.Front)...)
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

// ContractLint validates the complete deterministic shape of a candidate.
// It intentionally does not inspect coverage, dependencies, repository state,
// or prose meaning; those belong to graph and semantic review layers.
func ContractLint(content string, kind Kind) ([]FieldError, error) {
	doc, err := Parse(content, kind)
	if err != nil {
		return nil, err
	}
	diagnostics := append([]FieldError(nil), doc.FieldErrors...)
	// Legacy plans remain readable during migration. Their vocabulary and
	// machine-readable values are still parsed (and malformed Implements tokens
	// still surface above), but fields introduced by grammar 2 are not required
	// until the document explicitly opts into that grammar.
	if kind == KindPlan && doc.Front.Grammar == CurrentGrammar {
		diagnostics = append(diagnostics, planContractDiagnostics(doc)...)
	}
	return diagnostics, nil
}

func planContractDiagnostics(doc *Document) []FieldError {
	if len(doc.Sections) == 0 {
		return []FieldError{{Key: "milestone", Code: "invalid_plan_structure", Detail: "plan has no M-NN milestone sections"}}
	}
	var diagnostics []FieldError
	for _, milestone := range doc.Sections {
		if len(milestone.Children) == 0 {
			diagnostics = append(diagnostics, FieldError{ID: milestone.ID, Key: "task", Code: "invalid_plan_structure", Detail: milestone.ID + " has no T-NNN task sections"})
		}
		for _, task := range milestone.Children {
			if !strings.HasPrefix(task.ID, "T-") {
				diagnostics = append(diagnostics, FieldError{ID: task.ID, Key: "task", Code: "invalid_plan_structure", Detail: fmt.Sprintf("%s is not a T-NNN task id", task.ID)})
			}
			diagnostics = append(diagnostics, planTaskContractDiagnostics(task)...)
		}
	}
	return diagnostics
}

func planTaskContractDiagnostics(task Section) []FieldError {
	var diagnostics []FieldError
	rules := fieldDefinitions(KindPlan, PlanTaskScope)
	for _, rule := range rules {
		if !rule.Required {
			continue
		}
		key := strings.ToLower(rule.Canonical)
		value, present := task.Fields[key]
		block := fieldBlock(task.Body, rule.Canonical)
		if !present {
			diagnostics = append(diagnostics, FieldError{ID: task.ID, Key: rule.Canonical, Code: "missing_required_field", Detail: fmt.Sprintf("%s has no **%s:** field", task.ID, rule.Canonical)})
			continue
		}
		switch rule.Shape {
		case ShapeInline:
			if strings.TrimSpace(value) == "" {
				diagnostics = append(diagnostics, invalidFieldShape(task.ID, rule, "write one non-empty inline value"))
			}
		case ShapeChecklist:
			items, nested := markdownListItems(block)
			if len(items) < rule.MinItems || (rule.MaxItems > 0 && len(items) > rule.MaxItems) {
				diagnostics = append(diagnostics, invalidFieldShape(task.ID, rule, fmt.Sprintf("use %d-%d flat top-level list items", rule.MinItems, rule.MaxItems)))
			} else if nested {
				diagnostics = append(diagnostics, invalidFieldShape(task.ID, rule, "use a flat list without nested items"))
			}
		case ShapeCommandChecklist:
			items, nested := markdownListItems(block)
			slices, _ := markdownListItems(fieldBlock(task.Body, "Acceptance slices"))
			validCommands := len(items) == len(slices) && len(items) > 0 && !nested
			for _, item := range items {
				if len(backtickCommands(item)) != 1 {
					validCommands = false
				}
			}
			if !validCommands {
				remedy := fmt.Sprintf("provide exactly one backtick command for each of the %d Acceptance slices", len(slices))
				if len(slices) == 0 {
					remedy = "define Acceptance slices first, then provide exactly one backtick command for each slice"
				}
				diagnostics = append(diagnostics, invalidFieldShape(task.ID, rule, remedy))
			}
		case ShapeArtifacts:
			if rule.MinItems > 0 && len(commaItems(value)) < rule.MinItems {
				diagnostics = append(diagnostics, invalidFieldShape(task.ID, rule, "name at least one artifact; `none` is not valid here"))
			}
		case ShapeCommand:
			commands := backtickCommands(strings.Join(block, "\n"))
			if len(commands) != 1 {
				diagnostics = append(diagnostics, invalidFieldShape(task.ID, rule, "provide exactly one executable command in one backtick span; join dependent steps inside that command"))
			}
		}
	}
	return diagnostics
}

func invalidFieldShape(id string, rule FieldDefinition, remedy string) FieldError {
	return FieldError{ID: id, Key: rule.Canonical, Code: "invalid_field_shape", Detail: fmt.Sprintf("%s **%s:** must be %s; %s", id, rule.Canonical, rule.Shape, remedy)}
}

func fieldDefinitions(kind Kind, scope FieldScope) []FieldDefinition {
	var out []FieldDefinition
	for _, definition := range fieldVocabulary {
		if definition.Kind == kind && definition.Scope == scope {
			out = append(out, definition)
		}
	}
	return out
}

// PlanTaskGrammar describes the public grammar from the same rules consumed
// by ContractLint. Callers can present it without maintaining another list.
func PlanTaskGrammar() string {
	var lines []string
	for _, rule := range fieldDefinitions(KindPlan, PlanTaskScope) {
		if rule.Shape == "" {
			continue
		}
		required := "optional"
		if rule.Required {
			required = "required"
		}
		shape := string(rule.Shape)
		if rule.MinItems > 0 && rule.MaxItems > 0 {
			shape += fmt.Sprintf(" (%d-%d items)", rule.MinItems, rule.MaxItems)
		}
		lines = append(lines, fmt.Sprintf("**%s:** %s; %s", rule.Canonical, shape, required))
	}
	return strings.Join(lines, "\n")
}

func fieldBlock(body, name string) []string {
	lines := strings.Split(body, "\n")
	var out []string
	inside := false
	for _, line := range lines {
		key, value, bold, ok := parseFieldLine(line)
		if !inside {
			if ok && strings.EqualFold(strings.TrimSpace(key), name) {
				inside = true
				if value != "" {
					out = append(out, value)
				}
			}
			continue
		}
		if ok {
			_, canonical := canonicalField(key, KindPlan, PlanTaskScope)
			if bold || canonical {
				break
			}
		}
		if strings.HasPrefix(strings.TrimSpace(line), "##") {
			break
		}
		out = append(out, line)
	}
	return out
}

var markdownListItem = regexp.MustCompile(`^(?:[-*]|[0-9]+\.)\s+(.+)$`)
var backtickCommand = regexp.MustCompile("`([^`\\n]+)`")

func markdownListItems(lines []string) ([]string, bool) {
	var items []string
	nested := false
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		match := markdownListItem.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}
		if len(line) != len(trimmed) {
			nested = true
			continue
		}
		items = append(items, match[1])
	}
	return items, nested
}

func backtickCommands(value string) []string {
	var out []string
	for _, match := range backtickCommand.FindAllStringSubmatch(value, -1) {
		if strings.TrimSpace(match[1]) != "" {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}
	return out
}

func commaItems(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(strings.Trim(item, "`"))
		if item != "" && !strings.EqualFold(item, "none") && item != "-" {
			out = append(out, item)
		}
	}
	return out
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
			expected := ""
			if kind == KindSpec {
				expected = "REQ"
			} else if kind == KindPlan && scope == PlanTaskScope {
				expected = "SPEC"
			}
			ids, fieldErrors := parseIDField(s.ID, canonical, value, expected)
			s.Implements = append(s.Implements, ids...)
			s.FieldErrors = append(s.FieldErrors, fieldErrors...)
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

// splitIDs parses a comma-separated id list for tolerant, non-contract fields
// such as Depends on. Implements uses parseIDField and never drops malformed
// machine-readable tokens silently.
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

var embeddedID = regexp.MustCompile(`[A-Z]+-[0-9]+`)
var idRange = regexp.MustCompile(`^(SPEC)-([0-9]+)\s*[-–—]\s*(SPEC)-([0-9]+)$`)

// parseIDField implements grammar 2's literal list syntax. A valid item is one
// bare ID and items are separated only by commas. Related ids are retained on
// malformed diagnostics so graph validation can suppress only consequences
// of the failed parse without treating those ids as accepted edges.
func parseIDField(sectionID, field, value, expectedPrefix string) ([]string, []FieldError) {
	var ids []string
	var diagnostics []FieldError
	for _, raw := range strings.Split(value, ",") {
		token := strings.TrimSpace(raw)
		if token == "" {
			diagnostics = append(diagnostics, invalidIDDiagnostic(sectionID, field, token, "empty list item; use literal ids separated by commas"))
			continue
		}
		if looksLikeID(token) {
			prefix, _, _ := strings.Cut(token, "-")
			if expectedPrefix != "" && prefix != expectedPrefix {
				diagnostics = append(diagnostics, FieldError{
					ID: sectionID, Key: field, Code: "wrong_id_kind",
					Detail: fmt.Sprintf("%s **%s:** token %q has kind %s; use %s-NNN ids", sectionID, field, token, prefix, expectedPrefix),
				})
				continue
			}
			ids = append(ids, token)
			continue
		}

		reason := "use one literal " + expectedPrefix + "-NNN id per comma-separated item"
		if strings.ContainsAny(token, ";") {
			reason = "semicolon groups are not supported; separate literal ids with commas"
		} else if idRange.MatchString(token) {
			reason = "ranges are not supported; expand every literal id and separate them with commas"
		} else if matches := embeddedID.FindAllString(token, -1); len(matches) == 1 {
			reason = "punctuation or prose around an id is not supported; use the bare literal id"
		}
		diagnostics = append(diagnostics, invalidIDDiagnostic(sectionID, field, token, reason))
	}
	return ids, diagnostics
}

func invalidIDDiagnostic(sectionID, field, token, reason string) FieldError {
	return FieldError{
		ID: sectionID, Key: field, Code: "invalid_id_token",
		Detail:     fmt.Sprintf("%s unparsed token %q in **%s:**; %s", sectionID, token, field, reason),
		RelatedIDs: relatedSpecIDs(token),
	}
}

func relatedSpecIDs(token string) []string {
	if match := idRange.FindStringSubmatch(token); match != nil {
		first, firstErr := strconv.Atoi(match[2])
		last, lastErr := strconv.Atoi(match[4])
		if firstErr == nil && lastErr == nil && first <= last && last-first <= 1000 {
			out := make([]string, 0, last-first+1)
			for n := first; n <= last; n++ {
				out = append(out, fmt.Sprintf("SPEC-%03d", n))
			}
			return out
		}
	}
	var out []string
	for _, id := range embeddedID.FindAllString(token, -1) {
		if strings.HasPrefix(id, "SPEC-") && validID(id, "SPEC") {
			out = append(out, canonicalID(id, "SPEC"))
		}
	}
	return out
}

// FormatTaskImplements renders the exact delimiter accepted by the grammar-2
// task Implements parser.
func FormatTaskImplements(ids []string) string { return strings.Join(ids, ", ") }

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
			f.grammarRaw = val
			n, _ := strconv.Atoi(val)
			f.Grammar = n
		case "version":
			f.versionSet = true
			f.versionRaw = val
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
	if _, err := strconv.Atoi(f.grammarRaw); err != nil {
		return []FieldError{{Key: "grammar", Code: "invalid_frontmatter", Detail: fmt.Sprintf("grammar must be an integer, got %q", f.grammarRaw)}}
	}
	if f.Grammar != CurrentGrammar {
		return []FieldError{{Key: "grammar", Code: "unsupported_grammar", Detail: fmt.Sprintf("grammar %d is not supported; use grammar: %d", f.Grammar, CurrentGrammar)}}
	}
	return nil
}

func frontmatterDiagnostics(f Frontmatter) []FieldError {
	if !f.versionSet {
		return nil
	}
	n, err := strconv.Atoi(f.versionRaw)
	if err != nil || n < 0 {
		return []FieldError{{Key: "version", Code: "invalid_frontmatter", Detail: fmt.Sprintf("version must be a non-negative integer, got %q", f.versionRaw)}}
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
