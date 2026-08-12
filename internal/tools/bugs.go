package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/store"
)

// The bug tools open the project's own database exactly the way the service
// does — open, migrate, close, per operation — so a tool call and an HTTP
// call are the same kind of visitor, never a second long-lived owner.
func openBugDB(projectRoot string) (*store.DB, error) {
	db, err := store.Open(filepath.Join(projectRoot, ".ducklab", "ducklab.db"))
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// BugRead reads the bug board: one bug in full, or the whole list compactly.
//
// Named in the triager's ceiling and the chat toolbelt since both existed —
// and never implemented, so the registry silently dropped it from every belt
// that asked. A consultant told to "check whether a bug already covers this"
// had no way to look.
type BugRead struct{}

func (t *BugRead) Name() string   { return "bug_read" }
func (t *BugRead) Mutating() bool { return false }

func (t *BugRead) Description() string {
	return "Read the project's bug board. With an id, the full record of that bug; without, a compact list of every bug (id, severity, status, title) — use the list to check for duplicates before filing."
}

func (t *BugRead) Schema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{
				"type":        "string",
				"description": "A bug id like B-003. Omit to list every bug.",
			},
		},
	}
}

func (t *BugRead) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var req struct {
		ID string `json:"id"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &req); err != nil {
			return &Result{IsError: true, Content: fmt.Sprintf("bad arguments: %v", err)}, nil
		}
	}
	db, err := openBugDB(ectx.ProjectRoot)
	if err != nil {
		return &Result{IsError: true, Content: fmt.Sprintf("open bug board: %v", err)}, nil
	}
	defer db.Close()

	if id := strings.TrimSpace(req.ID); id != "" {
		rec, err := db.GetBug(id)
		if err != nil {
			return &Result{IsError: true, Content: fmt.Sprintf("no bug %s", id)}, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s [%s, %s] %s\n", rec.ID, rec.Severity, rec.Status, rec.Title)
		if rec.TaskID != "" {
			fmt.Fprintf(&b, "task: %s\n", rec.TaskID)
		}
		if rec.DuplicateOf != "" {
			fmt.Fprintf(&b, "duplicate of: %s\n", rec.DuplicateOf)
		}
		if rec.Component != "" {
			fmt.Fprintf(&b, "component: %s\n", rec.Component)
		}
		if rec.Body != "" {
			fmt.Fprintf(&b, "\n%s\n", rec.Body)
		}
		// The audit trail answers the question a bare status can't: WHO put
		// it there. A triager deciding whether a reopened report is a person's
		// deliberate call or a stale sweep reads it here.
		if hist := readBugHistory(ectx.ProjectRoot, rec.ID); len(hist) > 0 {
			b.WriteString("\nhistory:\n")
			for _, h := range hist {
				b.WriteString("  " + h + "\n")
			}
		}
		return &Result{Content: b.String()}, nil
	}

	rows, err := db.ListBugs()
	if err != nil {
		return &Result{IsError: true, Content: fmt.Sprintf("list bugs: %v", err)}, nil
	}
	if len(rows) == 0 {
		return &Result{Content: "The bug board is empty."}, nil
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s [%s, %s] %s\n", r.ID, r.Severity, r.Status, r.Title)
	}
	return &Result{Content: b.String()}, nil
}

// BugFile files a bug on the project's board.
//
// Born for the chat: a consultant that had verified every page, found a
// data-integrity bug and written the complete report ended with "Suggested
// next step: file a new bug" — and the person carried two thousand
// characters to the form by hand. When the loop can carry the finding, the
// finding goes by the loop.
//
// Deliberately in NO role's ceiling: it reaches a model only where a belt
// grants it explicitly (the chat's), so a stage architect can never file
// bugs mid-draft. The severity is taken as given, like every other reporter's
// (triage is where that judgement belongs).
type BugFile struct{}

func (t *BugFile) Name() string   { return "bug_file" }
func (t *BugFile) Mutating() bool { return true }

func (t *BugFile) Description() string {
	return "File a bug on the project's bug board. Use ONLY when the human has explicitly asked you to file it, never on your own initiative. Check bug_read first for an existing bug covering the same problem. Returns the new bug's id — report it back to the human."
}

func (t *BugFile) Schema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title": map[string]interface{}{
				"type":        "string",
				"description": "One line naming the defect",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "The full report: what happens, repro steps, expected behaviour, suspected files",
			},
			"severity": map[string]interface{}{
				"type":        "string",
				"description": "critical, high, normal or low (default normal)",
			},
		},
		"required": []string{"title", "body"},
	}
}

func (t *BugFile) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var req struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{IsError: true, Content: fmt.Sprintf("bad arguments: %v", err)}, nil
	}
	if strings.TrimSpace(req.Title) == "" {
		return &Result{IsError: true, Content: "a bug needs a title"}, nil
	}
	sev := strings.ToLower(strings.TrimSpace(req.Severity))
	if sev == "" {
		sev = string(bug.Normal)
	}
	if !bug.ValidSeverity(sev) {
		return &Result{IsError: true, Content: fmt.Sprintf("unknown severity %q, want critical, high, normal or low", req.Severity)}, nil
	}

	db, err := openBugDB(ectx.ProjectRoot)
	if err != nil {
		return &Result{IsError: true, Content: fmt.Sprintf("open bug board: %v", err)}, nil
	}
	defer db.Close()

	n, err := db.NextSequence("bug", "B")
	if err != nil {
		return &Result{IsError: true, Content: fmt.Sprintf("allocate bug id: %v", err)}, nil
	}
	rec := &store.Bug{
		ID:       fmt.Sprintf("B-%03d", n),
		Title:    strings.TrimSpace(req.Title),
		Body:     req.Body,
		Severity: sev,
		Status:   string(bug.Open),
		// Provenance: filed from a conversation, by this model, in this run.
		Source:   "chat",
		Reporter: string(ectx.Duckling),
	}
	if err := db.CreateBug(rec); err != nil {
		return &Result{IsError: true, Content: fmt.Sprintf("file bug: %v", err)}, nil
	}
	return &Result{Content: fmt.Sprintf("Filed %s [%s]: %s", rec.ID, rec.Severity, rec.Title)}, nil
}

// readBugHistory renders one bug's audit lines, oldest first. Best-effort:
// a project from before the trail existed simply has none.
func readBugHistory(projectRoot, bugID string) []string {
	f, err := os.Open(filepath.Join(projectRoot, ".ducklab", "bugs", "audit.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e bug.AuditEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Bug != bugID {
			continue
		}
		line := fmt.Sprintf("%s  %s -> %s  by %s (%s)", e.TS, e.From, e.To, e.Actor, e.Via)
		if e.Note != "" {
			line += " " + e.Note
		}
		out = append(out, line)
	}
	return out
}
