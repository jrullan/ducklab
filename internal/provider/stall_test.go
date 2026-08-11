package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// T-075: an upstream accepted the request and sent nothing. The 300s client
// timeout held the run silent, and the retry chain behind it re-ran the same
// wait — twenty minutes for one stalled provider. The watchdog declares the
// silence in seconds, as the transient it is, so the retry starts while the
// person still believes in the run.
func TestAStalledStreamFailsFastAsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// Headers sent, then nothing: the T-075 shape.
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := NewOpenAICompat("test", srv.URL, "", WithStallTimeout(150*time.Millisecond))
	ch := make(chan Delta, 16)
	go func() {
		for range ch {
		}
	}()
	start := time.Now()
	_, err := p.ChatStream(context.Background(), ChatRequest{Model: "m"}, ch)
	close(ch)

	if err == nil {
		t.Fatal("a silent stream returned no error")
	}
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Errorf("err = %v, want ErrProviderUnavailable (the retry path's signal)", err)
	}
	if !strings.Contains(err.Error(), "stalled") {
		t.Errorf("the error does not name the stall: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s to notice a stall bounded at 150ms", elapsed)
	}
}

// The other half: a SLOW stream is not a stalled one. Chunks that keep
// arriving reset the watchdog, however long the whole generation takes —
// punishing a local model for thinking would re-create the disease.
func TestASlowButAliveStreamIsNotAStall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		for i := 0; i < 6; i++ {
			// Each gap is most of the stall budget; the total far exceeds it.
			time.Sleep(100 * time.Millisecond)
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"w%d \"}}]}\n\n", i)
			f.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		f.Flush()
	}))
	defer srv.Close()

	p := NewOpenAICompat("test", srv.URL, "", WithStallTimeout(150*time.Millisecond))
	ch := make(chan Delta, 64)
	go func() {
		for range ch {
		}
	}()
	resp, err := p.ChatStream(context.Background(), ChatRequest{Model: "m"}, ch)
	close(ch)
	if err != nil {
		t.Fatalf("a slow-but-alive stream was declared dead: %v", err)
	}
	if len(resp.Choices) == 0 || !strings.Contains(resp.Choices[0].Message.Content, "w5") {
		t.Errorf("the response lost content: %+v", resp.Choices)
	}
}
