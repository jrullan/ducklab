package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEngine records what the operator did and answers from fixtures.
type fakeEngine struct {
	lastRunReq map[string]interface{}
	images []string
	settled []string
	extended []string
	testThenBuild bool
	testNote      string
	tasks []map[string]interface{}
	artifacts map[string]map[string]interface{}
	bugs []map[string]interface{}
	nextSteps []map[string]interface{}
	runs       map[string]map[string]interface{}
	accepted   []string
	acceptedAs string
	rejected   []string
	filed      []map[string]string
	attached     string
	attachedName string
	revised    []string
	lastStageReq  map[string]interface{}
	lastTriageReq map[string]interface{}
	budgetLifted string
	resumeCount int
}

func (f *fakeEngine) ProjectList() ([]map[string]interface{}, error) {
	return []map[string]interface{}{{"id": "calc", "name": "Calculator"}}, nil
}
func (f *fakeEngine) RunList(string) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	for _, r := range f.runs {
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeEngine) RunGet(id string) (map[string]interface{}, error) {
	r, ok := f.runs[id]
	if !ok {
		return nil, fmt.Errorf("run %q not found", id)
	}
	return r, nil
}
func (f *fakeEngine) RunDiff(string) (string, string, string, error) { return "+fixed", "", "", nil }
func (f *fakeEngine) RunAcceptAs(id, msg, actor string) (map[string]interface{}, error) {
	f.accepted = append(f.accepted, id)
	f.acceptedAs = actor
	return map[string]interface{}{"accepted": true}, nil
}
func (f *fakeEngine) RunReject(id, reason string) error {
	f.rejected = append(f.rejected, id+": "+reason)
	return nil
}
func (f *fakeEngine) RunAbort(string) error                          { return nil }
func (f *fakeEngine) RunResume(id string) (map[string]interface{}, error) {
	f.resumeCount++
	if f.budgetLifted == "" {
		return map[string]interface{}{"id": id, "status": "paused"}, nil
	}
	f.runs[id]["status"] = "running"
	return map[string]interface{}{"id": id, "status": "running"}, nil
}
func (f *fakeEngine) RunBudgetLift(id, kind, actor string) (map[string]interface{}, error) {
	if kind != "tokens" && kind != "time" && kind != "calls" {
		return nil, fmt.Errorf("invalid kind %q: no budget cap named %q", kind, kind)
	}
	f.budgetLifted = kind + " by " + actor
	return map[string]interface{}{"id": id, "kind": kind, "lifted_by": actor}, nil
}
func (f *fakeEngine) RunAnswer(string, string, string) error          { return nil }
func (f *fakeEngine) RunFileFindings(id string) ([]map[string]interface{}, error) {
	run, ok := f.runs[id]
	if !ok {
		return nil, fmt.Errorf("run %q not found", id)
	}
	findings, _ := run["findings"].([]interface{})
	for _, finding := range findings {
		m := finding.(map[string]interface{})
		body := fmt.Sprintf("file=%v line=%v issue=%v fix=%v", m["file"], m["line"], m["issue"], m["fix"])
		f.filed = append(f.filed, map[string]string{
			"severity": fmt.Sprint(m["severity"]), "body": body,
			"reporter": "mcp:" + "elena", "source": "mcp",
		})
	}
	return nil, nil
}
func (f *fakeEngine) RunStart(_ string, req map[string]interface{}) (map[string]interface{}, error) {
	f.lastRunReq = req
	return map[string]interface{}{"id": "r-new"}, nil
}
func (f *fakeEngine) StageStart(p, stage string, req map[string]interface{}) (map[string]interface{}, error) {
	f.lastStageReq = req
	f.revised = append(f.revised, fmt.Sprint(req["revise"]))
	if ext, ok := req["extend"].(string); ok && ext != "" {
		f.extended = append(f.extended, ext)
	}
	if settle, _ := req["settle"].(bool); settle {
		f.settled = append(f.settled, stage)
	}
	if imgs, ok := req["images"].([]string); ok {
		f.images = append(f.images, imgs...)
	}
	return map[string]interface{}{"id": "r-rev"}, nil
}
func (f *fakeEngine) ArtifactGet(_, kind string) (map[string]interface{}, error) {
	if f.artifacts != nil {
		if doc, ok := f.artifacts[kind]; ok {
			return doc, nil
		}
		return nil, fmt.Errorf("artifact %q not found", kind)
	}
	return map[string]interface{}{"kind": "requirements"}, nil
}
func (f *fakeEngine) TaskList(string) ([]map[string]interface{}, error) {
	if f.tasks != nil {
		return f.tasks, nil
	}
	return nil, nil
}
func (f *fakeEngine) BugAdd(_ string, req map[string]string) (map[string]interface{}, error) {
	f.filed = append(f.filed, req)
	return map[string]interface{}{"id": fmt.Sprintf("B-%03d", len(f.filed))}, nil
}

// drive sends JSON-RPC lines and returns the decoded responses.
func drive(t *testing.T, eng Engine, lines ...string) []map[string]interface{} {
	t.Helper()
	var in bytes.Buffer
	for _, l := range lines {
		in.WriteString(l + "\n")
	}
	var out bytes.Buffer
	if err := NewServer(eng).Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	var resps []map[string]interface{}
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad frame %q: %v", l, err)
		}
		resps = append(resps, m)
	}
	return resps
}

const initFrame = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"claude"}}}`

func callFrame(id int, tool string, args string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, tool, args)
}

func toolResultText(t *testing.T, resp map[string]interface{}) (string, bool) {
	t.Helper()
	res, _ := resp["result"].(map[string]interface{})
	content, _ := res["content"].([]interface{})
	if len(content) == 0 {
		t.Fatalf("no content in %v", resp)
	}
	first, _ := content[0].(map[string]interface{})
	text, _ := first["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

func TestInitializeAndToolListSpeakMCP(t *testing.T) {
	resps := drive(t, &fakeEngine{},
		initFrame,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	// The notification gets no reply: two responses for three frames.
	if len(resps) != 2 {
		t.Fatalf("%d responses, want 2", len(resps))
	}
	tools, _ := resps[1]["result"].(map[string]interface{})["tools"].([]interface{})
	names := map[string]bool{}
	for _, tl := range tools {
		names[fmt.Sprint(tl.(map[string]interface{})["name"])] = true
	}
	for _, must := range []string{"status", "run_get", "decide", "answer", "task_list", "run_start", "stage_start", "budget_lift", "file_findings"} {
		if !names[must] {
			t.Errorf("tool %q missing", must)
		}
	}
}

// The record must never say a human decided what a model decided.
func TestADecisionIsAttributedToTheOperator(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-1": {"id": "r-1", "status": "paused", "next": []interface{}{"accept", "reject"}},
	}}
	resps := drive(t, eng,
		initFrame,
		callFrame(2, "decide", `{"run_id":"r-1","action":"accept","reason":"gate green, diff matches the task"}`),
	)
	if _, isErr := toolResultText(t, resps[1]); isErr {
		t.Fatalf("accept failed: %v", resps[1])
	}
	if eng.acceptedAs != "mcp:claude" {
		t.Errorf("accepted as %q, want mcp:claude", eng.acceptedAs)
	}
}

// The engine's next list is the law, read from the run — an operator can
// never take an action a person could not.
func TestADecisionOutsideNextIsRefused(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-1": {"id": "r-1", "status": "paused", "next": []interface{}{"resume", "abort"}},
	}}
	resps := drive(t, eng,
		initFrame,
		callFrame(2, "decide", `{"run_id":"r-1","action":"accept","reason":"looks fine"}`),
	)
	text, isErr := toolResultText(t, resps[1])
	if !isErr || !strings.Contains(text, "legal actions") {
		t.Errorf("an action outside next went through: %q", text)
	}
	if len(eng.accepted) != 0 {
		t.Error("the engine was asked to accept anyway")
	}
}

// A capped run must be liftable through MCP before resume.
func TestBudgetLiftThroughMCPAllowsResume(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{"r-cap": {"id": "r-cap", "status": "paused", "next": []interface{}{"resume", "abort"}}}}
	resps := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"elena"}}}`,
		callFrame(2, "budget_lift", `{"run_id":"r-cap","kind":"tokens"}`),
		callFrame(3, "decide", `{"run_id":"r-cap","action":"resume","reason":"lift the cap"}`),
	)
	if _, err := toolResultText(t, resps[1]); err || eng.budgetLifted != "tokens by mcp:elena" { t.Fatalf("lift failed: %q %q", eng.budgetLifted, resps[1]) }
	if text, err := toolResultText(t, resps[2]); err || !strings.Contains(text, "running") { t.Fatalf("resume failed: %q", text) }
}

func TestBudgetLiftInvalidKindNamesTheField(t *testing.T) {
	resps := drive(t, &fakeEngine{}, initFrame, callFrame(2, "budget_lift", `{"run_id":"r-cap","kind":"widgets"}`))
	text, err := toolResultText(t, resps[1])
	if !err || !strings.Contains(strings.ToLower(text), "kind") { t.Fatalf("error does not identify kind: %q", text) }
}

// A decision without a reason is a decision nobody can audit.
func TestADecisionNeedsAReason(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-1": {"id": "r-1", "next": []interface{}{"accept"}},
	}}
	resps := drive(t, eng, initFrame,
		callFrame(2, "decide", `{"run_id":"r-1","action":"accept","reason":"  "}`))
	if text, isErr := toolResultText(t, resps[1]); !isErr || !strings.Contains(text, "reason") {
		t.Errorf("a reasonless decision went through: %q", text)
	}
}

func TestStatusListsWaitingAndRunning(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-1": {"id": "r-1", "status": "paused", "pending_kind": "gate", "next": []interface{}{"accept"}},
		"r-2": {"id": "r-2", "status": "running"},
		"r-3": {"id": "r-3", "status": "done"},
	}}
	resps := drive(t, eng, initFrame, callFrame(2, "status", `{}`))
	text, isErr := toolResultText(t, resps[1])
	if isErr {
		t.Fatal(text)
	}
	if !strings.Contains(text, "r-1") || !strings.Contains(text, "r-2") || strings.Contains(text, "r-3") {
		t.Errorf("status carries the wrong runs:\n%s", text)
	}
}

func TestStatusIncludesDocumentLifecycleState(t *testing.T) {
	eng := &fakeEngine{
		artifacts: map[string]map[string]interface{}{
			"requirements": {"approved": true, "markdown": "FULL REQUIREMENTS BODY"},
			"spec":        {"approved": false, "markdown": "FULL SPEC BODY", "proposal": map[string]interface{}{"run_id": "r-spec"}},
		},
		tasks: []map[string]interface{}{
			{"id": "T-001", "status": "todo"},
			{"id": "T-002", "status": "accepted"},
			{"id": "T-003", "status": "blocked"},
		},
		bugs: []map[string]interface{}{
			{"id": "B-001", "status": "open"},
			{"id": "B-002", "status": "triaged"},
			{"id": "B-003", "status": "closed"},
		},
	}
	resps := drive(t, eng, initFrame, callFrame(2, "status", `{}`))
	text, isErr := toolResultText(t, resps[1])
	if isErr {
		t.Fatal(text)
	}
	var projects []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &projects); err != nil {
		t.Fatalf("status is not JSON: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	docs, ok := projects[0]["documents"].(map[string]interface{})
	if !ok {
		t.Fatalf("status documents = %#v, want an object", projects[0]["documents"])
	}
	for kind, want := range map[string]string{
		"requirements": "approved",
		"spec":         "proposed",
		"plan":         "none",
	} {
		if got := docs[kind]; got != want {
			t.Errorf("documents.%s = %#v, want %q", kind, got, want)
		}
	}
	if got, ok := projects[0]["tasks"].(float64); !ok || got != 3 {
		t.Errorf("tasks = %#v, want 3", projects[0]["tasks"])
	}
	if got, ok := projects[0]["open_bugs"].(float64); !ok || got != 2 {
		t.Errorf("open_bugs = %#v, want 2", projects[0]["open_bugs"])
	}
	if strings.Contains(text, "FULL REQUIREMENTS BODY") || strings.Contains(text, "FULL SPEC BODY") {
		t.Error("status leaked a document body; lifecycle orientation must remain cheap")
	}
}

// Accepted is a gate result, not a release. Status must carry the pileup and
// the branches holding it so an operator can tell what is installed versus what
// merely passed review.
func TestStatusSurfacesAcceptedUnreleasedWork(t *testing.T) {
	eng := &fakeEngine{tasks: []map[string]interface{}{
		{"id": "T-001", "status": "accepted", "branch": "ducklab/T-001"},
		{"id": "T-002", "status": "accepted", "branch": "ducklab/T-002"},
		{"id": "T-003", "status": "todo", "branch": "main"},
	}}
	resps := drive(t, eng, initFrame, callFrame(2, "status", `{}`))
	text, isErr := toolResultText(t, resps[1])
	if isErr {
		t.Fatal(text)
	}
	if !strings.Contains(text, "accepted-unreleased") || !strings.Contains(text, "2") {
		t.Fatalf("status does not surface the accepted-unreleased count: %s", text)
	}
	if !strings.Contains(text, "ducklab/T-001") || !strings.Contains(text, "ducklab/T-002") {
		t.Errorf("status does not identify branches holding accepted work: %s", text)
	}
}

// MCP status follows ServiceStatus: unreleased_branches is the numeric count,
// while the branch identities use the additive unreleased_branch_names field.
func TestStatusUsesServiceStatusUnreleasedBranchContract(t *testing.T) {
	eng := &fakeEngine{tasks: []map[string]interface{}{
		{"id": "T-001", "status": "accepted", "branch": "ducklab/T-001"},
		{"id": "T-002", "status": "accepted", "branch": "ducklab/T-002"},
		{"id": "T-003", "status": "accepted", "branch": "main"},
		{"id": "T-004", "status": "todo", "branch": "ducklab/T-004"},
	}}
	resps := drive(t, eng, initFrame, callFrame(2, "status", `{}`))
	text, isErr := toolResultText(t, resps[1])
	if isErr {
		t.Fatal(text)
	}
	var projects []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &projects); err != nil {
		t.Fatalf("status is not JSON: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	project := projects[0]
	if got, ok := project["unreleased_branches"].(float64); !ok || got != 2 {
		t.Fatalf("unreleased_branches = %#v, want integer count 2", project["unreleased_branches"])
	}
	names, ok := project["unreleased_branch_names"].([]interface{})
	if !ok {
		t.Fatalf("unreleased_branch_names = %#v, want an array", project["unreleased_branch_names"])
	}
	want := []string{"ducklab/T-001", "ducklab/T-002"}
	if len(names) != len(want) {
		t.Fatalf("unreleased_branch_names = %#v, want %#v", names, want)
	}
	for i, name := range want {
		if names[i] != name {
			t.Errorf("unreleased_branch_names[%d] = %#v, want %q", i, names[i], name)
		}
	}
}

func TestStatusIncludesEmptyNextSteps(t *testing.T) {
	eng := &fakeEngine{nextSteps: []map[string]interface{}{}}
	resps := drive(t, eng, initFrame, callFrame(2, "status", `{}`))
	text, isErr := toolResultText(t, resps[1])
	if isErr {
		t.Fatal(text)
	}
	var projects []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &projects); err != nil {
		t.Fatalf("status is not JSON: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	steps, ok := projects[0]["next_steps"]
	if !ok {
		t.Fatal("status omitted next_steps for a project with no guidance")
	}
	if got, ok := steps.([]interface{}); !ok || len(got) != 0 {
		t.Errorf("next_steps = %#v, want an empty array", steps)
	}
}

func TestStatusIncludesProjectNextSteps(t *testing.T) {
	resps := drive(t, &fakeEngine{}, initFrame, callFrame(2, "status", `{}`))
	text, isErr := toolResultText(t, resps[1])
	if isErr {
		t.Fatal(text)
	}
	var projects []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &projects); err != nil {
		t.Fatalf("status is not JSON: %v", err)
	}
	steps, ok := projects[0]["next_steps"]
	if !ok {
		t.Fatal("status omitted next_steps")
	}
	got, ok := steps.([]interface{})
	if !ok || len(got) != 1 || got[0].(map[string]interface{})["id"] != "promote" {
		t.Errorf("next_steps = %#v, want the engine's project guidance", steps)
	}
}

func TestRunGetCarriesTheDiffAndNext(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-1": {"id": "r-1", "status": "paused", "verdict": "PASSED", "next": []interface{}{"accept", "reject"}},
	}}
	resps := drive(t, eng, initFrame, callFrame(2, "run_get", `{"run_id":"r-1"}`))
	text, _ := toolResultText(t, resps[1])
	if !strings.Contains(text, "+fixed") || !strings.Contains(text, "accept") {
		t.Errorf("run_get is missing the diff or the actions:\n%s", text)
	}
}

func TestRunGetCarriesStructuredReviewerFindings(t *testing.T) {
	findings := []interface{}{
		map[string]interface{}{"severity": "high", "file": "internal/mcp/tools.go", "line": 42, "issue": "missing action", "fix": "add the tool"},
		map[string]interface{}{"severity": "minor", "file": "README.md", "line": 7, "issue": "stale example", "fix": "refresh the example"},
	}
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-review": {"id": "r-review", "status": "paused", "verdict": "PASSED", "findings": findings},
	}}
	resps := drive(t, eng, initFrame, callFrame(2, "run_get", `{"run_id":"r-review"}`))
	text, isErr := toolResultText(t, resps[1])
	if isErr {
		t.Fatal(text)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("run_get is not JSON: %v", err)
	}
	gotFindings, ok := got["findings"].([]interface{})
	if !ok || len(gotFindings) != 2 {
		t.Fatalf("findings = %#v, want both reviewer findings", got["findings"])
	}
	for _, key := range []string{"severity", "file", "line", "issue", "fix"} {
		if _, ok := gotFindings[0].(map[string]interface{})[key]; !ok {
			t.Errorf("finding omitted %q: %#v", key, gotFindings[0])
		}
	}
}

func TestFileFindingsCreatesAttributedBugs(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-review": {"id": "r-review", "project_id": "calc", "status": "paused", "findings": []interface{}{
			map[string]interface{}{"severity": "high", "file": "a.go", "line": 12, "issue": "unsafe conversion", "fix": "check the input"},
			map[string]interface{}{"severity": "minor", "file": "b.go", "line": 8, "issue": "unclear name", "fix": "rename it"},
		}},
	}}
	resps := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"elena"}}}`,
		callFrame(2, "file_findings", `{"run_id":"r-review"}`),
	)
	if _, isErr := toolResultText(t, resps[1]); isErr {
		t.Fatalf("file_findings failed: %v", resps[1])
	}
	if len(eng.filed) != 2 {
		t.Fatalf("filed %d bugs, want 2", len(eng.filed))
	}
	for i, bug := range eng.filed {
		if bug["reporter"] != "mcp:elena" || bug["source"] != "mcp" {
			t.Errorf("bug lacks MCP attribution: %#v", bug)
		}
		wantSeverity := []string{"high", "minor"}[i]
		if bug["severity"] != wantSeverity {
			t.Errorf("bug severity = %q, want %q", bug["severity"], wantSeverity)
		}
		for _, part := range []string{"file", "line", "issue", "fix"} {
			if !strings.Contains(bug["body"], part) {
				t.Errorf("bug body lacks finding field %q: %#v", part, bug)
			}
		}
	}
}

func (f *fakeEngine) BugList(projectID string, openOnly bool) ([]map[string]interface{}, error) {
	bugs := f.bugs
	if bugs == nil {
		bugs = []map[string]interface{}{{"id": "B-001", "status": "open"}}
	}
	if !openOnly {
		return bugs, nil
	}
	var open []map[string]interface{}
	for _, b := range bugs {
		switch b["status"] {
		case "verified", "closed", "duplicate", "wontfix":
		default:
			open = append(open, b)
		}
	}
	return open, nil
}

func (f *fakeEngine) BugAttach(projectID, bugID, filename, dataB64 string) (map[string]interface{}, error) {
	f.attached, f.attachedName = dataB64, filename
	return map[string]interface{}{"items": []string{filename}}, nil
}

func (f *fakeEngine) BugTriage(projectID, bugID string, req map[string]interface{}) (map[string]interface{}, error) {
	f.lastTriageReq = req
	return map[string]interface{}{"id": "r-triage"}, nil
}

func (f *fakeEngine) BugPromote(projectID, bugID, actor string) (map[string]interface{}, error) {
	return map[string]interface{}{"task": "T-100"}, nil
}

func (f *fakeEngine) BugMove(projectID, bugID, status, actor string) (map[string]interface{}, error) {
	return map[string]interface{}{"status": status}, nil
}

func (f *fakeEngine) ProjectNext(projectID string) ([]map[string]interface{}, error) {
	if f.nextSteps != nil {
		return f.nextSteps, nil
	}
	return []map[string]interface{}{
		{"id": "promote", "action": "Promote B-037 to a task — or park it", "kind": "bug", "ref": "B-037"},
	}, nil
}

func (f *fakeEngine) AppStatus(projectID string) (map[string]interface{}, error) {
	return map[string]interface{}{"running": false, "command": "python app.py"}, nil
}

func (f *fakeEngine) AppStart(projectID string) (map[string]interface{}, error) {
	return map[string]interface{}{"running": true}, nil
}

func (f *fakeEngine) AppStop(projectID string) error { return nil }

func (f *fakeEngine) TestStart(projectID, taskID, duckling string, thenBuild, redo bool, note string) (map[string]interface{}, error) {
	f.testThenBuild, f.testNote = thenBuild, note
	return map[string]interface{}{"id": "r-test"}, nil
}

func TestStageStartCarriesPerRunOverrides(t *testing.T) {
	eng := &fakeEngine{}
	resps := drive(t, eng, initFrame,
		callFrame(2, "stage_start", `{"project_id":"p","stage":"spec","ducklings":["architect-fast","critic-careful"],"mode":"council","agent_turns":19}`))
	if _, isErr := toolResultText(t, resps[1]); isErr {
		t.Fatalf("stage_start failed: %v", resps[1])
	}
	if got := fmt.Sprint(eng.lastStageReq["ducklings"]); got != "[architect-fast critic-careful]" {
		t.Errorf("ducklings = %q, want selected seats", got)
	}
	if eng.lastStageReq["mode"] != "council" {
		t.Errorf("mode = %#v, want council", eng.lastStageReq["mode"])
	}
	if eng.lastStageReq["agent_turns"] != float64(19) {
		t.Errorf("agent_turns = %#v, want 19", eng.lastStageReq["agent_turns"])
	}
}

func TestBugTriageCarriesPerRunOverrides(t *testing.T) {
	eng := &fakeEngine{}
	resps := drive(t, eng, initFrame,
		callFrame(2, "bug_triage", `{"project_id":"p","bug_id":"B-001","ducklings":["triager"],"mode":"sectioned","agent_turns":7}`))
	if _, isErr := toolResultText(t, resps[1]); isErr {
		t.Fatalf("bug_triage failed: %v", resps[1])
	}
	if got := fmt.Sprint(eng.lastTriageReq["ducklings"]); got != "[triager]" {
		t.Errorf("ducklings = %q, want selected seat", got)
	}
	if eng.lastTriageReq["mode"] != "sectioned" {
		t.Errorf("mode = %#v, want sectioned", eng.lastTriageReq["mode"])
	}
	if eng.lastTriageReq["agent_turns"] != float64(7) {
		t.Errorf("agent_turns = %#v, want 7", eng.lastTriageReq["agent_turns"])
	}
}

// The remote operator's full loop: file with severity, attach the screenshot,
// triage that bug, promote it, and start the TDD chain — every step the
// desktop offers, reachable from wherever Hermes runs.
func TestTheFullBugCycleIsReachable(t *testing.T) {
	eng := &fakeEngine{}
	resps := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"elena"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"bug_report","arguments":{"project_id":"p","title":"t","body":"b","severity":"high"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"bug_attach","arguments":{"project_id":"p","bug_id":"B-001","filename":"shot.png","data_base64":"aGk="}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"bug_triage","arguments":{"project_id":"p","bug_id":"B-001"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"bug_promote","arguments":{"project_id":"p","bug_id":"B-001"}}}`,
		// test_start is the old name, kept as an unlisted alias — this call IS the pin for it.
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"test_start","arguments":{"project_id":"p","task_id":"T-100"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"bug_move","arguments":{"project_id":"p","bug_id":"B-001","status":"verified"}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"app","arguments":{"project_id":"p","action":"start"}}}`,
	)
	for i, r := range resps[1:] {
		if r["error"] != nil {
			t.Errorf("step %d errored: %v", i+2, r["error"])
		}
	}
	// The tool list itself names every step of the loop.
	listResp := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	blob, _ := json.Marshal(listResp[1])
	for _, must := range []string{"bug_attach", "bug_triage", "bug_promote", "bug_move", "test_build", "test_only", "bug_list", "\"app\""} {
		if !strings.Contains(string(blob), must) {
			t.Errorf("tools/list is missing %q", must)
		}
	}
}

// A model cannot emit hundreds of kilobytes of base64 token by token without
// corrupting them — Elena proved it within the hour of getting the tool. The
// operator names a PATH and the server carries the bytes itself.
func TestBugAttachReadsTheFileItself(t *testing.T) {
	img := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(img, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := &fakeEngine{}
	resps := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"elena"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"bug_attach","arguments":{"project_id":"p","bug_id":"B-001","path":"`+img+`"}}}`,
	)
	if resps[1]["error"] != nil {
		t.Fatalf("path attach errored: %v", resps[1]["error"])
	}
	if eng.attached == "" {
		t.Fatal("nothing reached the engine")
	}
	raw, err := base64.StdEncoding.DecodeString(eng.attached)
	if err != nil || string(raw) != "png-bytes" {
		t.Errorf("bytes corrupted in transit: %q %v", eng.attached, err)
	}
	if eng.attachedName != "shot.png" {
		t.Errorf("filename = %q, want the file's own name", eng.attachedName)
	}
}

// Asked for status, the operator answered from a bug's raw state transitions
// — "in_progress, duplicate or wontfix" — while the human's actual next move
// was "promote it". status now carries the guide's own steps per project,
// so the operator answers from the same brain the rail and autopilot use.
func TestStatusCarriesTheGuide(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{}}
	resps := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status"}}`,
	)
	blob, _ := json.Marshal(resps[1])
	if !strings.Contains(string(blob), "Promote B-037") {
		t.Errorf("status does not carry the guide's steps: %s", blob)
	}
}

// task_list used to relay the engine's full task objects — bodies included,
// 141KB on a hundred-task project. The operator asking was a local model
// whose harness truncates tool results: what survived the cut was ONE task's
// body and no status at all, from which it honestly told the human that a
// finished project still had "T-001 pending". Compact rows, summary FIRST,
// so the answer to "anything pending?" survives any truncation.
func TestTaskListIsCompactWithTheSummaryFirst(t *testing.T) {
	huge := strings.Repeat("Create relational database schema for all entities. ", 500)
	eng := &fakeEngine{tasks: []map[string]interface{}{
		{"id": "T-001", "status": "accepted", "title": "Database schema", "body": huge},
		{"id": "T-002", "status": "todo", "title": "User boundary", "body": huge,
			"next": []interface{}{"test", "run"}},
	}}
	resps := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"task_list","arguments":{"project_id":"p"}}}`,
	)
	blob, _ := json.Marshal(resps[1])
	text := string(blob)
	if !strings.Contains(text, "2 task(s): 1 todo; 1 accepted;") {
		t.Errorf("no leading summary: %.300s", text)
	}
	if !strings.Contains(text, "T-002  todo  User boundary  [test run]") {
		t.Errorf("no compact row: %.300s", text)
	}
	if strings.Contains(text, "Create relational database schema") {
		t.Error("task bodies leaked into the listing — the truncation trap again")
	}
	if len(text) > 2000 {
		t.Errorf("listing is %d bytes for two tasks; it would not survive a truncating harness", len(text))
	}
}

// "test_first vs test_start" — neither name says whether a build follows,
// and the operator guessing wrong launches the wrong thing. The names now
// state the shape: test_build chains the build, test_only stops at the red
// test, and both carry the note that a redo's new expectations ride in.
func TestTheTestToolsSayWhatTheyChain(t *testing.T) {
	eng := &fakeEngine{}
	drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"test_only","arguments":{"project_id":"p","task_id":"T-110","note":"the indicator must read {+/-}#.# lbs"}}}`,
	)
	if eng.testThenBuild {
		t.Error("test_only chained a build")
	}
	if eng.testNote != "the indicator must read {+/-}#.# lbs" {
		t.Errorf("the note did not ride through: %q", eng.testNote)
	}
	drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"test_build","arguments":{"project_id":"p","task_id":"T-110"}}}`,
	)
	if !eng.testThenBuild {
		t.Error("test_build did not chain the build")
	}
}

// The light path out of review, remotely: a dictated improvement becomes a
// plan amendment instead of a fake bug report. The tool wraps stage_start
// with the extend field; the engine owns the contract.
func TestPlanExtendReachesTheEngine(t *testing.T) {
	eng := &fakeEngine{}
	resps := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"plan_extend","arguments":{"project_id":"p","change":"add CSV export"}}}`,
	)
	if resps[1]["error"] != nil {
		t.Fatalf("plan_extend errored: %v", resps[1]["error"])
	}
	if len(eng.extended) != 1 || eng.extended[0] != "add CSV export" {
		t.Errorf("the change did not reach the engine's extend field: %v", eng.extended)
	}
}

// Settling remotely takes no prose: the tool sends the click and the engine
// assembles the revision from the debt itself.
func TestSpecSettleIsOneCall(t *testing.T) {
	eng := &fakeEngine{}
	resps := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"spec_settle","arguments":{"project_id":"p"}}}`,
	)
	if resps[1]["error"] != nil {
		t.Fatalf("spec_settle errored: %v", resps[1]["error"])
	}
	if len(eng.settled) != 1 || eng.settled[0] != "spec" {
		t.Errorf("settle did not reach the spec stage: %v", eng.settled)
	}
}

// The screenshot goes by path — the server reads and encodes it itself,
// because models cannot type base64 — and rides the amendment to a seeing
// architect.
func TestPlanExtendCarriesAScreenshotByPath(t *testing.T) {
	img := filepath.Join(t.TempDir(), "mock.png")
	if err := os.WriteFile(img, []byte("pngbytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := &fakeEngine{}
	resps := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"plan_extend","arguments":{"project_id":"p","change":"match the mock","image_path":"`+img+`"}}}`,
	)
	if resps[1]["error"] != nil {
		t.Fatalf("plan_extend errored: %v", resps[1]["error"])
	}
	if len(eng.images) != 1 || !strings.HasPrefix(eng.images[0], "data:image/png;base64,") {
		t.Errorf("no data URL reached the engine: %v", eng.images)
	}
}


// Elena's blocker, live from the T-002 redo: the failed build's lesson had
// no channel — run_start's schema omitted the note the engine always
// accepted, so the one thing a redo exists to carry could not ride it.
func TestRunStartCarriesTheRedoNote(t *testing.T) {
	eng := &fakeEngine{}
	drive(t, eng, initFrame,
		callFrame(2, "run_start", `{"project_id":"p","task_id":"T-002","note":"fs_patch chokes on backticks; rewrite whole functions with fs_write"}`))
	if eng.lastRunReq == nil || eng.lastRunReq["note"] != "fs_patch chokes on backticks; rewrite whole functions with fs_write" {
		t.Fatalf("the note did not reach the engine: %+v", eng.lastRunReq)
	}
}
