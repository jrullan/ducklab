package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
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
			"name": "budget_lift",
			"description": "Remove one budget cap from a live or paused run so resume can proceed. One-way, per-cap, and attributed to this MCP operator; use kind tokens | usd | turns | wallclock | calls.",
			"inputSchema": obj(map[string]interface{}{
				"run_id": str("the run id, r-..."),
				"kind": str("the cap to remove: tokens | usd | turns | wallclock | calls"),
			}, "run_id", "kind"),
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
			"description": "The project's tasks: a status summary first, then one compact line per " +
				"task (id, status, title, next). Answer \"anything pending?\" from the summary. " +
				"Bodies are omitted — read one task's brief via the run you start, or artifact_get " +
				"the plan.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"status":     str("only tasks in this status, e.g. todo | in_progress | blocked | accepted (optional)"),
			}, "project_id"),
		},
		{
			"name": "run_start",
			"description": "Build a task WITHOUT the test-first discipline — an exception, not the " +
				"ordinary path. When the human says to run, start or build a task, they mean " +
				"test_build (the TDD chain); use run_start only when they explicitly ask to skip " +
				"the test. Mode defaults to the project's habit; solo|pair|tournament|split.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"task_id":    str("a task whose next includes run"),
				"mode":       str("optional mode"),
				"note":       noteProp(),
				"redo":       redoProp(),
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
			"name": "plan_extend",
			"description": "The light path out of review: add tasks for a small change WITHOUT the " +
				"redesign cycle. An architect amends the plan (1-3 tasks, wired to existing spec " +
				"sections where they cover it; uncovered tasks wear spec-debt). If the change alters " +
				"what the product IS — its requirements — the amendment comes back empty: write a " +
				"brief via stage_start intake instead. The proposal pauses for the human's accept.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"change":     str("what to add or improve, in the human's words"),
				"image_path": str("optional: absolute path to a screenshot on this machine, shown to the architect beside the text"),
			}, "project_id", "change"),
		},
		{
			"name": "spec_settle",
			"description": "Erase spec-debt: a spec revision documents, as built, the tasks no " +
				"section covers. The engine assembles the prompt from the debt itself — no text " +
				"needed. On the human's accept, Covers: fields wire the plan and the markers come " +
				"off. Use when task_list or the guide reports spec-debt.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
			}, "project_id"),
		},
		{
			"name":        "bug_report",
			"description": "File a bug. Attach screenshots with bug_attach, then bug_triage classifies it, bug_promote turns it into a task, and test_build builds the fix.",
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
			"name": "test_build",
			"description": "The ordinary way to build a task: a model writes the FAILING test first; it " +
				"lands red, is committed, and the build runs against it — one authorization, decided at " +
				"the build's gate with the committed test in the diff. When the human says to run, start " +
				"or build a task, they mean this.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"task_id":    str("a startable task, T-..."),
				"note":       noteProp(),
				"redo":       redoProp(),
			}, "project_id", "task_id"),
		},
		{
			"name": "test_only",
			"description": "Write the failing test for a task and STOP — no build chained. The red test " +
				"pauses for the human's accept; the build is launched separately later. Use only when " +
				"the human asked for the test alone; test_build is the ordinary path.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"task_id":    str("a startable task, T-..."),
				"note":       noteProp(),
				"redo":       redoProp(),
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
	case "budget_lift":
		out, err := s.eng.RunBudgetLift(a.str("run_id"), a.str("kind"), "mcp:"+s.client)
		if err != nil {
			return nil, err
		}
		return toolJSON(out), nil
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
		// Compact on purpose, summary first. The raw list carries every
		// task's full body — 141KB on a hundred-task project — and the
		// operator that asked was a local model whose harness truncates
		// tool results: what survived was ONE task's body and no status
		// field, from which it honestly concluded the finished project had
		// work pending. The summary line answers "anything pending?" even
		// if everything after it is cut.
		counts := map[string]int{}
		for _, t := range tasks {
			st, _ := t["status"].(string)
			if st == "" {
				st = "unknown"
			}
			counts[st]++
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d task(s):", len(tasks))
		for _, st := range []string{"todo", "in_progress", "blocked", "test_ready", "accepted", "done", "unknown"} {
			if counts[st] > 0 {
				fmt.Fprintf(&b, " %d %s;", counts[st], st)
			}
		}
		b.WriteString("\n")
		filter := a.str("status")
		shown := 0
		for _, t := range tasks {
			st, _ := t["status"].(string)
			if filter != "" && st != filter {
				continue
			}
			shown++
			id, _ := t["id"].(string)
			title, _ := t["title"].(string)
			line := fmt.Sprintf("%s  %s  %s", id, st, title)
			if next, ok := t["next"].([]interface{}); ok && len(next) > 0 {
				parts := make([]string, 0, len(next))
				for _, n := range next {
					if ns, ok := n.(string); ok {
						parts = append(parts, ns)
					}
				}
				line += "  [" + strings.Join(parts, " ") + "]"
			}
			b.WriteString(line + "\n")
		}
		if filter != "" && shown == 0 {
			fmt.Fprintf(&b, "no tasks in status %q\n", filter)
		}
		return toolText(b.String(), false), nil
	case "run_start":
		req := map[string]interface{}{"task_id": a.str("task_id")}
		if m := a.str("mode"); m != "" {
			req["mode"] = m
		}
		// The redo-after-failure channel. The engine always accepted a note;
		// only this schema forgot it — so an operator relaunching a build
		// with the LESSON from the failed attempt (the whole point of a
		// redo) had no way to say it. test_build carried one all along.
		if n := a.str("note"); n != "" {
			req["note"] = n
		}
		if redo, _ := a["redo"].(bool); redo {
			req["redo"] = true
		}
		run, err := s.eng.RunStart(a.str("project_id"), req)
		if err != nil {
			return nil, err
		}
		return toolJSON(run), nil
	case "plan_extend":
		req := map[string]interface{}{"extend": a.str("change")}
		if path := strings.TrimSpace(a.str("image_path")); path != "" {
			// The server reads the file itself — models cannot type base64.
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil, fmt.Errorf("cannot read image %s: %v", path, rerr)
			}
			if len(data) > 8<<20 {
				return nil, fmt.Errorf("image is %d bytes; the amendment carries at most 8MB", len(data))
			}
			ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
			if !strings.HasPrefix(ct, "image/") {
				return nil, fmt.Errorf("%s is not an image", path)
			}
			req["images"] = []string{"data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data)}
		}
		run, err := s.eng.StageStart(a.str("project_id"), "plan", req)
		if err != nil {
			return nil, err
		}
		return toolJSON(run), nil
	case "spec_settle":
		run, err := s.eng.StageStart(a.str("project_id"), "spec",
			map[string]interface{}{"settle": true})
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
		out, err := s.eng.BugPromote(a.str("project_id"), a.str("bug_id"), "mcp:"+s.client)
		if err != nil {
			return nil, err
		}
		return toolJSON(out), nil
	case "bug_move":
		out, err := s.eng.BugMove(a.str("project_id"), a.str("bug_id"), a.str("status"), "mcp:"+s.client)
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
	case "test_build", "test_only", "test_start":
		// test_start survives unlisted as an alias: the operator's own skill
		// notes may still name it, and "unknown tool" teaches nothing.
		then := name != "test_only"
		if v, ok := a["then_build"].(bool); ok {
			then = v
		}
		redo, _ := a["redo"].(bool)
		run, err := s.eng.TestStart(a.str("project_id"), a.str("task_id"), "", then, redo, a.str("note"))
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
		entry["next_steps"] = []map[string]interface{}{}
			if steps, err := s.eng.ProjectNext(id); err == nil && len(steps) > 0 {
			entry["next_steps"] = steps
		}
		// Cheap lifecycle orientation: never include document bodies in status.
		documents := map[string]string{}
		for _, kind := range []string{"requirements", "spec", "plan"} {
			doc, err := s.eng.ArtifactGet(id, kind)
			if err != nil {
				documents[kind] = "none"
			} else if doc["proposal"] != nil {
				documents[kind] = "proposed"
			} else if markdown, ok := doc["markdown"].(string); !ok || strings.TrimSpace(markdown) == "" {
				documents[kind] = "none"
			} else if approved, ok := doc["approved"].(bool); ok && approved {
				documents[kind] = "approved"
			} else {
				documents[kind] = "draft"
			}
		}
		entry["documents"] = documents
		if tasks, err := s.eng.TaskList(id); err == nil {
			entry["tasks"] = len(tasks)
		} else {
			entry["tasks"] = 0
		}
		if bugs, err := s.eng.BugList(id, true); err == nil {
			entry["open_bugs"] = len(bugs)
		} else {
			entry["open_bugs"] = 0
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

// redoProp is the schema for the explicit-consent flag on task launches.
// The engine refuses to start a task that was already accepted — its work is
// committed — unless redo is true. The description is the warning: an
// operator working from a stale listing learns the task is finished from the
// refusal itself.
func redoProp() map[string]interface{} {
	return map[string]interface{}{
		"type": "boolean",
		"description": "REQUIRED to launch a task that was already accepted. The engine refuses " +
			"finished tasks otherwise: their work is committed, and a fresh run would redo it. " +
			"Only set true when a human has explicitly asked to redo the task.",
	}
}

// noteProp is the schema for the launch note — the channel for what only the
// launcher knows now: new expectations on a redo, the cause of the failure
// being retried. It rides the test-writer's prompt.
func noteProp() map[string]interface{} {
	return map[string]interface{}{
		"type": "string",
		"description": "Context for the model doing the work — e.g. why the previous attempt missed " +
			"expectations, or what the human just clarified. Always pass it on a redo.",
	}
}
