package provider

import (
	"context"
	"strings"
	"testing"
)

// The double behaved differently on the path under test.
//
// Fake.ChatStream served only the pre-scripted queue and ignored ScriptFunc, so
// every test that scripts by prompt — reviewer, judge, triager — silently
// stopped working the moment a run streamed. That is why nothing caught five of
// the six run kinds never wiring their streaming callbacks.
func TestTheFakeStreamsWhatItWouldHaveAnswered(t *testing.T) {
	f := NewFake("f")
	f.ScriptFunc = func(req ChatRequest, _ int) *ChatResponse {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "triager") {
				return &ChatResponse{Choices: []Choice{{
					Message:      Message{Role: "assistant", Content: `{"severity":"critical"}`, Reasoning: "weighing it"},
					FinishReason: FinishStop,
				}}}
			}
		}
		return nil
	}

	ch := make(chan Delta, 16)
	var text, reasoning strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		for d := range ch {
			text.WriteString(d.Text)
			reasoning.WriteString(d.Reasoning)
		}
	}()
	resp, err := f.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "system", Content: "You are the triager."}},
	}, ch)
	close(ch)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "critical") {
		t.Errorf("the scripted answer never reached the stream: %q", text.String())
	}
	if reasoning.String() != "weighing it" {
		t.Errorf("reasoning = %q", reasoning.String())
	}
	if resp.Choices[0].Message.Content == "" {
		t.Error("the assembled response is empty")
	}
}
