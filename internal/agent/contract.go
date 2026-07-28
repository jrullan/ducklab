package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Contracts parse a turn's final text into a typed value (04 §6.1).
//
// v0.1 only validated: parseContract returned an error and threw the content
// away. pair needs the reviewer's findings to inject into the next round, and
// tournament needs the judge's choice to act on, so a contract must hand back
// what it parsed.
//
// Nothing here guesses. A response that does not satisfy its contract is
// repaired or fails (I6); it is never partially salvaged.

// Finding is one reviewer observation.
type Finding struct {
	Severity string `json:"severity"` // critical | major | minor
	File     string `json:"file"`
	Line     int    `json:"line"`
	Issue    string `json:"issue"`
	Fix      string `json:"fix"`
}

// Verdict is the reviewer contract's parsed value.
type Verdict struct {
	Verdict  string    `json:"verdict"` // approve | request-changes
	Findings []Finding `json:"findings"`
}

// Approved reports whether the reviewer approved.
func (v *Verdict) Approved() bool { return v != nil && v.Verdict == "approve" }

// Blocking returns the findings that must not be silently dropped.
func (v *Verdict) Blocking() []Finding {
	if v == nil {
		return nil
	}
	var out []Finding
	for _, f := range v.Findings {
		if f.Severity == "critical" || f.Severity == "major" {
			out = append(out, f)
		}
	}
	return out
}

// Choice is the judge contract's parsed value.
type Choice struct {
	Choice string `json:"choice"` // A | B | … | none
	Reason string `json:"reason"`
}

// Chosen reports whether the judge picked a candidate.
func (c *Choice) Chosen() bool { return c != nil && c.Choice != "" && c.Choice != "none" }

// Section is one parsed markdown section (REQ-001, SPEC-004, …).
type Section struct {
	ID    string
	Title string
	Body  string
}

var validSeverities = map[string]bool{"critical": true, "major": true, "minor": true}

// ParseContract parses text against a contract, returning the typed value.
// Contracts with no structured output return nil.
func ParseContract(contract, text string) (interface{}, error) {
	switch {
	case contract == "" || contract == "freeform" || contract == "edits":
		return nil, nil
	case contract == "verdict":
		return parseVerdict(text)
	case contract == "choice":
		return parseChoice(text)
	case contract == "json:decomposition":
		return parseDecomposition(text)
	case strings.HasPrefix(contract, "json:"):
		return parseJSONObject(text)
	case strings.HasPrefix(contract, "markdown_sections:"):
		return parseSections(strings.TrimPrefix(contract, "markdown_sections:"), text)
	default:
		return nil, fmt.Errorf("unknown contract %q", contract)
	}
}

func parseVerdict(text string) (*Verdict, error) {
	raw, err := extractJSONObject(text)
	if err != nil {
		return nil, fmt.Errorf("verdict contract: %w", err)
	}
	var v Verdict
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("verdict contract: %w", err)
	}
	if v.Verdict != "approve" && v.Verdict != "request-changes" {
		return nil, fmt.Errorf(`verdict contract: "verdict" must be "approve" or "request-changes", got %q`, v.Verdict)
	}
	for i, f := range v.Findings {
		if f.Severity == "" {
			// A finding with no severity cannot be ranked or filtered, and
			// silently defaulting it would hide a critical one.
			return nil, fmt.Errorf("verdict contract: finding %d has no severity", i)
		}
		if !validSeverities[f.Severity] {
			return nil, fmt.Errorf("verdict contract: finding %d has severity %q, want critical, major or minor", i, f.Severity)
		}
		if strings.TrimSpace(f.Issue) == "" {
			return nil, fmt.Errorf("verdict contract: finding %d has an empty issue", i)
		}
	}
	// A reviewer cannot approve and simultaneously report blocking problems.
	if v.Approved() && len(v.Blocking()) > 0 {
		return nil, fmt.Errorf("verdict contract: approved while reporting %d blocking finding(s); approve or request changes, not both",
			len(v.Blocking()))
	}
	return &v, nil
}

func parseChoice(text string) (*Choice, error) {
	raw, err := extractJSONObject(text)
	if err != nil {
		return nil, fmt.Errorf("choice contract: %w", err)
	}
	var c Choice
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("choice contract: %w", err)
	}
	if c.Choice == "" {
		return nil, fmt.Errorf(`choice contract: "choice" is required (a label like "A", or "none")`)
	}
	if c.Choice != "none" {
		if len(c.Choice) != 1 || c.Choice[0] < 'A' || c.Choice[0] > 'Z' {
			return nil, fmt.Errorf(`choice contract: "choice" must be a single letter label or "none", got %q`, c.Choice)
		}
	}
	if strings.TrimSpace(c.Reason) == "" {
		return nil, fmt.Errorf(`choice contract: "reason" is required`)
	}
	return &c, nil
}

func parseJSONObject(text string) (map[string]interface{}, error) {
	raw, err := extractJSONObject(text)
	if err != nil {
		return nil, fmt.Errorf("json contract: %w", err)
	}
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("json contract: %w", err)
	}
	return v, nil
}

func parseSections(prefix, text string) ([]Section, error) {
	var out []Section
	var cur *Section
	var body strings.Builder

	flush := func() {
		if cur != nil {
			cur.Body = strings.TrimSpace(body.String())
			out = append(out, *cur)
			body.Reset()
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if id, title, ok := parseSectionHeading(line, prefix); ok {
			flush()
			cur = &Section{ID: id, Title: title}
			continue
		}
		if cur != nil {
			body.WriteString(line)
			body.WriteString("\n")
		}
	}
	flush()

	if len(out) == 0 {
		return nil, fmt.Errorf("markdown_sections contract: no sections matching %q found; "+
			"each section must start with a heading like \"## %s-001 — Title\"", prefix, prefix)
	}
	return out, nil
}

// parseSectionHeading recognises `## <PREFIX>-NNN — Title`. The separator may
// be an em dash or a hyphen; models produce both and the distinction carries
// no meaning.
func parseSectionHeading(line, prefix string) (id, title string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "## ") {
		return "", "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
	if !strings.HasPrefix(rest, prefix+"-") {
		return "", "", false
	}
	// The id runs to the first space.
	idEnd := strings.IndexByte(rest, ' ')
	if idEnd < 0 {
		return rest, "", true
	}
	id = rest[:idEnd]
	title = strings.TrimSpace(rest[idEnd:])
	title = strings.TrimPrefix(title, "—")
	title = strings.TrimPrefix(title, "-")
	return id, strings.TrimSpace(title), true
}

// extractJSONObject pulls the JSON object out of a model response.
//
// Models wrap JSON in prose and fences even when told not to. This strips
// fences and then takes the outermost balanced {...}, which is tolerant of a
// preamble without being tolerant of ambiguity: if there is no balanced
// object, it is an error, not a guess.
func extractJSONObject(text string) (string, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return "", fmt.Errorf("empty response")
	}
	s = stripCodeFences(s)

	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", fmt.Errorf("no JSON object found in the response")
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		switch {
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
			// braces inside strings do not nest
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("unbalanced JSON object in the response")
}

// stripCodeFences removes a leading ```lang line and a trailing ``` line.
func stripCodeFences(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Subtask is one piece of a decomposition, with the files it alone may touch.
type Subtask struct {
	Title string   `json:"title"`
	Files []string `json:"files"`
	Body  string   `json:"body"`
}

// Decomposition is the `split` architect's contract: a task broken into pieces
// that own disjoint files.
//
// File ownership is what makes the later integration a copy rather than a
// merge. A weak model asked to merge whole files destroys working code, so the
// decomposition either yields disjoint ownership or the mode refuses (05 §4.5).
type Decomposition struct {
	Subtasks []Subtask `json:"subtasks"`
}

// MinSubtasks and MaxSubtasks bound a decomposition (05 §4.5).
//
// One subtask is not a decomposition — it is the original task with extra
// machinery. Beyond five, the seams outnumber the work.
const (
	MinSubtasks = 2
	MaxSubtasks = 5
)

func parseDecomposition(text string) (*Decomposition, error) {
	raw, err := extractJSONObject(text)
	if err != nil {
		return nil, fmt.Errorf("decomposition contract: %w", err)
	}
	var d Decomposition
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, fmt.Errorf("decomposition contract: %w", err)
	}
	if len(d.Subtasks) < MinSubtasks || len(d.Subtasks) > MaxSubtasks {
		return nil, fmt.Errorf("decomposition contract: %d subtasks, want %d to %d",
			len(d.Subtasks), MinSubtasks, MaxSubtasks)
	}
	for i, st := range d.Subtasks {
		if strings.TrimSpace(st.Title) == "" {
			return nil, fmt.Errorf("decomposition contract: subtask %d has no title", i)
		}
		if len(st.Files) == 0 {
			// A subtask owning nothing cannot be integrated: phase 4 copies by
			// ownership, so there would be nothing to copy and its work would
			// be silently dropped.
			return nil, fmt.Errorf("decomposition contract: subtask %q claims no files", st.Title)
		}
	}
	return &d, nil
}
