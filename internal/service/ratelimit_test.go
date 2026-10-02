package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/provider"
)

// Jose's intake (2026-10-02) died on its first call: OpenRouter's upstream
// for the architect answered 429 "temporarily rate-limited upstream. Please
// retry shortly", the streamed call classified it as a plain error, and the
// run failed. Rate limiting is weather: it is retried, and once retries are
// spent the run pauses, resumable, instead of failing.
func TestARateLimitedStageRunRetriesThenPausesResumable(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"Provider returned error","code":429,"metadata":{"raw":"moonshotai/kimi-k3 is temporarily rate-limited upstream. Please retry shortly"}}}`)
	}))
	defer srv.Close()
	s := serviceWithDucklings(t, "pato-uno")
	s.ducklings.RegisterProvider(provider.NewOpenAICompat("fake", srv.URL, ""))
	projectID, _ := projectWithDocs(t, s, map[artifact.Kind]string{})

	run, err := s.StageStart(context.Background(), projectID, StageRequest{Stage: "intake", Mode: "solo", From: "A calculator."})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	<-rs.done
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := detail.Run
	if r.Status != "paused" || r.PendingKind != "provider" {
		t.Fatalf("status = %s/%s, want paused/provider; failure = %q", r.Status, r.PendingKind, r.Failure)
	}
	if n := calls.Load(); n < 2 {
		t.Fatalf("the rate-limited call was not retried (%d call)", n)
	}
	if !strings.Contains(r.Failure, "rate limited") {
		t.Fatalf("failure does not say rate limited: %q", r.Failure)
	}
	// Weather is not a dead endpoint: a rate-limited duckling stays eligible
	// for automatic seating (the #128 exclusion is for 404/refused/auth).
	if s.ducklings.LastProbeFailed("pato-uno") {
		t.Fatal("a rate limit excluded the duckling from automatic seating")
	}
}
