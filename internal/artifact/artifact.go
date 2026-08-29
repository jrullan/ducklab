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
	Kind       Kind
	Project    string
	Version    int
	UpdatedAt  string
	RunID      string
	Ducklings  []string
	ApprovedBy string
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
			parseSectionFields(currentChild, text)
		case current != nil:
			current.Body = text
			parseSectionFields(current, text)
		default:
			preamble = append(preamble, text)
		}
	}
	commitChild := func() {
		if currentChild != nil && current != nil {
			current.Children = append(current.Children, *currentChild)
			currentChild = nil
		}
	}
	commitParent := func() {
		commitChild()
		if current != nil {
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
		return rest, "", true
	}
	id = rest[:end]
	if !validID(id, prefix) {
		return "", "", false
	}
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
	_, err := strconv.Atoi(rest)
	return err == nil
}

// parseSectionFields extracts `**Key:** value` lines and the Implements edge.
func parseSectionFields(s *Section, body string) {
	s.Fields = map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		key, value, ok := parseFieldLine(line)
		if !ok {
			continue
		}
		s.Fields[key] = value
		if key == "owns" {
			for _, path := range strings.Split(value, ",") {
				path = strings.TrimSpace(strings.Trim(path, "`"))
				if path != "" {
					s.Owns = append(s.Owns, path)
				}
			}
		}
		if key == "implements" || key == "depends on" {
			ids := splitIDs(value)
			if key == "implements" {
				s.Implements = append(s.Implements, ids...)
			}
		}
	}
}

// parseFieldLine recognises `**Key:** value`, tolerating a missing bold marker
// because models drop it often enough that rejecting the line would lose real
// content.
func parseFieldLine(line string) (key, value string, ok bool) {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "- ")
	if !strings.HasPrefix(t, "**") {
		// Accept `Key: value` only when the key looks like a field, not prose.
		i := strings.Index(t, ":")
		if i <= 0 || i > 24 || strings.Contains(t[:i], " and ") {
			return "", "", false
		}
		k := strings.ToLower(strings.TrimSpace(t[:i]))
		if !knownField(k) {
			return "", "", false
		}
		return k, strings.TrimSpace(t[i+1:]), true
	}
	t = strings.TrimPrefix(t, "**")
	i := strings.Index(t, ":")
	if i < 0 {
		return "", "", false
	}
	k := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(t[:i], "**")))
	v := strings.TrimSpace(t[i+1:])
	v = strings.TrimPrefix(v, "**")
	return k, strings.TrimSpace(v), true
}

func knownField(k string) bool {
	switch k {
	case "implements", "originates from", "requirements", "run", "submitted at", "outcome", "priority", "status", "complexity", "depends on", "role hint", "acceptance", "owns":
		return true
	}
	return false
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
		case "based_on":
			f.BasedOn = val
		case "origin":
			f.Origin = val
		}
	}
	return f
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
	if doc.Front.BasedOn != "" {
		fmt.Fprintf(&b, "based_on: %s\n", doc.Front.BasedOn)
	}
	if doc.Front.Origin != "" {
		fmt.Fprintf(&b, "origin: %s\n", doc.Front.Origin)
	}
	fmt.Fprintf(&b, "approved_by: %s\n", doc.Front.ApprovedBy)
	b.WriteString("---\n\n")

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
