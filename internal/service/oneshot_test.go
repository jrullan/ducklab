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
// like the agent loop does — the raw request let qwen38-max reason into its
// 1200-token cap and the advisor card said "empty answer" (B-123).
func TestOneShotChatAppliesTheDucklingParams(t *testing.T) {
	temp := 0.2
	d := &duckling.Duckling{
		Model:  "qwen/qwen3.8-max",
		Params: config.SamplingParams{DisableThinking: true, Temperature: &temp},
	}
	p := &capturingProvider{}
	if _, err := oneShotChat(context.Background(), p, d, "sys", "user", 2000); err != nil {
		t.Fatal(err)
	}
	if p.got.Extra["chat_template_kwargs"] == nil || p.got.Extra["reasoning"] == nil {
		t.Errorf("thinking suppression missing from the request: %+v", p.got.Extra)
	}
	if p.got.Temperature == nil || *p.got.Temperature != 0.2 {
		t.Errorf("sampling params not applied: %+v", p.got.Temperature)
	}
	if p.got.MaxTokens == nil || *p.got.MaxTokens != 2000 {
		t.Errorf("max tokens not applied")
	}
}
