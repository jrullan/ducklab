package engineclt

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// B-513: switching a chat's consultant names the duckling and, for an
// operator, who switched; a person's switch sends no actor at all.
func TestChatSwitchPostsTheDucklingAndItsActor(t *testing.T) {
	var bodies []map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/runs/r-chat/chat/consultant" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("body %q: %v", raw, err)
		}
		bodies = append(bodies, body)
		_, _ = w.Write([]byte(`{"id":"r-chat","roster":{"consultant":"seer"}}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	if _, err := client.ChatSwitch("r-chat", "seer", ""); err != nil {
		t.Fatal(err)
	}
	out, err := client.ChatSwitch("r-chat", "seer", "mcp:elena")
	if err != nil {
		t.Fatal(err)
	}
	if bodies[0]["duckling"] != "seer" {
		t.Errorf("switch body = %v", bodies[0])
	}
	if _, ok := bodies[0]["actor"]; ok {
		t.Errorf("a person's switch sent an actor: %v", bodies[0])
	}
	if bodies[1]["actor"] != "mcp:elena" {
		t.Errorf("operator switch body = %v", bodies[1])
	}
	if roster, _ := out["roster"].(map[string]interface{}); roster["consultant"] != "seer" {
		t.Errorf("switch result = %v", out)
	}
}
