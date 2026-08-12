package service

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/jrullan/ducklab/internal/bug"
)

// The bug audit trail: every status transition, signed.
//
// B-041 was moved from fixed back to in_progress six minutes after its task
// was accepted, and nobody — not the person, not the agent asked directly —
// could say who did it. The bug table keeps only the latest status and an
// updated_at; the transitions themselves were nowhere. Decisions and reports
// carry an actor; moves were the one mutation that didn't.
//
// One JSONL per project, append-only, best-effort: an audit line that cannot
// be written must never block the move it describes — the move is the
// person's intent, the line is the receipt.

const bugAuditFile = "audit.jsonl"

func bugAuditPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".ducklab", "bugs", bugAuditFile)
}

func appendBugAudit(projectRoot string, e bug.AuditEntry) {
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	path := bugAuditPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

// readBugAudit returns each bug's transitions in file (chronological) order.
func readBugAudit(projectRoot string) map[string][]bug.AuditEntry {
	out := map[string][]bug.AuditEntry{}
	f, err := os.Open(bugAuditPath(projectRoot))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e bug.AuditEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Bug == "" {
			continue
		}
		out[e.Bug] = append(out[e.Bug], e)
	}
	return out
}
