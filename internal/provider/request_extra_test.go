package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatRequestFlattensProviderExtrasOnTheWire(t *testing.T) {
	wire, err := json.Marshal(ChatRequest{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "hello"}},
		Extra: map[string]interface{}{
			"reasoning": map[string]interface{}{"enabled": false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(wire)
	if strings.Contains(text, `"extra"`) {
		t.Fatalf("provider control was hidden in a non-API extra object: %s", text)
	}
	if !strings.Contains(text, `"reasoning":{"enabled":false}`) {
		t.Fatalf("reasoning control is not top-level: %s", text)
	}
}

func TestChatRequestRejectsExtraCollision(t *testing.T) {
	_, err := json.Marshal(ChatRequest{Model: "m", Extra: map[string]interface{}{"model": "other"}})
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("collision error = %v", err)
	}
}
