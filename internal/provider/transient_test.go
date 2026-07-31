package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A 504 killed a run on the first try.
//
// OpenRouter answers 200 with an `error` object, so an upstream gateway timeout
// arrives looking like a well-formed response. Classed as "invalid response" it
// was not transient, so the retry policy never fired — and a gateway timeout is
// exactly what retries exist for.
func TestAnUpstreamTimeoutIsTransient(t *testing.T) {
	for _, tc := range []struct {
		code string
		want error
	}{
		{"504", ErrProviderUnavailable},
		{"503", ErrProviderUnavailable},
		{"429", ErrRateLimit},
		// A code nobody recognises is not a reason to retry forever.
		{"400", ErrInvalidResponse},
		{"402", ErrInvalidResponse},
	} {
		t.Run(tc.code, func(t *testing.T) {
			body := fmt.Sprintf(`{"error":{"message":"The operation was aborted","code":%s}}`, tc.code)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, body)
			}))
			defer srv.Close()

			_, err := NewOpenAICompat("t", srv.URL, "").Chat(context.Background(),
				ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "go"}}})
			if err == nil {
				t.Fatal("accepted a response with no choices")
			}
			if !strings.Contains(err.Error(), "The operation was aborted") {
				t.Errorf("the provider's own words were lost: %v", err)
			}
			transient := IsTransient(err)
			wantTransient := tc.want != ErrInvalidResponse
			if transient != wantTransient {
				t.Errorf("code %s: transient=%v, want %v (%v)", tc.code, transient, wantTransient, err)
			}
		})
	}
}

// The point of the classification: the call is actually made again.
func TestATransientUpstreamFailureIsRetried(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			fmt.Fprint(w, `{"error":{"message":"The operation was aborted","code":504}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	p := NewOpenAICompat("t", srv.URL, "")
	req := ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "go"}}}

	var resp ChatResponse
	err := Retry(context.Background(), RetryPolicy{MaxAttempts: 3, InitialWait: time.Millisecond, MaxWait: time.Millisecond}, func() error {
		var e error
		resp, e = p.Chat(context.Background(), req)
		return e
	})
	if err != nil {
		t.Fatalf("the retry gave up on a transient failure: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("the call was made %d time(s); the second one is the point", hits.Load())
	}
	if resp.Choices[0].Message.Content != "ok" {
		t.Errorf("content = %q", resp.Choices[0].Message.Content)
	}
}
