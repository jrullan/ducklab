package engineclt

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The engine answers /next as {items, total} (handleProjectNext). A bare-list
// decode failed every call and MCP status showed next_steps: [] everywhere.
func TestProjectNextReadsTheEnginesEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/calc/next" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"intake","action":"Describe what you want to build"}],"total":1}`))
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	steps, err := client.ProjectNext("calc")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0]["id"] != "intake" {
		t.Fatalf("steps = %v", steps)
	}
}
