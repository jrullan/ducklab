package provider

import "context"

// WithOpenRouterEndpoint pins every chat request sent through p to one
// concrete OpenRouter endpoint. Discovery calls remain unchanged.
func WithOpenRouterEndpoint(p Provider, endpoint string) Provider {
	if p == nil || endpoint == "" {
		return p
	}
	return &openRouterEndpointProvider{Provider: p, endpoint: endpoint}
}

type openRouterEndpointProvider struct {
	Provider
	endpoint string
}

func (p *openRouterEndpointProvider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	req.Provider = endpointPreferences(p.endpoint)
	return p.Provider.Chat(ctx, req)
}

func (p *openRouterEndpointProvider) ChatStream(ctx context.Context, req ChatRequest, ch chan<- Delta) (ChatResponse, error) {
	req.Provider = endpointPreferences(p.endpoint)
	return p.Provider.ChatStream(ctx, req, ch)
}

func endpointPreferences(endpoint string) *ProviderPreferences {
	noFallbacks := false
	return &ProviderPreferences{Only: []string{endpoint}, AllowFallbacks: &noFallbacks}
}
