package mcp

import "testing"

func TestRemoteMCPVerbsRequireProjectAllowlist(t *testing.T) {
	denied := &fakeEngine{}
	for _, tool := range NewServer(denied).toolList() {
		if tool["name"] == "pull" || tool["name"] == "push" || tool["name"] == "pr" {
			t.Fatalf("remote verb %q advertised without allowlist", tool["name"])
		}
	}

	eng := &fakeEngine{project: map[string]interface{}{"config": map[string]interface{}{
		"remote": map[string]interface{}{"allow_mcp_verbs": []interface{}{"pull", "pr"}},
	}}}
	server := NewServer(eng)
	seen := map[string]bool{}
	for _, tool := range server.toolList() {
		seen[tool["name"].(string)] = true
	}
	if !seen["pull"] || !seen["pr"] || seen["push"] {
		t.Fatalf("allowlist tools = %v", seen)
	}

	_, err := server.call("push", []byte(`{"project_id":"calc"}`))
	if err == nil {
		t.Fatal("push succeeded despite missing allowlist")
	}
	_, err = server.call("pull", []byte(`{"project_id":"calc","branch":"topic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if eng.remoteAction != "pull" || eng.remoteRequest["actor"] != "mcp:operator" || eng.remoteRequest["branch"] != "topic" {
		t.Fatalf("remote request = %q %#v", eng.remoteAction, eng.remoteRequest)
	}
}
