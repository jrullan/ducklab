package mcp

import (
	"reflect"
	"strings"
	"testing"
)

// B-520: an operator marks a reference as scope by putting its path in
// scope_refs; the engine receives it as {"path", "role": "scope"} beside the
// plain background paths.
func TestStageStartAndProjectStartCarryScopeRefs(t *testing.T) {
	eng := &fakeEngine{}
	resps := drive(t, eng, initFrame,
		callFrame(2, "stage_start", `{"project_id":"p","stage":"intake","brief":"use this guide","refs":["/w/notes.md"],"scope_refs":["/w/guide.md"]}`),
		callFrame(3, "project_start", `{"name":"calc","brief":"match the manual","scope_refs":["/w/manual.md"]}`),
	)
	for i := 1; i <= 2; i++ {
		if _, isErr := toolResultText(t, resps[i]); isErr {
			t.Fatalf("call %d failed: %v", i, resps[i])
		}
	}
	want := []interface{}{"/w/notes.md", map[string]interface{}{"path": "/w/guide.md", "role": "scope"}}
	if got, _ := eng.lastStageReq["refs"].([]interface{}); !reflect.DeepEqual(got, want) {
		t.Errorf("stage_start refs = %#v, want %#v", eng.lastStageReq["refs"], want)
	}
	wantStart := []interface{}{map[string]interface{}{"path": "/w/manual.md", "role": "scope"}}
	if got, _ := eng.startReq["refs"].([]interface{}); !reflect.DeepEqual(got, wantStart) {
		t.Errorf("project_start refs = %#v, want %#v", eng.startReq["refs"], wantStart)
	}
}

func TestScopeRefsSchemaTellsASmallOperatorWhenToUseIt(t *testing.T) {
	for _, tool := range toolList() {
		name := tool["name"]
		if name != "stage_start" && name != "project_start" {
			continue
		}
		schema, _ := tool["inputSchema"].(map[string]interface{})
		props, _ := schema["properties"].(map[string]interface{})
		scope, ok := props["scope_refs"].(map[string]interface{})
		if !ok {
			t.Errorf("%s has no scope_refs", name)
			continue
		}
		desc, _ := scope["description"].(string)
		for _, want := range []string{"SCOPE", "describe the product the person wants built", "do not guess", "Every other reference goes in refs"} {
			if !strings.Contains(desc, want) {
				t.Errorf("%s scope_refs description lacks %q", name, want)
			}
		}
		refs, _ := props["refs"].(map[string]interface{})
		if d, _ := refs["description"].(string); !strings.Contains(d, "BACKGROUND") {
			t.Errorf("%s refs description does not say background: %q", name, d)
		}
	}
}
