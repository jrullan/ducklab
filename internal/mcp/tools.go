package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// toolList declares the operator surface. Every description tells the model
// how ducklab thinks, because the operator that reads `next` and reasons from
// results is the operator the contract was built for.
func toolList() []map[string]interface{} {
	obj := func(props map[string]interface{}, required ...string) map[string]interface{} {
		schema := map[string]interface{}{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	str := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	return []map[string]interface{}{
		{
			"name": "status",
			"description": "What needs a decision and what is running, across every project. " +
				"Start here. Each pending run carries `next`: the ONLY actions you may take on it. " +
				"Each project carries `next_steps` — the engine's own guidance, the same the desktop's " +
				"rail shows. When the human asks what to do, answer FROM next_steps: it already knows " +
				"that a triaged bug wants promoting and which task is buildable.",
			"inputSchema": obj(map[string]interface{}{}),
		},
		{
			"name": "run_get",
			"description": "One run's full result: status, verdict, failure, spend, the diff it " +
				"produced, and `next` — the legal actions. The verdict is the gate's, not yours " +
				"and not negotiable; your decision is whether to accept the work.",
			"inputSchema": obj(map[string]interface{}{"run_id": str("the run id, r-...")}, "run_id"),
		},
		{
			"name": "decide",
			"description": "Decide a run waiting at its gate. `action` must come from the run's " +
				"`next` list. `reason` is required and recorded — you are deciding as yourself, " +
				"and the record will say so. request_changes sends the draft back with your note.",
			"inputSchema": obj(map[string]interface{}{
				"run_id": str("the run id"),
				"action": str("one of the run's next actions: accept, reject, request_changes, resume, abort"),
				"reason": str("why, in one or two sentences; recorded with the decision"),
			}, "run_id", "action", "reason"),
		},
		{
			"name":        "answer",
			"description": "Answer a question a run asked (pending_kind=question in run_get).",
			"inputSchema": obj(map[string]interface{}{
				"run_id":      str("the run id"),
				"question_id": str("from the run's pending data"),
				"answer":      str("your answer"),
			}, "run_id", "answer"),
		},
		{
			"name":        "artifact_get",
			"description": "Read a project document: requirements, spec or plan — approved content plus any pending proposal.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"kind":       str("requirements | spec | plan"),
			}, "project_id", "kind"),
		},
		{
			"name": "task_list",
			"description": "The project's tasks with status and `next`. A task offering no run is " +
				"not startable — blocked tasks name what they wait on.",
			"inputSchema": obj(map[string]interface{}{"project_id": str("the project id")}, "project_id"),
		},
		{
			"name":        "run_start",
			"description": "Build a task. Mode defaults to the project's habit; solo|pair|tournament|split.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"task_id":    str("a task whose next includes run"),
				"mode":       str("optional mode"),
			}, "project_id", "task_id"),
		},
		{
			"name": "stage_start",
			"description": "Run a document stage: intake (brief or adopt), spec, plan. adopt=true " +
				"surveys an existing codebase into the requirements it already satisfies.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"stage":      str("intake | spec | plan"),
				"brief":      str("intake only: what to build, or context for adopt"),
				"adopt":      map[string]interface{}{"type": "boolean", "description": "intake only: survey the tree"},
			}, "project_id", "stage"),
		},
		{
			"name":        "bug_report",
			"description": "File a bug. Attach screenshots with bug_attach, then bug_triage classifies it, bug_promote turns it into a task, and test_start builds the fix.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"title":      str("one line"),
				"body":       str("what happened, what was expected"),
				"severity":   str("critical | high | normal | low (default normal)"),
			}, "project_id", "title", "body"),
		},
		{
			"name":        "bug_list",
			"description": "The project's bugs with status: open (untriaged), triaged, in_progress, fixed (waiting for a person to verify), verified, closed.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"open_only":  map[string]interface{}{"type": "boolean", "description": "only bugs not yet resolved"},
			}, "project_id"),
		},
		{
			"name": "bug_attach",
			"description": "Attach one image to a bug (8MB cap). Give `path` when the image is a file " +
				"on this machine — a saved chat download — and the server reads and encodes it itself; " +
				"never try to type base64 by hand, generated base64 arrives corrupted. The screenshot " +
				"says what a paragraph cannot: a vision triager is shown the pixels themselves.",
			"inputSchema": obj(map[string]interface{}{
				"project_id":  str("the project id"),
				"bug_id":      str("the bug, B-..."),
				"path":        str("absolute path to the image file on this machine (preferred)"),
				"filename":    str("optional label; defaults to the file's own name"),
				"data_base64": str("raw base64, ONLY when no file exists on disk"),
			}, "project_id", "bug_id"),
		},
		{
			"name": "bug_triage",
			"description": "Classify bugs: severity, duplicates, suspected files. One bug when bug_id is " +
				"given, every open one otherwise. The classifications are proposals on a run that may " +
				"wait at its gate — check status and decide it like any other.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"bug_id":     str("optional: triage exactly this bug"),
			}, "project_id"),
		},
		{
			"name":        "bug_promote",
			"description": "Turn a triaged bug into a task on the board. The task carries the report and the triage's analysis.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"bug_id":     str("a triaged bug, B-..."),
			}, "project_id", "bug_id"),
		},
		{
			"name": "bug_move",
			"description": "Move a bug between states — most importantly fixed→verified, the judgement " +
				"that the report is actually answered, which no model makes alone (I2). Only move to " +
				"verified when the human you speak for has confirmed it.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"bug_id":     str("the bug, B-..."),
				"status":     str("target status, e.g. verified | closed"),
			}, "project_id", "bug_id", "status"),
		},
		{
			"name": "app",
			"description": "The application under development: status shows its run command and " +
				"whether it is up; start launches it as an engine-managed process; stop kills it. " +
				"Useful before reproducing a bug — the report is better when the app was actually running.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"action":     str("status | start | stop"),
			}, "project_id", "action"),
		},
		{
			"name": "test_start",
			"description": "The TDD chain for a task: a model writes the FAILING test first; it lands red, " +
				"is committed, and the build runs against it. then_build defaults true — one authorization, " +
				"decided at the build's gate with the committed test in the diff. This is the primary way " +
				"to build a task; run_start alone skips the test discipline.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"task_id":    str("a startable task, T-..."),
				"then_build": map[string]interface{}{"type": "boolean", "description": "chain the build when the test lands red (default true)"},
			}, "project_id", "task_id"),
		},
	}
}

type args map[string]interface{}

func (a args) str(k string) string {
	v, _ := a[k].(string)
	return v
}

func (s *Server) call(name string, raw json.RawMessage) (map[string]interface{}, error) {
	var a args
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("arguments did not parse: %v", err)
		}
	}
	switch name {
	case "status":
		return s.status()
	case "run_get":
		return s.runGet(a.str("run_id"))
	case "decide":
		return s.decide(a.str("run_id"), a.str("action"), a.str("reason"))
	case "answer":
		if err := s.eng.RunAnswer(a.str("run_id"), a.str("question_id"), a.str("answer")); err != nil {
			return nil, err
		}
		return toolText("answered", false), nil
	case "artifact_get":
		doc, err := s.eng.ArtifactGet(a.str("project_id"), a.str("kind"))
		if err != nil {
			return nil, err
		}
		return toolJSON(doc), nil
	case "task_list":
		tasks, err := s.eng.TaskList(a.str("project_id"))
		if err != nil {
			return nil, err
		}
		return toolJSON(tasks), nil
	case "run_start":
		req := map[string]interface{}{"task_id": a.str("task_id")}
		if m := a.str("mode"); m != "" {
			req["mode"] = m
		}
		run, err := s.eng.RunStart(a.str("project_id"), req)
		if err != nil {
			return nil, err
		}
		return toolJSON(run), nil
	case "stage_start":
		req := map[string]interface{}{}
		if b := a.str("brief"); b != "" {
			req["from"] = b
		}
		if adopt, _ := a["adopt"].(bool); adopt {
			req["adopt"] = true
		}
		run, err := s.eng.StageStart(a.str("project_id"), a.str("stage"), req)
		if err != nil {
			return nil, err
		}
		return toolJSON(run), nil
	case "bug_report":
		// Attributed: the record must say WHO filed it, and "mcp:elena" is
		// auditable where an empty reporter is a shrug.
		req := map[string]string{
			"title": a.str("title"), "body": a.str("body"),
			"reporter": "mcp:" + s.client, "source": "mcp",
		}
		if sev := a.str("severity"); sev != "" {
			req["severity"] = sev
		}
		bug, err := s.eng.BugAdd(a.str("project_id"), req)
		if err != nil {
			return nil, err
		}
		return toolJSON(bug), nil
	case "bug_list":
		open, _ := a["open_only"].(bool)
		bugs, err := s.eng.BugList(a.str("project_id"), open)
		if err != nil {
			return nil, err
		}
		return toolJSON(bugs), nil
	case "bug_attach":
		data := a.str("data_base64")
		filename := a.str("filename")
		if p := a.str("path"); p != "" {
			// The model names a file; the server carries the bytes. A model
			// cannot emit hundreds of kilobytes of base64 token by token
			// without corrupting them — the first field agent to try proved
			// it within the hour.
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil, fmt.Errorf("read %s: %v", p, err)
			}
			if len(raw) > 8<<20 {
				return nil, fmt.Errorf("%s is %d bytes; the cap is 8MB — attach a screenshot, not a recording", p, len(raw))
			}
			data = base64.StdEncoding.EncodeToString(raw)
			if filename == "" {
				filename = filepath.Base(p)
			}
		}
		if data == "" {
			return nil, fmt.Errorf("give path (preferred) or data_base64")
		}
		items, err := s.eng.BugAttach(a.str("project_id"), a.str("bug_id"), filename, data)
		if err != nil {
			return nil, err
		}
		return toolJSON(items), nil
	case "bug_triage":
		run, err := s.eng.BugTriage(a.str("project_id"), a.str("bug_id"))
		if err != nil {
			return nil, err
		}
		return toolJSON(run), nil
	case "bug_promote":
		out, err := s.eng.BugPromote(a.str("project_id"), a.str("bug_id"))
		if err != nil {
			return nil, err
		}
		return toolJSON(out), nil
	case "bug_move":
		out, err := s.eng.BugMove(a.str("project_id"), a.str("bug_id"), a.str("status"))
		if err != nil {
			return nil, err
		}
		return toolJSON(out), nil
	case "app":
		switch a.str("action") {
		case "status":
			st, err := s.eng.AppStatus(a.str("project_id"))
			if err != nil {
				return nil, err
			}
			return toolJSON(st), nil
		case "start":
			st, err := s.eng.AppStart(a.str("project_id"))
			if err != nil {
				return nil, err
			}
			return toolJSON(st), nil
		case "stop":
			if err := s.eng.AppStop(a.str("project_id")); err != nil {
				return nil, err
			}
			return toolText("stopped", false), nil
		default:
			return nil, fmt.Errorf("action must be status, start or stop")
		}
	case "test_start":
		then := true
		if v, ok := a["then_build"].(bool); ok {
			then = v
		}
		run, err := s.eng.TestStart(a.str("project_id"), a.str("task_id"), "", then)
		if err != nil {
			return nil, err
		}
		return toolJSON(run), nil
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

// status is the operator's Now inbox: what waits, what runs, per project.
func (s *Server) status() (map[string]interface{}, error) {
	projects, err := s.eng.ProjectList()
	if err != nil {
		return nil, err
	}
	out := []map[string]interface{}{}
	for _, p := range projects {
		id, _ := p["id"].(string)
		if id == "" {
			continue
		}
		runs, err := s.eng.RunList(id)
		if err != nil {
			continue
		}
		var waiting, active []map[string]interface{}
		for _, r := range runs {
			switch r["status"] {
			case "paused":
				waiting = append(waiting, pick(r, "id", "stage", "task_id", "pending_kind", "verdict", "next"))
			case "running", "queued":
				active = append(active, pick(r, "id", "stage", "task_id", "status"))
			}
		}
		entry := map[string]interface{}{
			"project": id, "name": p["name"],
			"waiting_for_decision": waiting,
			"running":              active,
		}
		// The guide's ordered steps: without them an operator reads a bug's
		// raw status transitions and answers "in_progress, duplicate or
		// wontfix" to a human whose actual next move was "promote it".
		if steps, err := s.eng.ProjectNext(id); err == nil && len(steps) > 0 {
			entry["next_steps"] = steps
		}
		out = append(out, entry)
	}
	return toolJSON(out), nil
}

func (s *Server) runGet(id string) (map[string]interface{}, error) {
	run, err := s.eng.RunGet(id)
	if err != nil {
		return nil, err
	}
	view := pick(run, "id", "project_id", "stage", "mode", "task_id", "status", "verdict",
		"failure", "resolution", "pending_kind", "pending_data", "next", "budget", "warning")
	if diff, _, _, dErr := s.eng.RunDiff(id); dErr == nil && diff != "" {
		view["diff"] = diff
	}
	return toolJSON(view), nil
}

// decide maps the operator's verdict onto the engine's endpoints, attributed.
// The action must be one the engine offered — read from the run, not trusted
// from the model — so an operator can never take an action a person could not.
func (s *Server) decide(runID, action, reason string) (map[string]interface{}, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("a decision needs a reason; it is recorded with your name")
	}
	run, err := s.eng.RunGet(runID)
	if err != nil {
		return nil, err
	}
	if !offered(run, action) {
		return nil, fmt.Errorf("%q is not among this run's legal actions %v — read run_get and pick from next",
			action, run["next"])
	}
	actor := "mcp:" + s.client
	switch action {
	case "accept":
		res, err := s.eng.RunAcceptAs(runID, reason, actor)
		if err != nil {
			return nil, err
		}
		return toolJSON(res), nil
	case "reject":
		if err := s.eng.RunReject(runID, actor+": "+reason); err != nil {
			return nil, err
		}
		return toolText("rejected", false), nil
	case "request_changes":
		projectID, _ := run["project_id"].(string)
		stage, _ := run["stage"].(string)
		out, err := s.eng.StageStart(projectID, stage, map[string]interface{}{
			"revise": reason,
		})
		if err != nil {
			return nil, err
		}
		return toolJSON(out), nil
	case "resume":
		out, err := s.eng.RunResume(runID)
		if err != nil {
			return nil, err
		}
		return toolJSON(out), nil
	case "abort":
		if err := s.eng.RunAbort(runID); err != nil {
			return nil, err
		}
		return toolText("aborted", false), nil
	default:
		return nil, fmt.Errorf("unknown action %q", action)
	}
}

func offered(run map[string]interface{}, action string) bool {
	next, _ := run["next"].([]interface{})
	for _, n := range next {
		if n == action {
			return true
		}
	}
	return false
}

func pick(m map[string]interface{}, keys ...string) map[string]interface{} {
	out := map[string]interface{}{}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			out[k] = v
		}
	}
	return out
}
