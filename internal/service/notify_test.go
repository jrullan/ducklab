package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
)

// MCP is pull: an operator agent only learns a run settled when someone asks
// it to look. The engine announces the gate moments to one webhook — signed,
// best-effort, never blocking a run — and the agent's platform wakes it.
func TestTheWebhookAnnouncesGateMoments(t *testing.T) {
	var mu sync.Mutex
	type hit struct {
		body []byte
		sig  string
	}
	var hits []hit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, hit{body: body, sig: r.Header.Get("X-Hub-Signature-256")})
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	isolate(t)
	cfg := config.DefaultGlobal()
	cfg.Notify = config.Notify{WebhookURL: srv.URL, Secret: "quack"}
	s, err := New(cfg, Options{Bus: bus.New(64)})
	if err != nil {
		t.Fatal(err)
	}

	s.bus.Publish(bus.Event{
		Type: "human_needed", RunID: "r-1", ProjectID: "p", TS: time.Now(),
		Data: map[string]interface{}{"kind": "gate", "detail": "green, decide it"},
	})
	// Uninteresting types never leave the building.
	s.bus.Publish(bus.Event{Type: "token_delta", RunID: "r-1", TS: time.Now()})

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(hits)
		mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 {
		t.Fatalf("webhook hits = %d, want exactly the human_needed", len(hits))
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(hits[0].body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["event"] != "human_needed" || payload["run_id"] != "r-1" {
		t.Errorf("payload = %v", payload)
	}
	mac := hmac.New(sha256.New, []byte("quack"))
	mac.Write(hits[0].body)
	if hits[0].sig != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
		t.Error("the signature does not verify GitHub-style against the shared secret")
	}
}
