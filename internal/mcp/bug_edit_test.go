package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/engineapi"
	"github.com/jrullan/ducklab/internal/engineclt"
	"github.com/jrullan/ducklab/internal/service"
)

func listedTool(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	resps := drive(t, &fakeEngine{}, initFrame, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	res, _ := resps[1]["result"].(map[string]interface{})
	tools, _ := res["tools"].([]interface{})
	for _, raw := range tools {
		tool, _ := raw.(map[string]interface{})
		if tool["name"] == name {
			return tool
		}
	}
	t.Fatalf("tools/list has no %s", name)
	return nil
}

// B-525: the operator over MCP met "split proposal requires test or coverage
// work but no portion claims it" three times with no tool that could change
// the proposal. The description is the only manual a small model reads, so it
// must say when to reach for the tool, that the person agrees first, and how
// the proposal argument behaves.
func TestBugEditTellsASmallModelWhenAndWithWhoseConsent(t *testing.T) {
	tool := listedTool(t, "bug_edit")
	desc, _ := tool["description"].(string)
	for _, want := range []string{
		"no portion claims it",     // the refusal it answers, verbatim
		"bug_promote",              // what comes after
		"promote_blockers",         // where a triage gate says it
		"ASK FIRST",                // consent
		"after they agree",         // consent, said twice as plainly
		"REPLACES the whole split", // the argument's one trap
		"Regression test covers",   // the fix the rule accepts
		"Errors are the engine's own words",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("bug_edit description lacks %q", want)
		}
	}
	schema, _ := tool["inputSchema"].(map[string]interface{})
	props, _ := schema["properties"].(map[string]interface{})
	for _, field := range []string{"project_id", "bug_id", "title", "body", "severity", "proposal"} {
		if props[field] == nil {
			t.Errorf("bug_edit schema lacks %s", field)
		}
	}
	proposal, _ := props["proposal"].(map[string]interface{})
	items, _ := proposal["items"].(map[string]interface{})
	blob, _ := json.Marshal(items)
	for _, want := range []string{`"required":["title","acceptance","owns"]`, `"maxItems":2`, `"minItems":1`} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("proposal portion schema lacks %s: %s", want, blob)
		}
	}
	// The tools that lead an operator into the dead end now point out of it.
	if d, _ := listedTool(t, "bug_promote")["description"].(string); !strings.Contains(d, "bug_edit") {
		t.Errorf("bug_promote does not name bug_edit: %s", d)
	}
	if d, _ := listedTool(t, "bug_triage")["description"].(string); !strings.Contains(d, "promote_blockers") {
		t.Errorf("bug_triage does not say where an unpromotable proposal is flagged: %s", d)
	}
}

// Only what the operator changes reaches the engine — and the engine rewrites
// the body whenever a title arrives, so a title-only edit carries the current
// text instead of erasing the report.
func TestBugEditSendsOnlyWhatChangesAndKeepsTheReport(t *testing.T) {
	eng := &fakeEngine{bugs: []map[string]interface{}{{"id": "B-001", "body": "steps to reproduce"}}}
	resps := drive(t, eng, initFrame,
		callFrame(2, "bug_edit", `{"project_id":"p","bug_id":"B-001","title":"sharper title"}`),
		callFrame(3, "bug_edit", `{"project_id":"p","bug_id":"B-001","proposal":[]}`),
		callFrame(4, "bug_edit", `{"project_id":"p","bug_id":"B-001","severity":"high","proposal":[{"title":"t","acceptance":["a"],"owns":["x.go"]}]}`),
		callFrame(5, "bug_edit", `{"project_id":"p","bug_id":"B-001"}`),
	)
	if len(eng.edited) != 3 {
		t.Fatalf("engine edits = %d, want 3: %#v", len(eng.edited), eng.edited)
	}
	if eng.edited[0]["title"] != "sharper title" || eng.edited[0]["body"] != "steps to reproduce" {
		t.Errorf("title-only edit = %#v, want the current body carried along", eng.edited[0])
	}
	if _, sent := eng.edited[0]["proposal"]; sent {
		t.Errorf("an edit without a proposal touched the split: %#v", eng.edited[0])
	}
	if p, ok := eng.edited[1]["proposal"].([]interface{}); !ok || len(p) != 0 {
		t.Errorf("empty proposal = %#v, want [] sent (discard the split)", eng.edited[1]["proposal"])
	}
	if _, sent := eng.edited[1]["title"]; sent {
		t.Errorf("proposal-only edit sent a title: %#v", eng.edited[1])
	}
	if p, _ := eng.edited[2]["proposal"].([]interface{}); len(p) != 1 || eng.edited[2]["severity"] != "high" {
		t.Errorf("edit = %#v", eng.edited[2])
	}
	if text, isErr := toolResultText(t, resps[4]); !isErr || !strings.Contains(text, "nothing to change") {
		t.Errorf("empty edit = %q (error=%v)", text, isErr)
	}
}

// The B-014 dead end, end to end through the real engine: promote refuses the
// stored split with the engine's own words, bug_edit fixes it (and a bad edit
// comes back verbatim), and promote then succeeds.
func TestBugEditOverMCPUnsticksTheB014Promote(t *testing.T) {
	root := t.TempDir()
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, filepath.Join(root, k))
	}
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{"fake": {Kind: config.ProviderKindOpenAI, BaseURL: "fake://"}}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{"pato": {Provider: "fake", Model: "m"}}
	svc, err := service.New(cfg, service.Options{Bus: bus.New(16), ConfigPath: filepath.Join(root, "config.toml")})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	project, err := svc.ProjectInit(context.Background(), service.InitRequest{Path: dir, Name: "calculator", GitInit: true, GitName: "Ada", GitEmail: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "scripts/check.sh"} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.BugAdd(context.Background(), project.ID, service.BugRequest{Title: "touch devices cannot drag vertices"}); err != nil {
		t.Fatal(err)
	}
	// The stored proposal exactly as calculator B-014's triage left it.
	if _, err := svc.ApplyTriage(context.Background(), project.ID, []map[string]interface{}{{
		"bug": "B-001", "severity": "high", "reason": "mouse-only listeners",
		"test_strategy":   "test-first",
		"suspected_files": []string{"index.html", "scripts/check.sh"},
		"deliverables": []string{
			"A test simulates pointerdown/pointermove/pointerup on the canvas and asserts vertex drag and camera pan work identically to mouse",
		},
		"proposal": []interface{}{
			map[string]interface{}{"title": "Migrate canvas interaction to Pointer Events and add touch-action:none",
				"acceptance": []interface{}{"canvas has touch-action: none", "pointer handlers implement drag and pan like mouse"},
				"owns":       []interface{}{"index.html"}},
			map[string]interface{}{"title": "Remove check.sh guard forbidding touch/pointer handlers",
				"acceptance": []interface{}{"check.sh no longer forbids pointer handlers"},
				"owns":       []interface{}{"scripts/check.sh"}},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engineapi.New(svc, bus.New(16), "token", "test", ""))
	t.Cleanup(server.Close)
	eng := &engineclt.Client{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()}

	args := func(extra string) string {
		return `{"project_id":"` + project.ID + `","bug_id":"B-001"` + extra + `}`
	}
	fixed := `,"proposal":[` +
		`{"title":"Migrate canvas interaction to Pointer Events and add touch-action:none","acceptance":["canvas has touch-action: none","Regression test covers pointer drag and pan"],"owns":["index.html"]},` +
		`{"title":"Remove check.sh guard forbidding touch/pointer handlers","acceptance":["check.sh no longer forbids pointer handlers"],"owns":["scripts/check.sh"]}]`
	tooMany := `,"proposal":[{"title":"t","acceptance":["a","b","c"],"owns":["index.html"]}]`
	resps := drive(t, eng, initFrame,
		callFrame(2, "bug_promote", args("")),
		callFrame(3, "bug_edit", args(tooMany)),
		callFrame(4, "bug_edit", args(fixed)),
		callFrame(5, "bug_promote", args("")),
	)
	text, isErr := toolResultText(t, resps[1])
	if !isErr || !strings.Contains(text, "split proposal requires test or coverage work but no portion claims it") || !strings.Contains(text, "bug_edit") {
		t.Fatalf("first promote = %q (error=%v), want the engine's refusal naming bug_edit", text, isErr)
	}
	text, isErr = toolResultText(t, resps[2])
	if !isErr || !strings.Contains(text, "split proposal: portion 1: proposal portions need title, 1-2 acceptance criteria, and owns") {
		t.Errorf("invalid edit = %q (error=%v), want the engine's validation verbatim", text, isErr)
	}
	text, isErr = toolResultText(t, resps[3])
	if isErr || strings.Contains(text, "promote_blocker") || !strings.Contains(text, "Regression test covers pointer drag and pan") {
		t.Fatalf("edit = %q (error=%v), want the stored split with no blocker", text, isErr)
	}
	text, isErr = toolResultText(t, resps[4])
	if isErr || !strings.Contains(text, `"tasks"`) {
		t.Fatalf("promote after edit = %q (error=%v), want the two tasks", text, isErr)
	}
}

// B-523: a triage gate's unpromotable proposal reaches the MCP operator in
// run_get, problem and fix, before it decides — not at promote afterwards.
func TestRunGetCarriesATriageGatesPromoteBlockers(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-1": {"id": "r-1", "stage": "triage", "status": "paused", "pending_kind": "gate",
			"next": []interface{}{"accept", "reject"},
			"pending_data": map[string]interface{}{
				"promote_blockers": []interface{}{map[string]interface{}{
					"bug": "B-014", "problem": "split proposal requires test or coverage work but no portion claims it",
					"fix": "give the test work to the portion that owns the behavior under test",
				}},
			}},
	}}
	resps := drive(t, eng, initFrame, callFrame(2, "run_get", `{"run_id":"r-1"}`))
	text, isErr := toolResultText(t, resps[1])
	if isErr || !strings.Contains(text, "no portion claims it") || !strings.Contains(text, "give the test work") {
		t.Fatalf("run_get = %q (error=%v), want the gate's promote_blockers", text, isErr)
	}
}
