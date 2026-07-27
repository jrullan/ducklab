package provider

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// A live probe, skipped unless DUCKLAB_PROBE_URL is set. It exists because a
// run sat on turn 0 for minutes with the endpoint idle, and reading the code
// could not say whether our client or the server was at fault.
func TestLiveStreamProbe(t *testing.T) {
	base := os.Getenv("DUCKLAB_PROBE_URL")
	if base == "" {
		t.Skip("set DUCKLAB_PROBE_URL to run")
	}
	p := NewOpenAICompat("probe", base, "")
	ch := make(chan Delta, 64)
	n := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for d := range ch {
			if d.Text != "" {
				n++
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := p.ChatStream(ctx, ChatRequest{
		Model:    os.Getenv("DUCKLAB_PROBE_MODEL"),
		Messages: []Message{{Role: "user", Content: "Say hello in five words."}},
	}, ch)
	close(ch)
	<-done
	t.Logf("elapsed=%v err=%v choices=%d deltas=%d", time.Since(start).Round(time.Millisecond), err, len(resp.Choices), n)
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	if n == 0 {
		t.Error("no deltas arrived; the endpoint streams but our client saw nothing")
	}
}

// The scanner was constructed with a full-length buffer, so its read window
// was empty, every Read returned (0, nil), and scan() spun forever without
// consuming a byte. Streaming never worked; a run that asked for it hung on
// its first turn while the endpoint sat idle.
func TestSSEScannerConsumesItsInput(t *testing.T) {
	body := "data: {\"a\":1}\n\ndata: {\"b\":2}\n\ndata: [DONE]\n\n"
	s := newSSEScanner(strings.NewReader(body))

	done := make(chan []string, 1)
	go func() {
		var got []string
		for s.scan() {
			got = append(got, s.event())
		}
		done <- got
	}()

	select {
	case got := <-done:
		want := []string{`{"a":1}`, `{"b":2}`, "[DONE]"}
		if !slices.Equal(got, want) {
			t.Errorf("events = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the scanner never finished: it is spinning without reading")
	}
}

// An event larger than the buffer must not reproduce the deadlock from the
// other end — a full buffer leaves the same empty read window.
func TestSSEScannerHandlesAnEventLargerThanItsBuffer(t *testing.T) {
	big := strings.Repeat("x", 20_000)
	s := newSSEScanner(strings.NewReader("data: " + big + "\n\n"))

	done := make(chan string, 1)
	go func() {
		if s.scan() {
			done <- s.event()
			return
		}
		done <- ""
	}()

	select {
	case got := <-done:
		if got != big {
			t.Errorf("got %d bytes, want %d", len(got), len(big))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a large event deadlocked the scanner")
	}
}
