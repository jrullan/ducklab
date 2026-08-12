package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// fakeEngine records what the operator did and answers from fixtures.
type fakeEngine struct {
	runs       map[string]map[string]interface{}
	accepted   []string
	acceptedAs string
	rejected   []string
	revised    []string
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
func (f *fakeEngine) RunResume(string) (map[string]interface{}, error) { return nil, nil }
func (f *fakeEngine) RunAnswer(string, string, string) error          { return nil }
func (f *fakeEngine) RunStart(string, map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{"id": "r-new"}, nil
}
func (f *fakeEngine) StageStart(p, stage string, req map[string]interface{}) (map[string]interface{}, error) {
	f.revised = append(f.revised, fmt.Sprint(req["revise"]))
	return map[string]interface{}{"id": "r-rev"}, nil
}
func (f *fakeEngine) ArtifactGet(string, string) (map[string]interface{}, error) {
	return map[string]interface{}{"kind": "requirements"}, nil
}
func (f *fakeEngine) TaskList(string) ([]map[string]interface{}, error) { return nil, nil }
func (f *fakeEngine) BugAdd(string, map[string]string) (map[string]interface{}, error) {
	return map[string]interface{}{"id": "B-001"}, nil
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
	for _, must := range []string{"status", "run_get", "decide", "answer", "task_list", "run_start", "stage_start"} {
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

func (f *fakeEngine) BugList(projectID string, openOnly bool) ([]map[string]interface{}, error) {
	return []map[string]interface{}{{"id": "B-001", "status": "open"}}, nil
}

func (f *fakeEngine) BugAttach(projectID, bugID, filename, dataB64 string) (map[string]interface{}, error) {
	return map[string]interface{}{"items": []string{filename}}, nil
}

func (f *fakeEngine) BugTriage(projectID, bugID string) (map[string]interface{}, error) {
	return map[string]interface{}{"id": "r-triage"}, nil
}

func (f *fakeEngine) BugPromote(projectID, bugID string) (map[string]interface{}, error) {
	return map[string]interface{}{"task": "T-100"}, nil
}

func (f *fakeEngine) BugMove(projectID, bugID, status string) (map[string]interface{}, error) {
	return map[string]interface{}{"status": status}, nil
}

func (f *fakeEngine) AppStatus(projectID string) (map[string]interface{}, error) {
	return map[string]interface{}{"running": false, "command": "python app.py"}, nil
}

func (f *fakeEngine) AppStart(projectID string) (map[string]interface{}, error) {
	return map[string]interface{}{"running": true}, nil
}

func (f *fakeEngine) AppStop(projectID string) error { return nil }

func (f *fakeEngine) TestStart(projectID, taskID, duckling string, thenBuild bool) (map[string]interface{}, error) {
	return map[string]interface{}{"id": "r-test"}, nil
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
	for _, must := range []string{"bug_attach", "bug_triage", "bug_promote", "bug_move", "test_start", "bug_list", "\"app\""} {
		if !strings.Contains(string(blob), must) {
			t.Errorf("tools/list is missing %q", must)
		}
	}
}
