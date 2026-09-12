package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Run history for the consultant.
//
// The chat's whole point is answering "why didn't T-097 pass?" — and the
// consultant was blind to exactly that: run records live under .ducklab,
// which the filesystem denylist rightly protects, so it groped at
// fs_list/.ducklab/runs and got refused, twice, on the record. These are the
// front doors: the same state.json and events.jsonl the desktop renders,
// summarized to what a reader infers history from — who ran, what the gate
// said, what the reviewer said, how it ended.

// RunListTool lists a project's runs, newest first.
type RunListTool struct{}

func (t *RunListTool) Name() string   { return "run_list" }
func (t *RunListTool) Mutating() bool { return false }

func (t *RunListTool) Description() string {
	return "List this project's runs, newest first: id, stage, task, status, verdict. Filter by task id to trace one task's history."
}

func (t *RunListTool) Schema() interface{} {
	return NewSchema().
		AddString("task", "Only runs for this task id (optional)", false).
		AddInt("limit", "Max runs to return (default 20)", false)
}

func (t *RunListTool) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a struct {
		Task  string `json:"task"`
		Limit int    `json:"limit"`
	}
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	if a.Limit <= 0 || a.Limit > 100 {
		a.Limit = 20
	}
	dir := filepath.Join(ectx.Docs(), ".ducklab", "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ErrorResult("no runs recorded yet"), nil
	}
	type row struct {
		id, stage, task, status, verdict, started string
	}
	var rows []row
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		st, err := os.ReadFile(filepath.Join(dir, e.Name(), "state.json"))
		if err != nil {
			continue
		}
		var s struct {
			ID      string `json:"id"`
			Stage   string `json:"stage"`
			TaskID  string `json:"task_id"`
			Status  string `json:"status"`
			Verdict string `json:"verdict"`
			Started string `json:"started_at"`
		}
		if json.Unmarshal(st, &s) != nil {
			continue
		}
		if a.Task != "" && s.TaskID != a.Task {
			continue
		}
		rows = append(rows, row{s.ID, s.Stage, s.TaskID, s.Status, s.Verdict, s.Started})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].started > rows[j].started })
	if len(rows) > a.Limit {
		rows = rows[:a.Limit]
	}
	if len(rows) == 0 {
		return SuccessResult("no matching runs"), nil
	}
	var b strings.Builder
	for _, r := range rows {
		task := r.task
		if task == "" {
			task = "-"
		}
		verdict := r.verdict
		if verdict == "" {
			verdict = "-"
		}
		fmt.Fprintf(&b, "%s  %s  %s  %s  %s  %s\n", r.id, r.stage, task, r.status, verdict, r.started)
	}
	return SuccessResult("id  stage  task  status  verdict  started\n%s", b.String()), nil
}

// RunReadTool summarizes one run's record: turns, verdicts, gates, failure.
type RunReadTool struct{}

func (t *RunReadTool) Name() string   { return "run_read" }
func (t *RunReadTool) Mutating() bool { return false }

func (t *RunReadTool) Description() string {
	return "Read one run's record: who took each turn, reviewer verdicts and findings, gate results, how it ended. Use run_list first to find the id."
}

func (t *RunReadTool) Schema() interface{} {
	return NewSchema().AddString("id", "Run id, e.g. r-20260811-224844-ebl7", true)
}

func (t *RunReadTool) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	summary, err := ReadRunSummary(ectx.Docs(), a.ID)
	if err != nil {
		return ErrorResult("%v", err), nil
	}
	return SuccessResult("%s", summary), nil
}

// ReadRunSummary is the authoritative reading of a run record used by both the
// run_read tool and deterministic consultant dossiers. Keeping one formatter
// prevents the chat door from describing a different run than the tool the
// consultant can call while investigating it.
func ReadRunSummary(projectRoot, runID string) (string, error) {
	// The id is a directory name here; a path would escape the record.
	id := filepath.Base(strings.TrimSpace(runID))
	dir := filepath.Join(projectRoot, ".ducklab", "runs", id)
	st, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return "", fmt.Errorf("no run %q — run_list shows what exists", id)
	}
	var s struct {
		Stage    string                 `json:"stage"`
		Mode     string                 `json:"mode"`
		TaskID   string                 `json:"task_id"`
		Status   string                 `json:"status"`
		Verdict  string                 `json:"verdict"`
		Accepted bool                   `json:"accepted"`
		Failure  string                 `json:"failure"`
		Origin   string                 `json:"origin"`
		Autonomy string                 `json:"autonomy"`
		Pending  map[string]interface{} `json:"pending_data"`
	}
	if err := json.Unmarshal(st, &s); err != nil {
		return "", fmt.Errorf("unreadable record: %v", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %s %s", id, s.Stage, s.Mode)
	if s.TaskID != "" {
		fmt.Fprintf(&b, " · %s", s.TaskID)
	}
	fmt.Fprintf(&b, "\nstatus: %s · verdict: %s · accepted: %v", s.Status, s.Verdict, s.Accepted)
	if s.Origin != "" {
		fmt.Fprintf(&b, " · origin: %s", s.Origin)
	}
	if s.Autonomy != "" {
		fmt.Fprintf(&b, " · autonomy: %s", s.Autonomy)
	}
	b.WriteString("\n")
	if s.Failure != "" {
		fmt.Fprintf(&b, "failure: %s\n", s.Failure)
	}
	if d, ok := s.Pending["detail"].(string); ok && d != "" {
		fmt.Fprintf(&b, "pending: %s\n", d)
	}

	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err == nil {
		var timeline []string
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			var e struct {
				Type string                 `json:"type"`
				Data map[string]interface{} `json:"data"`
			}
			if json.Unmarshal([]byte(line), &e) != nil {
				continue
			}
			if entry := runTimelineEntry(e.Type, e.Data); entry != "" {
				timeline = append(timeline, entry)
			}
		}
		if len(timeline) > 0 {
			b.WriteString("\n### timeline\n")
			b.WriteString(strings.Join(timeline, "\n"))
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// ReadRunSummaryForPrompt keeps the state/failure header and complete recent
// timeline entries within maxBytes. A verdict and its findings are one entry:
// the cut never leaves an orphan finding without the verdict that owns it.
// A long gate log or repair loop must not consume a small consultant's context
// before it can answer; run_read remains available for older evidence.
func ReadRunSummaryForPrompt(projectRoot, runID string, maxBytes int) (string, error) {
	summary, err := ReadRunSummary(projectRoot, runID)
	if err != nil || maxBytes <= 0 || len(summary) <= maxBytes {
		return summary, err
	}
	const marker = "\n### timeline\n"
	header, timeline, ok := strings.Cut(summary, marker)
	if !ok {
		return boundRunSummaryHeader(summary, maxBytes), nil
	}
	header = boundRunSummaryHeader(header, maxBytes/2)
	entries := splitRunTimelineEntries(timeline)
	// Reserve enough room for the exact omitted marker before selecting whole
	// entries from newest to oldest.
	omittedLine := fmt.Sprintf("- … %d earlier timeline entries omitted; use run_read if needed\n", len(entries))
	available := maxBytes - len(header) - len(marker) - len(omittedLine)
	if available < 0 {
		available = 0
	}
	start, used := len(entries), 0
	for start > 0 {
		entryBytes := len(entries[start-1]) + 1
		if used+entryBytes > available {
			break
		}
		start--
		used += entryBytes
	}
	omitted := start
	if omitted == 0 {
		omittedLine = ""
	} else {
		omittedLine = fmt.Sprintf("- … %d earlier timeline entries omitted; use run_read if needed\n", omitted)
	}
	return header + marker + omittedLine + strings.Join(entries[start:], "\n") + "\n", nil
}

func runTimelineEntry(eventType string, d map[string]interface{}) string {
	var b strings.Builder
	switch eventType {
	case "turn_start":
		fmt.Fprintf(&b, "- R%v %v: %v (%v)", d["round"], d["role"], d["duckling"], d["turn"])
	case "message":
		if v, ok := d["verdict"].(string); ok && v != "" {
			n := 0
			if fs, ok := d["findings"].([]interface{}); ok {
				n = len(fs)
			}
			fmt.Fprintf(&b, "- R%v reviewer verdict: %s (%d findings)", d["round"], v, n)
			if fs, ok := d["findings"].([]interface{}); ok {
				for i, f := range fs {
					if i >= 5 {
						fmt.Fprintf(&b, "\n    … %d more", len(fs)-5)
						break
					}
					if fm, ok := f.(map[string]interface{}); ok {
						fmt.Fprintf(&b, "\n    - [%v] %v", fm["severity"], truncate(compactLine(fmt.Sprint(fm["issue"])), 200))
					}
				}
			}
		} else if c, ok := d["content"].(string); ok {
			if protocol, tools := summarizeToolProtocol(c); protocol {
				fmt.Fprintf(&b, "- R%v %v emitted tool protocol instead of prose", d["round"], d["role"])
				if len(tools) > 0 {
					fmt.Fprintf(&b, " (tools: %s)", strings.Join(tools, ", "))
				}
			} else {
				fmt.Fprintf(&b, "- R%v %v said: %s", d["round"], d["role"], truncate(compactLine(c), 300))
			}
		}
	case "round_gate":
		fmt.Fprintf(&b, "- R%v gate: %v", d["round"], d["result"])
	case "gate":
		exit := d["exit"]
		if exit == nil {
			exit = d["exit_code"]
		}
		command := d["cmd"]
		if command == nil {
			command = d["command"]
		}
		fmt.Fprintf(&b, "- gate exit %v: %v", exit, truncate(compactLine(fmt.Sprint(command)), 120))
	case "verdict":
		fmt.Fprintf(&b, "- verdict: %v", d["verdict"])
		if detail := strings.TrimSpace(fmt.Sprint(d["detail"])); detail != "" && detail != "<nil>" {
			fmt.Fprintf(&b, " — %v", truncate(compactLine(detail), 200))
		}
	case "human_needed":
		fmt.Fprintf(&b, "- waiting for a human: %v", d["kind"])
		if detail := strings.TrimSpace(fmt.Sprint(d["detail"])); detail != "" && detail != "<nil>" {
			fmt.Fprintf(&b, " %v", truncate(compactLine(detail), 200))
		}
	case "error":
		if detail := strings.TrimSpace(fmt.Sprint(d["error"])); detail != "" && detail != "<nil>" {
			fmt.Fprintf(&b, "- error: %v", truncate(compactLine(detail), 200))
		}
	case "run_end":
		fmt.Fprintf(&b, "- ended: %v", d["verdict"])
	}
	return b.String()
}

var toolProtocolName = regexp.MustCompile(`(?i)(?:invoke\s+name=|"(?:tool|name)"\s*:)\s*["']([^"']+)["']`)

func summarizeToolProtocol(content string) (bool, []string) {
	trimmed := strings.TrimSpace(content)
	lower := strings.ToLower(trimmed)
	protocol := strings.HasPrefix(trimmed, "<｜｜DSML｜｜") ||
		strings.HasPrefix(lower, "<tool_call") ||
		strings.HasPrefix(lower, "<function_calls") ||
		strings.HasPrefix(lower, "```ducklab")
	if !protocol {
		return false, nil
	}
	seen := map[string]bool{}
	var names []string
	for _, match := range toolProtocolName.FindAllStringSubmatch(trimmed, -1) {
		name := strings.TrimSpace(match[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) == 4 {
			break
		}
	}
	return true, names
}

func compactLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func splitRunTimelineEntries(timeline string) []string {
	var entries []string
	for _, line := range strings.Split(strings.TrimSuffix(timeline, "\n"), "\n") {
		if strings.HasPrefix(line, "- ") {
			entries = append(entries, line)
		} else if len(entries) > 0 {
			entries[len(entries)-1] += "\n" + line
		}
	}
	return entries
}

func boundRunSummaryHeader(header string, maxBytes int) string {
	if maxBytes <= 0 || len(header) <= maxBytes {
		return header
	}
	// Preserve identity and state (the first two lines), then the tail of the
	// failure/pending detail where compilers and gates put the decisive error.
	first := strings.IndexByte(header, '\n')
	second := -1
	if first >= 0 {
		if next := strings.IndexByte(header[first+1:], '\n'); next >= 0 {
			second = first + 1 + next
		}
	}
	if second < 0 {
		return header[:maxBytes]
	}
	prefix := header[:second+1]
	marker := "failure/details: … earlier bytes omitted; use run_read for the full record\n"
	tailBytes := maxBytes - len(prefix) - len(marker)
	if tailBytes <= 0 {
		return prefix[:min(len(prefix), maxBytes)]
	}
	tail := header[len(header)-tailBytes:]
	if newline := strings.IndexByte(tail, '\n'); newline >= 0 && newline+1 < len(tail) {
		tail = tail[newline+1:]
	}
	return prefix + marker + tail
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
