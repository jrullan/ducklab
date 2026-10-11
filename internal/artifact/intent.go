package artifact

// Intent is the person's side of the lifecycle record. Requirements are a
// council's normative interpretation; intent.md is an append-only journal of
// the briefs that caused those interpretations to change.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/xplat"
)

type historicalIntake struct {
	ID        string `json:"id"`
	Stage     string `json:"stage"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
	Accepted  bool   `json:"accepted"`
}

// EnsureIntent imports briefs from older intake runs and returns the journal.
// It never invents requirement edges: old proposal snapshots were consumed on
// acceptance, so only future promotions can prove which sections a brief
// added or changed.
func EnsureIntent(projectRoot string) (*Document, error) {
	doc, err := Load(projectRoot, KindIntent)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, sec := range doc.Sections {
		for _, run := range intentRuns(&sec) {
			seen[run] = true
		}
	}
	runsDir := filepath.Join(projectRoot, ".ducklab", "runs")
	entries, _ := os.ReadDir(runsDir)
	var old []historicalIntake
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runDir := filepath.Join(runsDir, entry.Name())
		state, readErr := os.ReadFile(filepath.Join(runDir, "state.json"))
		if readErr != nil {
			continue
		}
		var run historicalIntake
		// A prepared revision has not claimed its parent and may never run
		// (B-521): it is not history.
		if json.Unmarshal(state, &run) != nil || run.Stage != "intake" || seen[run.ID] || run.Status == "prepared" {
			continue
		}
		var request struct {
			Adopt bool `json:"adopt"`
		}
		if raw, requestErr := os.ReadFile(filepath.Join(runDir, "stage_request.json")); requestErr == nil {
			_ = json.Unmarshal(raw, &request)
		}
		if request.Adopt {
			continue
		}
		brief, briefErr := os.ReadFile(filepath.Join(runDir, "brief.md"))
		if briefErr != nil || strings.TrimSpace(string(brief)) == "" {
			continue
		}
		old = append(old, run)
	}
	sort.Slice(old, func(i, j int) bool { return old[i].StartedAt < old[j].StartedAt })
	changed := false
	for _, run := range old {
		brief, _ := os.ReadFile(filepath.Join(runsDir, run.ID, "brief.md"))
		outcome := "pending"
		if run.Accepted {
			outcome = "accepted"
		} else if run.Status == "done" || run.Status == "failed" {
			outcome = "not accepted"
		}
		appendIntentSection(doc, run.ID, run.StartedAt, outcome, string(brief), true)
		changed = true
	}
	if changed {
		if err := writeIntent(projectRoot, doc); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

// AppendIntent records a newly submitted brief before a model sees it.
func AppendIntent(projectRoot, runID, submittedAt, brief string) (string, error) {
	doc, err := EnsureIntent(projectRoot)
	if err != nil {
		return "", err
	}
	for _, sec := range doc.Sections {
		if sec.Field("run") == runID {
			return sec.ID, nil
		}
	}
	sec := appendIntentSection(doc, runID, submittedAt, "pending", brief, false)
	return sec.ID, writeIntent(projectRoot, doc)
}

func appendIntentSection(doc *Document, runID, submittedAt, outcome, brief string, legacy bool) *Section {
	id := fmt.Sprintf("INT-%03d", nextIntentNumber(doc))
	title := intentTitle(brief)
	body := fmt.Sprintf("**Run:** %s\n**Submitted at:** %s\n**Outcome:** %s\n**Requirements:** -\n", runID, submittedAt, outcome)
	if legacy {
		body += "\n_Imported from the historical run record; section-level links may be unavailable._\n"
	}
	body += "\n### Original brief\n\n" + fenceIntentText("brief", brief)
	section := Section{ID: id, Title: title, Body: body}
	parseSectionFields(&section, withoutIntentText(body), KindIntent, SectionScope)
	doc.Sections = append(doc.Sections, section)
	return &doc.Sections[len(doc.Sections)-1]
}

func nextIntentNumber(doc *Document) int {
	max := 0
	for _, sec := range doc.Sections {
		var n int
		if _, err := fmt.Sscanf(sec.ID, "INT-%d", &n); err == nil && n > max {
			max = n
		}
	}
	return max + 1
}

func intentTitle(brief string) string {
	for _, line := range strings.Split(strings.TrimSpace(brief), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len([]rune(line)) > 72 {
			return string([]rune(line)[:69]) + "…"
		}
		return line
	}
	return "Untitled intention"
}

// LinkRequirementsProposal adds the deterministic Intent → Requirements edge
// before the human accepts the proposal. Only new or textually changed
// sections are linked; unchanged requirements do not acquire a false origin.
func LinkRequirementsProposal(projectRoot, runID string) (string, []string, error) {
	intentID, err := IntentIDForRun(projectRoot, runID)
	if err != nil {
		return "", nil, err
	}
	if intentID == "" {
		return "", nil, nil
	}
	current, err := Load(projectRoot, KindRequirements)
	if err != nil {
		return "", nil, err
	}
	proposal, err := LoadProposed(projectRoot, KindRequirements)
	if err != nil || proposal == nil {
		return intentID, nil, err
	}
	linked := LinkRequirementsDocument(current, proposal, intentID)
	if len(linked) > 0 {
		if err := xplat.AtomicWrite(ProposedPath(projectRoot, KindRequirements), []byte(Render(proposal)), 0o644); err != nil {
			return "", nil, err
		}
	}
	return intentID, linked, nil
}

// IntentIDForRun returns the append-only intention that caused one intake.
// The stage needs this before final review so deterministic provenance is part
// of the reviewed candidate, not a mutation applied afterward.
func IntentIDForRun(projectRoot, runID string) (string, error) {
	intent, err := EnsureIntent(projectRoot)
	if err != nil {
		return "", err
	}
	if i := intentIndexForRun(intent, runID); i >= 0 {
		return intent.Sections[i].ID, nil
	}
	return "", nil
}

// LinkRequirementsDocument adds Intent → Requirement edges to an in-memory
// candidate. It is shared by pre-review materialization and the proposal-file
// compatibility path, making the latter idempotent.
func LinkRequirementsDocument(current, proposal *Document, intentID string) (linked []string) {
	if proposal == nil || intentID == "" {
		return nil
	}
	for i := range proposal.Sections {
		sec := &proposal.Sections[i]
		var before *Section
		if current != nil {
			before = current.Section(sec.ID)
		}
		if before != nil && before.Title == sec.Title && strings.TrimSpace(before.Body) == strings.TrimSpace(sec.Body) {
			continue
		}
		origins := idsInField(sec.Field("originates from"))
		if !contains(origins, intentID) {
			origins = append(origins, intentID)
		}
		sec.Body = setIntentField(sec.Body, "Originates from", strings.Join(origins, ", "))
		linked = append(linked, sec.ID)
	}
	return linked
}

// ResolveIntent records the outcome without changing the original brief.
//
// The run may be the intent's first run or any revision in its chain. Only an
// acceptance may close an intent from anywhere in the chain; any other outcome
// (superseded, not accepted) is recorded only when it lands on the chain's
// newest run — an older draft being closed while a later revision is still
// being worked says nothing about the person's request (B-521). An accepted
// intent is never downgraded.
func ResolveIntent(projectRoot, runID, outcome string, requirements []string) error {
	doc, err := EnsureIntent(projectRoot)
	if err != nil {
		return err
	}
	i := intentIndexForRun(doc, runID)
	if i < 0 {
		return nil
	}
	sec := &doc.Sections[i]
	if outcome != "accepted" {
		if strings.EqualFold(strings.TrimSpace(sec.Field("outcome")), "accepted") || intentHead(sec) != runID {
			return nil
		}
	}
	sec.Body = setIntentField(sec.Body, "Outcome", outcome)
	if len(requirements) > 0 {
		sec.Body = setIntentField(sec.Body, "Requirements", strings.Join(requirements, ", "))
	}
	parseSectionFields(sec, withoutIntentText(sec.Body), KindIntent, SectionScope)
	return writeIntent(projectRoot, doc)
}

// IntentRef is what a revision needs to know about the intent it continues.
type IntentRef struct {
	ID        string
	Outcome   string
	Revisions int
}

// IntentForRun finds the intent a run belongs to — as its first run or as one
// of its revisions. The zero IntentRef means the run serves no intent.
func IntentForRun(projectRoot, runID string) (IntentRef, error) {
	doc, err := EnsureIntent(projectRoot)
	if err != nil {
		return IntentRef{}, err
	}
	i := intentIndexForRun(doc, runID)
	if i < 0 {
		return IntentRef{}, nil
	}
	sec := &doc.Sections[i]
	return IntentRef{ID: sec.ID, Outcome: strings.TrimSpace(sec.Field("outcome")), Revisions: len(intentRevisionRuns(sec))}, nil
}

// AppendIntentRevision records a revision of an intake proposal under the
// intent it revises (B-521). A revision is not a new intention: the person is
// still asking for the same thing and has said how the draft missed it — or
// has forwarded the reviewer's findings, which are kept apart from their own
// words so the journal never attributes a model's objection to the person.
// The intent reopens to pending: it stays open until a run in its chain is
// accepted, rejected or discarded. Idempotent per run.
func AppendIntentRevision(projectRoot, intentID, runID, submittedAt, note, findings string) (int, error) {
	doc, err := EnsureIntent(projectRoot)
	if err != nil {
		return 0, err
	}
	sec := doc.Section(intentID)
	if sec == nil {
		return 0, fmt.Errorf("intent %s not found", intentID)
	}
	runs := intentRevisionRuns(sec)
	for i, run := range runs {
		if run == runID {
			return i + 1, nil
		}
	}
	if strings.EqualFold(strings.TrimSpace(sec.Field("outcome")), "accepted") {
		return 0, fmt.Errorf("intent %s is already accepted; a change to it is a new intention", intentID)
	}
	runs = append(runs, runID)
	n := len(runs)
	body := setIntentHeaderField(sec.Body, "Revisions", strings.Join(runs, ", "))
	body = setIntentField(body, "Outcome", "pending")
	body = strings.TrimRight(body, "\n") + "\n\n" + renderIntentRevision(n, runID, submittedAt, note, findings)
	sec.Body = body
	parseSectionFields(sec, withoutIntentText(sec.Body), KindIntent, SectionScope)
	return n, writeIntent(projectRoot, doc)
}

// Headings of the revision journal. The desktop parses the same words; keep
// them in step with frontend/src/lib/intent.ts.
const (
	intentRevisionHeading = "### Revision"
	intentNoteHeading     = "#### The person's note"
	intentFindingsHeading = "#### Reviewer findings carried over"
)

func renderIntentRevision(n int, runID, submittedAt, note, findings string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d — %s\n\nSubmitted at %s.\n", intentRevisionHeading, n, runID, submittedAt)
	note, findings = strings.TrimSpace(note), strings.TrimSpace(findings)
	if note != "" {
		b.WriteString("\n" + intentNoteHeading + "\n\n" + fenceIntentText("note", note) + "\n")
	}
	if findings != "" {
		b.WriteString("\n" + intentFindingsHeading + "\n\n" + fenceIntentText("findings", findings) + "\n")
	}
	if note == "" && findings == "" {
		b.WriteString("\n_Nothing new: the revision repeated earlier notes._\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// intentRevisionRuns lists the runs that revised this intent, oldest first.
// Sections written before B-521 have no such field and no revisions.
func intentRevisionRuns(sec *Section) []string {
	var out []string
	for _, item := range strings.Split(sec.Field("revisions"), ",") {
		if item = strings.TrimSpace(item); item != "" && item != "-" {
			out = append(out, item)
		}
	}
	return out
}

func intentRuns(sec *Section) []string {
	runs := []string{}
	if run := strings.TrimSpace(sec.Field("run")); run != "" {
		runs = append(runs, run)
	}
	return append(runs, intentRevisionRuns(sec)...)
}

// intentHead is the newest run in the intent's chain.
func intentHead(sec *Section) string {
	runs := intentRuns(sec)
	if len(runs) == 0 {
		return ""
	}
	return runs[len(runs)-1]
}

func intentIndexForRun(doc *Document, runID string) int {
	if runID == "" {
		return -1
	}
	for i := range doc.Sections {
		for _, run := range intentRuns(&doc.Sections[i]) {
			if run == runID {
				return i
			}
		}
	}
	return -1
}

func writeIntent(projectRoot string, doc *Document) error {
	doc.Front.Kind = KindIntent
	doc.Front.ApprovedBy = "human"
	doc.Front.Version++
	doc.Front.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := os.MkdirAll(DocsDir(projectRoot), 0o755); err != nil {
		return err
	}
	return xplat.AtomicWrite(Path(projectRoot, KindIntent), []byte(Render(doc)), 0o644)
}

func setIntentField(body, label, value string) string {
	if replaced, ok := replaceIntentField(body, label, value); ok {
		return replaced
	}
	return "**" + label + ":** " + value + "\n" + body
}

func replaceIntentField(body, label, value string) (string, bool) {
	prefix := "**" + label + ":**"
	lines := strings.Split(body, "\n")
	var fence intentFence
	for i, line := range lines {
		// A field line inside a person's words is their text, not ours.
		if fence.opaque(line) {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			lines[i] = prefix + " " + value
			return strings.Join(lines, "\n"), true
		}
	}
	return body, false
}

// setIntentHeaderField sets a field of an intent entry's header block; a new
// field joins the end of that block (Revisions after Requirements) instead of
// the top of the entry.
func setIntentHeaderField(body, label, value string) string {
	if replaced, ok := replaceIntentField(body, label, value); ok {
		return replaced
	}
	prefix := "**" + label + ":**"
	lines := strings.Split(body, "\n")
	last := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "**") && strings.Contains(trimmed, ":**") {
			last = i
			continue
		}
		if trimmed != "" || last >= 0 {
			break
		}
	}
	if last < 0 {
		return setIntentField(body, label, value)
	}
	out := append(append(append([]string{}, lines[:last+1]...), prefix+" "+value), lines[last+1:]...)
	return strings.Join(out, "\n")
}

func idsInField(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if looksLikeID(item) {
			out = append(out, item)
		}
	}
	return out
}
