package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestRetryOnTransient500 verifies a transient 5xx is ridden out rather than
// killing the call — the exact failure that escalated a real plan-mode run.
func TestRetryOnTransient500(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			http.Error(w, `{"error":{"message":"EngineCore encountered an issue","code":500}}`, 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	s := &HTTPSource{SrcName: "fake", BaseURL: srv.URL + "/v1", ModelID: "m", Timeout: 5 * time.Second}
	var retried int32
	res, err := s.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}},
		Options{OnRetry: func(int, string) { atomic.AddInt32(&retried, 1) }})
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if res.Content != "ok" {
		t.Errorf("content = %q", res.Content)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 attempts (1 fail + 1 ok), got %d", calls)
	}
	if atomic.LoadInt32(&retried) != 1 {
		t.Errorf("OnRetry should fire once, fired %d", retried)
	}
}

// TestNoRetryOn4xx verifies client errors are not retried (retrying won't fix them).
func TestNoRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "bad request", 400)
	}))
	defer srv.Close()

	s := &HTTPSource{SrcName: "fake", BaseURL: srv.URL + "/v1", ModelID: "m", Timeout: 5 * time.Second}
	if _, err := s.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, Options{}); err == nil {
		t.Fatal("expected 4xx to error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("4xx must not retry, got %d attempts", calls)
	}
}
