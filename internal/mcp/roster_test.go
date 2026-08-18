package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The roster tool is one action-oriented MCP surface rather than a collection
// of project-file editing tools. Keeping this contract in the advertised schema
// lets clients discover the global/project pin boundary before they mutate it.
func TestRosterToolAdvertisesScopedCanonicalContract(t *testing.T) {
	var roster map[string]interface{}
	for _, tool := range toolList() {
		if tool["name"] == "roster" {
			roster = tool
			break
		}
	}
	if roster == nil {
		t.Error("tools/list must advertise the roster tool")
		return
	}

	schema, ok := roster["inputSchema"].(map[string]interface{})
	if !ok {
		t.Fatal("roster tool must advertise an input schema")
	}
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("roster input schema must describe its properties")
	}
	for _, field := range []string{"action", "scope", "project_id", "mode", "role", "ducklings"} {
		if _, ok := properties[field]; !ok {
			t.Errorf("roster input schema is missing %q", field)
		}
	}
	ducklings, ok := properties["ducklings"].(map[string]interface{})
	if !ok || ducklings["type"] != "array" {
		t.Error("roster ducklings must be an ordered array, not a scalar assignment")
	}
}

func TestRosterProjectSetPreservesOrderedReplacement(t *testing.T) {
	f := &fakeEngine{}
	s := NewServer(f)
	params, _ := json.Marshal(map[string]interface{}{"name": "roster", "arguments": map[string]interface{}{"action": "set", "scope": "project", "project_id": "p", "role": "implementer", "ducklings": []string{"a", "b"}}})
	_, rpcErr := s.dispatch(&rpcRequest{Method: "tools/call", Params: params})
	if rpcErr != nil || strings.Join(f.lastRosterDucklings, ",") != "a,b" {
		t.Fatalf("ordered replacement not forwarded: %#v %v", f.lastRosterDucklings, rpcErr)
	}
}

func TestRosterUnpinDispatchesProjectOnly(t *testing.T) {
	f := &fakeEngine{}
	s := NewServer(f)
	params, _ := json.Marshal(map[string]interface{}{"name": "roster", "arguments": map[string]interface{}{"action": "unpin", "scope": "project", "project_id": "p", "role": "reviewer"}})
	_, rpcErr := s.dispatch(&rpcRequest{Method: "tools/call", Params: params})
	if rpcErr != nil || f.lastRosterUnpin != "p::reviewer" {
		t.Fatalf("unpin not forwarded: %q %v", f.lastRosterUnpin, rpcErr)
	}
}

// Invalid requests must reach roster's action validation rather than being
// silently treated as an unknown MCP tool. This is intentionally in-process,
// matching the JSON-RPC path MCP clients use.
func TestRosterGetDelegatesCanonicalViewsByScope(t *testing.T) {
	f := &fakeEngine{}
	s := NewServer(f)
	for _, tc := range []struct{ scope, want string }{{"global", "global"}, {"project", "p"}} {
		params, _ := json.Marshal(map[string]interface{}{"name": "roster", "arguments": map[string]interface{}{"action": "get", "scope": tc.scope, "project_id": "p", "mode": "split"}})
		result, err := s.dispatch(&rpcRequest{Method: "tools/call", Params: params})
		if err != nil {
			t.Fatal(err)
		}
		m := result.(map[string]interface{})
		if m["scope"] == "global" && tc.want != "global" {
			t.Fatalf("wrong project view: %#v", m)
		}
		if tc.scope == "project" && m["project_id"] != tc.want {
			t.Fatalf("wrong project view: %#v", m)
		}
	}
}

func TestRosterToolRejectsInvalidScopeWithNext(t *testing.T) {
	server := NewServer(nil)
	params, _ := json.Marshal(map[string]interface{}{"name": "roster", "arguments": map[string]interface{}{"action": "get", "scope": "workspace"}})
	result, err := server.dispatch(&rpcRequest{Method: "tools/call", Params: params})
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]interface{})
	content := m["content"].([]map[string]interface{})
	if m["isError"] != true || !strings.Contains(content[0]["text"].(string), "scope") || !strings.Contains(content[0]["text"].(string), "next") {
		t.Fatalf("bad scope error: %#v", m)
	}
}

func TestRosterToolDispatchesActionValidation(t *testing.T) {
	server := NewServer(nil)
	params, err := json.Marshal(map[string]interface{}{
		"name":      "roster",
		"arguments": map[string]interface{}{"action": "replace", "scope": "project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, rpcErr := server.dispatch(&rpcRequest{Method: "tools/call", Params: params})
	if rpcErr != nil {
		t.Fatalf("roster validation must be a tool result, got RPC error: %v", rpcErr)
	}
	toolResult, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("tools/call returned %T, want tool result", result)
	}
	if toolResult["isError"] != true {
		t.Fatalf("invalid roster action must be rejected, got %#v", toolResult)
	}
	content, _ := toolResult["content"].([]map[string]interface{})
	if len(content) == 0 || !strings.Contains(strings.ToLower(content[0]["text"].(string)), "action") {
		t.Errorf("invalid roster action must name the action field, got %#v", toolResult)
	}
}
