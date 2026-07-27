package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
		Model:     os.Getenv("DUCKLAB_PROBE_MODEL"),
		Messages:  []Message{{Role: "user", Content: "Say hello in five words."}},
		MaxTokens: func() *int { n := 64; return &n }(),
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
	// A streamed run used to record zero tokens, which silently disabled every
	// budget: a limit that counts nothing never stops anything (I3).
	if resp.Usage.CompletionTokens == 0 {
		t.Error("no usage on a streamed response; budgets cannot be enforced")
	}
	t.Logf("usage: prompt=%d completion=%d", resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
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

// The bytes vLLM actually sends, replayed. A streamed run recorded zero
// tokens, and zero-token budgets stop nothing (I3) — but reading the parser
// did not show why, so this replays a captured stream instead of arguing
// about it.
func TestStreamedUsageIsCaptured(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		"",
		`data: {"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		"",
		`data: {"choices":[{"index":0,"delta":{"content":" there"},"finish_reason":"length"}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":11,"total_tokens":16,"completion_tokens":5}}`,
		"",
		"data: [DONE]",
		"",
		"",
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]interface{}
		json.NewDecoder(r.Body).Decode(&got)
		if got["stream_options"] == nil {
			t.Error("stream_options missing: the server sends no usage without it")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	p := NewOpenAICompat("probe", srv.URL, "")
	ch := make(chan Delta, 64)
	go func() {
		for range ch {
		}
	}()
	resp, err := p.ChatStream(context.Background(), ChatRequest{Model: "m"}, ch)
	close(ch)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.CompletionTokens != 5 || resp.Usage.PromptTokens != 11 {
		t.Errorf("usage = %+v, want prompt 11 completion 5", resp.Usage)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "hi there" {
		t.Errorf("content lost: %+v", resp.Choices)
	}
}

// A reader may return data AND io.EOF in the same call — http bodies routinely
// do, strings.Reader never does. The scanner used to emit everything left as
// one event on EOF, handing the parser several concatenated frames as a single
// malformed one; it dropped the lot. Live runs still looked fine because only
// the tail was affected — which is exactly where usage lives.
type eofWithData struct {
	data []byte
	done bool
}

func (r *eofWithData) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	n := copy(p, r.data)
	return n, io.EOF
}

func TestSSEScannerHandlesDataDeliveredWithEOF(t *testing.T) {
	body := "data: {\"a\":1}\n\ndata: {\"b\":2}\n\ndata: [DONE]\n\n"
	s := newSSEScanner(&eofWithData{data: []byte(body)})

	var got []string
	for s.scan() {
		got = append(got, s.event())
	}
	want := []string{`{"a":1}`, `{"b":2}`, "[DONE]"}
	if !slices.Equal(got, want) {
		t.Errorf("events = %q, want %q", got, want)
	}
}

// A final frame with no blank line after it must still be delivered.
func TestSSEScannerDeliversAnUnterminatedFinalFrame(t *testing.T) {
	s := newSSEScanner(&eofWithData{data: []byte("data: {\"a\":1}\n\ndata: {\"b\":2}")})
	var got []string
	for s.scan() {
		got = append(got, s.event())
	}
	if !slices.Equal(got, []string{`{"a":1}`, `{"b":2}`}) {
		t.Errorf("events = %q; the last frame was lost", got)
	}
}
