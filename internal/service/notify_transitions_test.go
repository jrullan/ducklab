package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
)

// Operator-relevant run transitions are doorbells, not polling hints. The
// payload keeps the transition-specific information an MCP operator needs to
// choose its next action.
func TestTheWebhookAnnouncesRunTransitionsAndDistress(t *testing.T) {
	var mu sync.Mutex
	var payloads []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
			return
		}
		mu.Lock()
		payloads = append(payloads, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	isolate(t)
	cfg := config.DefaultGlobal()
	cfg.Notify.WebhookURL = srv.URL
	s, err := New(cfg, Options{Bus: bus.New(16)})
	if err != nil {
		t.Fatal(err)
	}

	events := []bus.Event{
		{Type: "run_end", RunID: "r-end", ProjectID: "p", TS: time.Now(), Data: map[string]interface{}{"verdict": "FAILED"}},
		{Type: "run_paused", RunID: "r-pause", ProjectID: "p", TS: time.Now(), Data: map[string]interface{}{"pending_kind": "budget"}},
		{Type: "question_asked", RunID: "r-question", ProjectID: "p", TS: time.Now(), Data: map[string]interface{}{"question": "Which API?"}},
		{Type: "distress", RunID: "r-distress", ProjectID: "p", TS: time.Now(), Data: map[string]interface{}{"reason": "repetition_loop"}},
	}
	for _, event := range events {
		s.bus.Publish(event)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(payloads)
		mu.Unlock()
		if n >= len(events) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != len(events) {
		t.Fatalf("webhook payloads = %d, want %d", len(payloads), len(events))
	}
	for i, want := range events {
		got := payloads[i]
		if got["event"] != want.Type || got["run_id"] != want.RunID || got["project_id"] != want.ProjectID {
			t.Errorf("payload %d identity = %v, want event=%q run_id=%q project_id=%q", i, got, want.Type, want.RunID, want.ProjectID)
		}
		data, _ := got["data"].(map[string]interface{})
		for key, value := range want.Data {
			if data[key] != value {
				t.Errorf("payload %d data[%q] = %v, want %v", i, key, data[key], value)
			}
		}
	}
}
