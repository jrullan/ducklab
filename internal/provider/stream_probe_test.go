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

// Streamed tool calls arrive in fragments: the name in one chunk, the
// arguments split across several more, each tagged with an index. They used to
// be appended as if each fragment were a whole call, so the assistant message
// sent back carried one call with a name and no arguments and several with a
// slice of JSON and no name. vLLM answered 400 "Expecting value: line 1 column
// 10" and the run died right after streaming its first turn perfectly.
func TestStreamedToolCallsAreReassembled(t *testing.T) {
	var a toolCallAccumulator
	frag := func(i int, id, name, args string) streamToolCall {
		var f streamToolCall
		f.Index, f.ID = i, id
		f.Function.Name, f.Function.Arguments = name, args
		return f
	}
	// Two interleaved calls, as a model emitting them in parallel would.
	a.add(frag(0, "call_a", "fs_read", ""))
	a.add(frag(1, "call_b", "fs_patch", ""))
	a.add(frag(0, "", "", `{"path":`))
	a.add(frag(1, "", "", `{"path":"b.go"}`))
	a.add(frag(0, "", "", `"a.go"}`))

	got := a.result()
	if len(got) != 2 {
		t.Fatalf("got %d tool calls, want 2 — fragments were counted as calls", len(got))
	}
	if got[0].ID != "call_a" || got[0].Function.Name != "fs_read" {
		t.Errorf("first call lost its identity: %+v", got[0])
	}
	if got[0].Function.Arguments != `{"path":"a.go"}` {
		t.Errorf("arguments = %q, want valid reassembled JSON", got[0].Function.Arguments)
	}
	if got[1].Function.Arguments != `{"path":"b.go"}` {
		t.Errorf("second call's arguments = %q", got[1].Function.Arguments)
	}
	if got[0].Type != "function" {
		t.Errorf("type = %q; a call the server will accept needs one", got[0].Type)
	}
}
