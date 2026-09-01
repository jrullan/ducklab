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
		seen[sec.Field("run")] = true
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
		if json.Unmarshal(state, &run) != nil || run.Stage != "intake" || seen[run.ID] {
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
	body += "\n### Original brief\n\n" + strings.TrimSpace(brief)
	section := Section{ID: id, Title: title, Body: body}
	parseSectionFields(&section, body)
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
	for _, sec := range intent.Sections {
		if sec.Field("run") == runID {
			return sec.ID, nil
		}
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
func ResolveIntent(projectRoot, runID, outcome string, requirements []string) error {
	doc, err := EnsureIntent(projectRoot)
	if err != nil {
		return err
	}
	changed := false
	for i := range doc.Sections {
		if doc.Sections[i].Field("run") != runID {
			continue
		}
		doc.Sections[i].Body = setIntentField(doc.Sections[i].Body, "Outcome", outcome)
		if len(requirements) > 0 {
			doc.Sections[i].Body = setIntentField(doc.Sections[i].Body, "Requirements", strings.Join(requirements, ", "))
		}
		changed = true
		break
	}
	if !changed {
		return nil
	}
	return writeIntent(projectRoot, doc)
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
	prefix := "**" + label + ":**"
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			lines[i] = prefix + " " + value
			return strings.Join(lines, "\n")
		}
	}
	return prefix + " " + value + "\n" + body
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
