package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The stream parser read delta.content and delta.tool_calls and nothing else.
//
// A reasoning model's thinking arrives in a different field, so it was
// generated, billed, and dropped in the parse before any event existed. A run
// that sat apparently idle for two minutes had nothing on screen to explain
// itself, and the tokens were on the invoice either way.
func TestStreamedThinkingReachesTheCaller(t *testing.T) {
	// vLLM and DeepSeek's own API use reasoning_content; OpenRouter uses
	// reasoning. Both appear here because a duckling's endpoint must not decide
	// whether its thinking is visible.
	chunks := []string{
		`{"choices":[{"delta":{"reasoning_content":"Let me check the "}}]}`,
		`{"choices":[{"delta":{"reasoning":"triangle inequality."}}]}`,
		`{"choices":[{"delta":{"content":"Done."},"finish_reason":"stop"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAICompat("test", srv.URL, "")
	ch := make(chan Delta, 32)
	var thinking, answer strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		for d := range ch {
			thinking.WriteString(d.Reasoning)
			answer.WriteString(d.Text)
		}
	}()
	resp, err := p.ChatStream(context.Background(),
		ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "go"}}}, ch)
	close(ch)
	<-done
	if err != nil {
		t.Fatal(err)
	}

	if got := thinking.String(); got != "Let me check the triangle inequality." {
		t.Errorf("thinking = %q; both vendor field names must be read", got)
	}
	// The two must never be mixed: the contract parser reads the answer, and
	// deliberation folded into it would be parsed as the reply.
	if got := answer.String(); got != "Done." {
		t.Errorf("answer = %q — thinking leaked into it", got)
	}
	if got := resp.Choices[0].Message.Reasoning; !strings.Contains(got, "triangle") {
		t.Errorf("the assembled response lost the thinking: %q", got)
	}
	if got := resp.Choices[0].Message.Content; got != "Done." {
		t.Errorf("assembled content = %q", got)
	}
}

// A non-streaming call must reach the same place, or whether thinking is
// visible would depend on whether the endpoint supports SSE.
func TestNonStreamingThinkingIsParsed(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","content":"Done.",` +
		`"reasoning_content":"Checking the law of cosines."},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":10,"completion_tokens":900,` +
		`"completion_tokens_details":{"reasoning_tokens":870}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	p := NewOpenAICompat("test", srv.URL, "")
	resp, err := p.Chat(context.Background(),
		ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "go"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Reasoning != "Checking the law of cosines." {
		t.Errorf("reasoning = %q", resp.Choices[0].Message.Reasoning)
	}
	// 870 of 900 completion tokens went to thinking. "The run spent 900 tokens"
	// and "the run spent 900 tokens, 870 of them thinking" call for different
	// actions, and only the second explains a budget that ran out with almost
	// nothing written.
	if resp.Usage.ReasoningTokens != 870 {
		t.Errorf("reasoning_tokens = %d, want 870", resp.Usage.ReasoningTokens)
	}
}

// Reasoning is a by-product of one turn, not part of the conversation. A field
// that decodes it would also encode it, and replaying a model's deliberation
// back to it is not a conversation.
func TestReasoningIsNeverSentBackInARequest(t *testing.T) {
	out, err := json.Marshal(Message{
		Role: "assistant", Content: "Done.", Reasoning: "Long deliberation.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "deliberation") {
		t.Errorf("thinking is serialised into the request: %s", out)
	}
}
