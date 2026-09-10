package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type openRouterEndpointWire struct {
	ProviderName  string      `json:"provider_name"`
	Tag           string      `json:"tag"`
	Quantization  string      `json:"quantization"`
	ContextLength int         `json:"context_length"`
	MaxCompletion int         `json:"max_completion_tokens"`
	IsModerated   *bool       `json:"is_moderated"`
	Pricing       pricingWire `json:"pricing"`
	DataPolicy    struct {
		PromptTraining *bool  `json:"prompt_training"`
		Training       *bool  `json:"training"`
		Retention      string `json:"retention"`
	} `json:"data_policy"`
}

type pricingWire struct {
	Prompt     json.Number `json:"prompt"`
	Completion json.Number `json:"completion"`
}

// ModelEndpoints reads OpenRouter's concrete endpoint catalog and its ZDR
// catalog. The latter is best-effort: endpoint selection remains useful when
// the separate privacy request is unavailable, but its policy stays unknown.
func (p *OpenAICompat) ModelEndpoints(ctx context.Context, model string) ([]ModelEndpoint, error) {
	path, err := modelEndpointPath(model)
	if err != nil {
		return nil, err
	}
	var listing struct {
		Data struct {
			Endpoints []openRouterEndpointWire `json:"endpoints"`
		} `json:"data"`
	}
	if err := p.openRouterGet(ctx, path, &listing); err != nil {
		return nil, err
	}

	zdr := map[string]bool{}
	var privacy struct {
		Data []struct {
			Tag string `json:"tag"`
		} `json:"data"`
	}
	if err := p.openRouterGet(ctx, "/endpoints/zdr", &privacy); err == nil {
		for _, endpoint := range privacy.Data {
			zdr[endpoint.Tag] = true
		}
	}

	out := make([]ModelEndpoint, 0, len(listing.Data.Endpoints))
	for _, endpoint := range listing.Data.Endpoints {
		item := ModelEndpoint{
			ProviderName: endpoint.ProviderName, Tag: endpoint.Tag,
			Quantization: endpoint.Quantization, ContextTokens: endpoint.ContextLength,
			MaxOutputTokens: endpoint.MaxCompletion, Moderated: endpoint.IsModerated,
		}
		if value, err := endpoint.Pricing.Prompt.Float64(); err == nil {
			item.InputPerMTok = value * 1e6
		}
		if value, err := endpoint.Pricing.Completion.Float64(); err == nil {
			item.OutputPerMTok = value * 1e6
		}
		if endpoint.DataPolicy.PromptTraining != nil {
			item.PromptTraining = endpoint.DataPolicy.PromptTraining
		} else {
			item.PromptTraining = endpoint.DataPolicy.Training
		}
		if isZDR := zdr[endpoint.Tag]; isZDR {
			value := true
			item.ZeroDataRetention = &value
			item.DataRetention = "zero"
		} else if endpoint.DataPolicy.Retention != "" {
			value := strings.EqualFold(endpoint.DataPolicy.Retention, "zero") || strings.EqualFold(endpoint.DataPolicy.Retention, "none")
			item.ZeroDataRetention = &value
			item.DataRetention = endpoint.DataPolicy.Retention
		}
		out = append(out, item)
	}
	return out, nil
}

func modelEndpointPath(model string) (string, error) {
	author, slug, ok := strings.Cut(strings.TrimSpace(model), "/")
	if !ok || author == "" || slug == "" {
		return "", fmt.Errorf("OpenRouter model %q must use author/slug", model)
	}
	return "/models/" + url.PathEscape(author) + "/" + url.PathEscape(slug) + "/endpoints", nil
}

func (p *OpenAICompat) openRouterGet(ctx context.Context, path string, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.baseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	p.setHeaders(req)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OpenRouter endpoints: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode OpenRouter endpoints: %w", err)
	}
	return nil
}
