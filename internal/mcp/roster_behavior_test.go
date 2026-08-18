package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func rosterCall(t *testing.T, s *Server, fields map[string]interface{}) (map[string]interface{}, *rpcError) {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil { t.Fatal(err) }
	p, err := json.Marshal(map[string]interface{}{"name": "roster", "arguments": json.RawMessage(b)})
	if err != nil { t.Fatal(err) }
	returnResult, rpcErr := s.dispatch(&rpcRequest{Method: "tools/call", Params: p})
	if rpcErr != nil { return nil, rpcErr }
	return returnResult.(map[string]interface{}), nil
}

func TestRosterMCPGetReturnsCanonicalViewUnchanged(t *testing.T) {
	global := map[string]interface{}{"entries": []interface{}{map[string]interface{}{"role": "implementer", "ducklings": []interface{}{"a", "b"}, "duckling": "a", "source": "global mode seat"}}}
	project := map[string]interface{}{"entries": []interface{}{map[string]interface{}{"role": "implementer", "ducklings": []interface{}{"b"}, "duckling": "b", "source": "project pin", "default": "a"}}}
	f := &fakeEngine{rosterViews: map[string]map[string]interface{}{"global:split": global, "p:split": project}}
	s := NewServer(f)
	got, _ := rosterCall(t, s, map[string]interface{}{"action": "get", "scope": "global", "mode": "split"})
	if !reflect.DeepEqual(got, global) { t.Fatalf("global view changed: %#v", got) }
	got, _ = rosterCall(t, s, map[string]interface{}{"action": "get", "scope": "project", "project_id": "p", "mode": "split"})
	if !reflect.DeepEqual(got, project) { t.Fatalf("project view changed: %#v", got) }
}

func TestRosterMCPProjectSetReplacesAndUnpinReturnsInheritance(t *testing.T) {
	f := &fakeEngine{rosterViews: map[string]map[string]interface{}{"p:split": {"entries": []interface{}{map[string]interface{}{"role": "implementer", "ducklings": []interface{}{"a", "b"}, "source": "project pin"}}}}}
	s := NewServer(f)
	_, _ = rosterCall(t, s, map[string]interface{}{"action": "set", "scope": "project", "project_id": "p", "mode": "split", "role": "implementer", "ducklings": []string{"a", "b"}})
	if !reflect.DeepEqual(f.lastRosterDucklings, []string{"a", "b"}) { t.Fatalf("set did not replace with ordered list: %#v", f.lastRosterDucklings) }
	_, _ = rosterCall(t, s, map[string]interface{}{"action": "unpin", "scope": "project", "project_id": "p", "mode": "split", "role": "implementer"})
	if f.lastRosterUnpin != "p:split:implementer" { t.Fatalf("wrong unpin call: %q", f.lastRosterUnpin) }
	f.rosterViews["p:split"] = map[string]interface{}{"entries": []interface{}{map[string]interface{}{"source": "global mode seat", "ducklings": []interface{}{"g1", "g2"}}}}
	got, _ := rosterCall(t, s, map[string]interface{}{"action": "get", "scope": "project", "project_id": "p", "mode": "split"})
	if got["entries"] == nil || !strings.Contains(string(mustJSON(got["entries"])), "global mode seat") { t.Fatalf("unpin did not expose inheritance: %#v", got) }
}

func TestRosterMCPValidationErrorsNameFieldsAndNext(t *testing.T) {
	f := &rosterValidationEngine{fakeEngine: &fakeEngine{}}
	s := NewServer(f)
	cases := []map[string]interface{}{
		{"action": "set", "scope": "project", "project_id": "p", "mode": "split", "role": "implementer", "ducklings": []string{"unknown"}},
		{"action": "set", "scope": "project", "project_id": "p", "mode": "split", "role": "bad", "ducklings": []string{"a"}},
		{"action": "set", "scope": "project", "project_id": "p", "mode": "split", "role": "implementer", "ducklings": []string{}},
	}
	for _, fields := range cases {
		got, _ := rosterCall(t, s, fields)
		text := got["content"].([]map[string]interface{})[0]["text"].(string)
		if got["isError"] != true || !strings.Contains(text, "next") { t.Errorf("not actionable: %#v", got) }
	}
}

func mustJSON(v interface{}) []byte { b, _ := json.Marshal(v); return b }

type rosterValidationEngine struct{ *fakeEngine }
func (f *rosterValidationEngine) RosterSetManyMode(project, mode, role string, ids []string) (map[string]interface{}, error) {
	if role == "bad" { return nil, &fieldError{"field role invalid; next: choose a board role"} }
	if len(ids) == 0 { return nil, &fieldError{"field ducklings must not be empty; next: provide IDs"} }
	if ids[0] == "unknown" { return nil, &fieldError{"no duckling 'unknown' — registered: a; next: choose a registered duckling"} }
	return f.fakeEngine.RosterSetManyMode(project, mode, role, ids)
}
type fieldError struct{ text string }
func (e *fieldError) Error() string { return e.text }
