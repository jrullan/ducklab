package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type routingCapture struct{ request ChatRequest }

func (p *routingCapture) ID() string                               { return "openrouter" }
func (p *routingCapture) Models(context.Context) ([]string, error) { return nil, nil }
func (p *routingCapture) Chat(_ context.Context, req ChatRequest) (ChatResponse, error) {
	p.request = req
	return ChatResponse{}, nil
}
func (p *routingCapture) ChatStream(_ context.Context, req ChatRequest, _ chan<- Delta) (ChatResponse, error) {
	p.request = req
	return ChatResponse{}, nil
}

func TestOpenRouterEndpointPinsOneUpstreamWithoutFallbacks(t *testing.T) {
	base := &routingCapture{}
	routed := WithOpenRouterEndpoint(base, "deepinfra/fp4")
	if _, err := routed.Chat(context.Background(), ChatRequest{Model: "z-ai/glm-5.2"}); err != nil {
		t.Fatal(err)
	}
	if base.request.Provider == nil || len(base.request.Provider.Only) != 1 || base.request.Provider.Only[0] != "deepinfra/fp4" {
		t.Fatalf("provider routing = %#v", base.request.Provider)
	}
	if base.request.Provider.AllowFallbacks == nil || *base.request.Provider.AllowFallbacks {
		t.Fatalf("fallbacks were not disabled: %#v", base.request.Provider)
	}
	wire, err := json.Marshal(base.request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"provider":{"only":["deepinfra/fp4"],"allow_fallbacks":false}`) {
		t.Fatalf("OpenRouter wire contract = %s", wire)
	}
}
