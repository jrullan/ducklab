package mcp

import (
	"encoding/json"
	"fmt"
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
				"Start here. Each pending run carries `next`: the ONLY actions you may take on it.",
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
			"description": "File a bug. Triage and promotion follow the ordinary loop.",
			"inputSchema": obj(map[string]interface{}{
				"project_id": str("the project id"),
				"title":      str("one line"),
				"body":       str("what happened, what was expected"),
			}, "project_id", "title", "body"),
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
		bug, err := s.eng.BugAdd(a.str("project_id"), map[string]string{
			"title": a.str("title"), "body": a.str("body"),
		})
		if err != nil {
			return nil, err
		}
		return toolJSON(bug), nil
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
		out = append(out, map[string]interface{}{
			"project": id, "name": p["name"],
			"waiting_for_decision": waiting,
			"running":              active,
		})
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
