package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/report"
	"github.com/jrullan/ducklab/internal/runlog"
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
		AddInt("limit", "Max runs to return (default 20, maximum 100)", false).
		AddInt("offset", "Number of matching runs to skip (for pagination)", false)
}

func (t *RunListTool) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a struct {
		Task   string `json:"task"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	if a.Limit <= 0 {
		a.Limit = 20
	}
	if a.Limit > 100 {
		a.Limit = 100
	}
	if a.Offset < 0 {
		a.Offset = 0
	}
	dir := filepath.Join(ectx.ProjectRoot, ".ducklab", "runs")
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
	total := len(rows)
	if a.Offset >= total {
		return SuccessResult("showing 0 of %d; no more runs", total), nil
	}
	rows = rows[a.Offset:]
	if len(rows) > a.Limit {
		rows = rows[:a.Limit]
	}
	if len(rows) == 0 {
		return SuccessResult("showing 0 of %d; no more runs", total), nil
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
	line := fmt.Sprintf("showing %d of %d", len(rows), total)
	if a.Offset+len(rows) < total {
		line += fmt.Sprintf("; use offset %d to continue", a.Offset+len(rows))
	}
	return SuccessResult("%s\nid  stage  task  status  verdict  started\n%s", line, b.String()), nil
}

// ProjectStatsTool returns compact, engine-computed project aggregates.
type ProjectStatsTool struct{}

func (t *ProjectStatsTool) Name() string   { return "project_stats" }
func (t *ProjectStatsTool) Mutating() bool { return false }
func (t *ProjectStatsTool) Description() string {
	return "Read engine-computed project statistics including first-run pass rate, acceptance rate, cost totals, and totals by mode and duckling."
}
func (t *ProjectStatsTool) Schema() interface{} { return NewSchema() }
func (t *ProjectStatsTool) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	runs, err := readRuns(ectx.ProjectRoot)
	if err != nil {
		return SuccessResult("no runs recorded yet"), nil
	}
	return SuccessResult("%s", report.RenderProjectStats(report.BuildProjectStats(runs))), nil
}
func readRuns(root string) ([]*runlog.Run, error) {
	entries, err := os.ReadDir(filepath.Join(root, ".ducklab", "runs"))
	if err != nil {
		return nil, err
	}
	var runs []*runlog.Run
	for _, e := range entries {
		if e.IsDir() {
			data, err := os.ReadFile(filepath.Join(root, ".ducklab", "runs", e.Name(), "state.json"))
			if err == nil {
				var r runlog.Run
				if json.Unmarshal(data, &r) == nil {
					runs = append(runs, &r)
				}
			}
		}
	}
	return runs, nil
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
	// The id is a directory name here; a path would escape the record.
	id := filepath.Base(strings.TrimSpace(a.ID))
	dir := filepath.Join(ectx.ProjectRoot, ".ducklab", "runs", id)
	st, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return ErrorResult("no run %q — run_list shows what exists", id), nil
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
		return ErrorResult("unreadable record: %v", err), nil
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
		b.WriteString("\n### timeline\n")
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
			d := e.Data
			switch e.Type {
			case "turn_start":
				fmt.Fprintf(&b, "- R%v %v: %v (%v)\n", d["round"], d["role"], d["duckling"], d["turn"])
			case "message":
				if v, ok := d["verdict"].(string); ok && v != "" {
					n := 0
					if fs, ok := d["findings"].([]interface{}); ok {
						n = len(fs)
					}
					fmt.Fprintf(&b, "- R%v reviewer verdict: %s (%d findings)\n", d["round"], v, n)
					if fs, ok := d["findings"].([]interface{}); ok {
						for i, f := range fs {
							if i >= 5 {
								fmt.Fprintf(&b, "    … %d more\n", len(fs)-5)
								break
							}
							if fm, ok := f.(map[string]interface{}); ok {
								fmt.Fprintf(&b, "    - [%v] %v\n", fm["severity"], truncate(fmt.Sprint(fm["issue"]), 200))
							}
						}
					}
				} else if c, ok := d["content"].(string); ok {
					fmt.Fprintf(&b, "- R%v %v said: %s\n", d["round"], d["role"], truncate(c, 300))
				}
			case "round_gate":
				fmt.Fprintf(&b, "- R%v gate: %v\n", d["round"], d["result"])
			case "gate":
				fmt.Fprintf(&b, "- gate exit %v: %v\n", d["exit"], truncate(fmt.Sprint(d["cmd"]), 120))
			case "verdict":
				fmt.Fprintf(&b, "- verdict: %v — %v\n", d["verdict"], truncate(fmt.Sprint(d["detail"]), 200))
			case "human_needed":
				fmt.Fprintf(&b, "- waiting for a human: %v %v\n", d["kind"], truncate(fmt.Sprint(d["detail"]), 200))
			case "error":
				fmt.Fprintf(&b, "- error: %v\n", truncate(fmt.Sprint(d["error"]), 200))
			case "run_end":
				fmt.Fprintf(&b, "- ended: %v\n", d["verdict"])
			}
		}
	}
	return SuccessResult("%s", b.String()), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
