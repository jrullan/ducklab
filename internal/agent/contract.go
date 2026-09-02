package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
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
//
// A finding is either anchored (file:line) or class-level: an invariant the
// change must hold that is violated across several places. The schema used to
// admit only the first, so a defect living in three files came back as three
// local symptoms across three rounds (B-286). A class-level finding carries
// File "*" (or no file) and names its invariant.
type Finding struct {
	Severity string `json:"severity"` // critical | major | minor
	File     string `json:"file"`
	Line     int    `json:"line"`
	Issue    string `json:"issue"`
	Fix      string `json:"fix"`
	// Invariant is the criterion or invariant this finding cites — the rule
	// the change must hold. Required for a class-level finding; welcome on an
	// anchored one.
	Invariant string `json:"invariant,omitempty"`
}

// ClassLevel reports whether the finding names a pattern rather than a place.
func (f Finding) ClassLevel() bool {
	return f.File == "*" || (strings.TrimSpace(f.File) == "" && strings.TrimSpace(f.Invariant) != "")
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
	case contract == "json:triage":
		return parseTriage(text)
	case contract == "json:inventory":
		return parseInventory(text)
	case contract == "json:plan_manifest":
		return parsePlanManifest(text)
	case strings.HasPrefix(contract, "json:"):
		return parseJSONObject(text)
	case strings.HasPrefix(contract, "markdown_sections:"):
		return parseSections(strings.TrimPrefix(contract, "markdown_sections:"), text)
	default:
		return nil, fmt.Errorf("unknown contract %q", contract)
	}
}

type PlanManifest struct {
	Milestones []ManifestMilestone `json:"milestones"`
}

type ManifestMilestone struct {
	ID    string         `json:"id"`
	Title string         `json:"title"`
	Tasks []ManifestTask `json:"tasks"`
}

type ManifestTask struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Implements   []string `json:"implements"`
	Produces     []string `json:"produces"`
	Consumes     []string `json:"consumes"`
	Verification string   `json:"verification"`
}

func parsePlanManifest(text string) (*PlanManifest, error) {
	raw, err := extractJSONObject(text)
	if err != nil {
		return nil, fmt.Errorf("plan manifest contract: %w", err)
	}
	var manifest PlanManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return nil, fmt.Errorf("plan manifest contract: %w", err)
	}
	if len(manifest.Milestones) == 0 {
		return nil, fmt.Errorf("plan manifest contract: milestones must not be empty")
	}
	seen := map[string]bool{}
	producer := map[string]string{}
	for mi, milestone := range manifest.Milestones {
		milestoneID, ok := canonicalContractID(milestone.ID, "M")
		if !ok || strings.TrimSpace(milestone.Title) == "" || len(milestone.Tasks) == 0 || seen[milestoneID] {
			return nil, fmt.Errorf("plan manifest contract: invalid milestone %d", mi)
		}
		manifest.Milestones[mi].ID = milestoneID
		seen[milestoneID] = true
		for ti, task := range milestone.Tasks {
			taskID, ok := canonicalContractID(task.ID, "T")
			if !ok || strings.TrimSpace(task.Title) == "" || len(task.Implements) == 0 ||
				len(task.Produces) == 0 || strings.TrimSpace(task.Verification) == "" || seen[taskID] {
				return nil, fmt.Errorf("plan manifest contract: invalid task %d in %s", ti, milestone.ID)
			}
			manifest.Milestones[mi].Tasks[ti].ID = taskID
			seen[taskID] = true
			for pi, item := range task.Produces {
				item = strings.TrimSpace(item)
				if item == "" {
					return nil, fmt.Errorf("plan manifest contract: empty produced artifact in %s", taskID)
				}
				if prior := producer[item]; prior != "" && prior != taskID {
					return nil, fmt.Errorf("plan manifest contract: %s and %s both produce %s", prior, taskID, item)
				}
				producer[item] = taskID
				manifest.Milestones[mi].Tasks[ti].Produces[pi] = item
			}
		}
	}
	return &manifest, nil
}

func validContractID(id, prefix string) bool {
	if !strings.HasPrefix(id, prefix+"-") || len(id) <= len(prefix)+1 {
		return false
	}
	for _, r := range id[len(prefix)+1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
		if strings.TrimSpace(f.Fix) == "" {
			// A real defect with an omitted remedy is incomplete, not a no-op.
			// Calling it "no defect" sent the reviewer a false repair instruction
			// on Neocapture T-004, so two otherwise useful findings were discarded.
			return nil, fmt.Errorf("verdict contract: finding %d has no fix; add one actionable sentence in the fix field", i)
		}
		// A small self-reviewer twice padded an approval with "no defects" as a
		// minor finding and "N/A" as its fix. Those are not observations: they
		// corrupt convergence metrics and make later seats search for a defect
		// that the reviewer explicitly says does not exist. Approval with no
		// defect is represented by findings: [].
		if noOpFinding(f) {
			return nil, fmt.Errorf("verdict contract: finding %d describes no defect or actionable change; omit it and use an empty findings list", i)
		}
		if f.File == "*" && strings.TrimSpace(f.Invariant) == "" {
			// "*" says "everywhere"; without the rule it is everywhere and
			// nowhere, and the implementer has nothing to hold the change to.
			return nil, fmt.Errorf("verdict contract: finding %d is class-level (file \"*\") but names no invariant", i)
		}
	}
	// A reviewer cannot approve and simultaneously report blocking problems.
	if v.Approved() && len(v.Blocking()) > 0 {
		return nil, fmt.Errorf("verdict contract: approved while reporting %d blocking finding(s); approve or request changes, not both",
			len(v.Blocking()))
	}
	return &v, nil
}

func noOpFinding(f Finding) bool {
	issue := strings.ToLower(strings.TrimSpace(f.Issue))
	fix := strings.ToLower(strings.Trim(strings.TrimSpace(f.Fix), "."))
	noIssue := strings.Contains(issue, "no defect") ||
		strings.Contains(issue, "no issue") ||
		strings.Contains(issue, "exactly what was asked") ||
		strings.Contains(issue, "no change needed")
	noFix := fix == "n/a" || fix == "none" ||
		strings.Contains(fix, "no change needed") || strings.Contains(fix, "no change required")
	return noIssue || noFix
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

type InventoryItem struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	EvidencePath string `json:"evidence-path"`
}

type Inventory struct {
	Items  []InventoryItem `json:"items"`
	Capped bool            `json:"capped,omitempty"`
}

func parseInventory(text string) (*Inventory, error) {
	raw, err := extractJSONObject(text)
	if err != nil {
		return nil, fmt.Errorf("inventory contract: %w", err)
	}
	var v Inventory
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("inventory contract: %w", err)
	}
	valid := map[string]bool{"route": true, "handler": true, "schema": true, "service": true, "client": true, "integration": true, "config": true}
	for i, item := range v.Items {
		if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.EvidencePath) == "" || !valid[item.Kind] {
			return nil, fmt.Errorf("inventory contract: invalid item %d", i)
		}
	}
	return &v, nil
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
		id, ok = canonicalContractID(rest, prefix)
		return id, "", ok
	}
	id, ok = canonicalContractID(rest[:idEnd], prefix)
	if !ok {
		return "", "", false
	}
	title = strings.TrimSpace(rest[idEnd:])
	title = strings.TrimPrefix(title, "—")
	title = strings.TrimPrefix(title, "-")
	return id, strings.TrimSpace(title), true
}

// canonicalContractID makes numeric identity independent of harmless zero
// padding. Small models occasionally count 009, 0010, 011; the next revision
// then writes 010 and a byte-oriented structure check falsely reports that
// the section disappeared.
func canonicalContractID(id, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(id, prefix+"-")
	if !ok || rest == "" {
		return "", false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return "", false
	}
	width := 3
	if prefix == "M" {
		width = 2
	}
	return fmt.Sprintf("%s-%0*d", prefix, width, n), true
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

// Triage is the triager's contract: one bug, classified (04 §6.6).
type SplitProposal struct {
	Title      string   `json:"title"`
	Acceptance []string `json:"acceptance"`
	Owns       []string `json:"owns"`
}

type Triage struct {
	Severity string `json:"severity"`
	// DuplicateOf is a bug id or empty. A pointer so "absent" and "not a
	// duplicate" are the same answer rather than two.
	DuplicateOf    string   `json:"duplicate_of"`
	Component      string   `json:"component"`
	SuspectedFiles []string `json:"suspected_files"`
	// Reproducible is nil when the triager could not tell. A bug whose
	// reproducibility is unknown is not the same as one known not to
	// reproduce, and flattening them would close real reports.
	Reproducible *bool `json:"reproducible"`
	// TaskTitle is empty when the bug is not actionable.
	TaskTitle string `json:"task_title"`
	Reason    string `json:"reason"`
	// TestStrategy is the triager's judgment on the honest verification for
	// this bug: "test-first" when it is reproducible as an automated test
	// (behavioural, crash, data), "build-only" when the honest check is
	// eyes (visual/cosmetic/config). Recommends, never decides.
	TestStrategy string `json:"test_strategy"`
	// Deliverables are the fix task's work contract: 2-5 concrete,
	// verifiable outcomes. The plan architects are dictated the same shape
	// (stage.TaskBodyContract); a promoted bug used to be the one door into
	// the build loop that carried none, so its implementer ran without a
	// checklist and reported "1/1" on the task as a whole.
	Deliverables []string `json:"deliverables"`
	// Proposal portions a multi-concern bug into independently owned tasks.
	// It remains advice until a person promotes the bug.
	Proposal []SplitProposal `json:"proposal"`
	// TestReason is one line: why that strategy — and for test-first, a
	// sketch of the reproduction the test-writer can start from.
	TestReason string `json:"test_reason"`
}

var validTriageSeverities = map[string]bool{
	"critical": true, "high": true, "normal": true, "low": true,
}

func parseTriage(text string) (*Triage, error) {
	raw, err := extractJSONObject(text)
	if err != nil {
		return nil, fmt.Errorf("triage contract: %w", err)
	}
	var t Triage
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, fmt.Errorf("triage contract: %w", err)
	}
	t.Severity = strings.ToLower(strings.TrimSpace(t.Severity))
	if !validTriageSeverities[t.Severity] {
		return nil, fmt.Errorf("triage contract: severity %q, want critical, high, normal or low", t.Severity)
	}
	for _, portion := range t.Proposal {
		if strings.TrimSpace(portion.Title) == "" || len(portion.Acceptance) > 2 || len(portion.Acceptance) == 0 || len(portion.Owns) == 0 {
			return nil, fmt.Errorf("triage contract: proposal portions need title, 1-2 acceptance criteria, and owns")
		}
	}
	if strings.TrimSpace(t.Reason) == "" {
		// A classification with no reason cannot be argued with, and this one
		// is going in front of a person who has to decide whether to trust it.
		return nil, fmt.Errorf("triage contract: no reason given")
	}
	// Normalized, tolerant of variants, never invented: an empty strategy
	// stays empty — older triages and unsure models simply do not recommend.
	switch v := strings.ToLower(strings.TrimSpace(t.TestStrategy)); {
	case strings.Contains(v, "test"):
		t.TestStrategy = "test-first"
	case strings.Contains(v, "build") || strings.Contains(v, "only"):
		t.TestStrategy = "build-only"
	default:
		t.TestStrategy = ""
	}
	return &t, nil
}
