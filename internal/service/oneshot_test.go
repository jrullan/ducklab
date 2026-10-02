package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
	"github.com/jrullan/ducklab/internal/provider"
)

type capturingProvider struct{ got provider.ChatRequest }

func (c *capturingProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	c.got = req
	return provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Content: "ok"}}}}, nil
}
func (c *capturingProvider) ID() string { return "capturing" }
func (c *capturingProvider) ChatStream(ctx context.Context, req provider.ChatRequest, ch chan<- provider.Delta) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, provider.ErrUnsupported
}
func (c *capturingProvider) Models(ctx context.Context) ([]string, error) { return nil, nil }

// A one-shot call to a disable_thinking seat must suppress thinking exactly
// like the agent loop does, from the duckling's effective caps (B-123, B-479).
// This test used to assert chat_template_kwargs for qwen/qwen3.8-max — the
// local-server parameter OpenRouter ignores — because one-shots never saw the
// probed ThinkingControl. Each control now gets the request the loop sends.
func TestOneShotChatSuppressesThinkingAsTheLoopDoes(t *testing.T) {
	temp := 0.2
	d := &duckling.Duckling{
		Model:  "qwen/qwen3.8-max",
		Params: config.SamplingParams{DisableThinking: true, Temperature: &temp},
	}
	for _, c := range []struct {
		control       string
		wantReasoning bool // reasoning.enabled=false
		wantTemplate  bool // chat_template_kwargs.enable_thinking=false
	}{
		{"disabled", true, false},   // OpenRouter probe accepted the control
		{"mandatory", false, false}, // the endpoint refuses it; never lie
		{"", false, true},           // a local template server
	} {
		p := &capturingProvider{}
		if _, err := oneShotChat(context.Background(), p, d, &duckling.Capabilities{ThinkingControl: c.control}, "sys", "user", 2000); err != nil {
			t.Fatal(err)
		}
		if got := p.got.Extra["reasoning"] != nil; got != c.wantReasoning {
			t.Errorf("control %q: reasoning control sent = %v, want %v (%+v)", c.control, got, c.wantReasoning, p.got.Extra)
		}
		if got := p.got.Extra["chat_template_kwargs"] != nil; got != c.wantTemplate {
			t.Errorf("control %q: template kwargs sent = %v, want %v (%+v)", c.control, got, c.wantTemplate, p.got.Extra)
		}
		if p.got.Temperature == nil || *p.got.Temperature != 0.2 {
			t.Errorf("sampling params not applied: %+v", p.got.Temperature)
		}
		if p.got.MaxTokens == nil || *p.got.MaxTokens != 2000 {
			t.Errorf("max tokens not applied")
		}
	}
}
