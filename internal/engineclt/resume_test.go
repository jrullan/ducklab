package engineclt

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// B-493: a resume note rides the body with its speaker; no note sends no
// body, exactly as before the note existed.
func TestRunResumeSendsANoteOnlyWhenThereIsOne(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/runs/r-1/resume" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		_, _ = w.Write([]byte(`{"id":"r-1","status":"running"}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	if _, err := client.RunResume("r-1", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RunResume("r-1", "2^-9 is 0.001953125", "mcp:elena"); err != nil {
		t.Fatal(err)
	}
	if bodies[0] != "" && bodies[0] != "null" {
		t.Errorf("a note-less resume sent a body: %q", bodies[0])
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(bodies[1]), &got); err != nil || got["note"] != "2^-9 is 0.001953125" || got["actor"] != "mcp:elena" {
		t.Errorf("resume body = %q (%v)", bodies[1], err)
	}
}
