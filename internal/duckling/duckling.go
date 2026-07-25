// Package duckling manages duckling registration, health probing, and
// capability detection.
package duckling

import (
	"context"
	"fmt"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

// Duckling is a named, configured model participant.
type Duckling struct {
	ID       config.DucklingID
	Provider config.ProviderID
	Model    string
	Roles    []config.Role
	Notes    string
	Params   config.SamplingParams
	Caps     Capabilities
	Cost     config.Cost
}

// Capabilities describes what a duckling can do.
type Capabilities struct {
	NativeTools   bool `json:"native_tools"`
	JSONMode      bool `json:"json_mode"`
	ContextTokens int  `json:"context_tokens"`
	Vision        bool `json:"vision"`
}

// HealthStatus is the health of a duckling.
type HealthStatus string

const (
	HealthUnknown     HealthStatus = "unknown"
	HealthHealthy     HealthStatus = "healthy"
	HealthUnreachable HealthStatus = "unreachable"
	HealthAuthFailed  HealthStatus = "auth_failed"
)

// Health is the result of a health probe.
type Health struct {
	Status    HealthStatus
	Latency   time.Duration
	Error     string
	CheckedAt time.Time
}

// Registry manages ducklings.
type Registry struct {
	ducklings map[config.DucklingID]*Duckling
	providers map[config.ProviderID]provider.Provider
}

// NewRegistry creates a new duckling registry.
func NewRegistry() *Registry {
	return &Registry{
		ducklings: make(map[config.DucklingID]*Duckling),
		providers: make(map[config.ProviderID]provider.Provider),
	}
}

// Register registers a duckling.
func (r *Registry) Register(d *Duckling) error {
	if d.ID == "" {
		return fmt.Errorf("duckling id is required")
	}
	if _, exists := r.ducklings[d.ID]; exists {
		return fmt.Errorf("duckling %q already registered", d.ID)
	}
	r.ducklings[d.ID] = d
	return nil
}

// RegisterProvider registers a provider for ducklings.
func (r *Registry) RegisterProvider(p provider.Provider) {
	r.providers[config.ProviderID(p.ID())] = p
}

// Get returns a duckling by ID.
func (r *Registry) Get(id config.DucklingID) (*Duckling, error) {
	d, ok := r.ducklings[id]
	if !ok {
		return nil, fmt.Errorf("duckling %q not found", id)
	}
	return d, nil
}

// List returns all registered ducklings.
func (r *Registry) List() []*Duckling {
	result := make([]*Duckling, 0, len(r.ducklings))
	for _, d := range r.ducklings {
		result = append(result, d)
	}
	return result
}

// Provider returns the provider for a duckling.
func (r *Registry) Provider(id config.DucklingID) (provider.Provider, error) {
	d, err := r.Get(id)
	if err != nil {
		return nil, err
	}
	p, ok := r.providers[d.Provider]
	if !ok {
		return nil, fmt.Errorf("provider %q for duckling %q not found", d.Provider, id)
	}
	return p, nil
}

// Probe probes a duckling's capabilities.
func (r *Registry) Probe(ctx context.Context, id config.DucklingID) (*Capabilities, error) {
	d, err := r.Get(id)
	if err != nil {
		return nil, err
	}
	p, err := r.Provider(id)
	if err != nil {
		return nil, err
	}

	caps := &Capabilities{
		NativeTools:   false,
		JSONMode:      false,
		ContextTokens: 32768,
	}

	// Check if the model supports native tool calling
	// by sending a trivial tool request
	probeReq := provider.ChatRequest{
		Model: d.Model,
		Messages: []provider.Message{
			{Role: "user", Content: "Call the tool with ok=true"},
		},
		Tools: []provider.Tool{{
			Type: "function",
			Function: provider.ToolFunction{
				Name:        "ducklab_probe",
				Description: "Probe tool",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"ok": map[string]interface{}{
							"type": "boolean",
						},
					},
				},
			},
		}},
		MaxTokens: intPtr(10),
	}

	resp, err := p.Chat(ctx, probeReq)
	if err == nil && len(resp.Choices) > 0 && len(resp.Choices[0].Message.ToolCalls) > 0 {
		caps.NativeTools = true
	}

	// Check JSON mode
	jsonReq := provider.ChatRequest{
		Model: d.Model,
		Messages: []provider.Message{
			{Role: "user", Content: "Reply with exactly: {\"ok\":true}"},
		},
		MaxTokens: intPtr(20),
		JSONMode:  true,
	}
	resp, err = p.Chat(ctx, jsonReq)
	if err == nil && len(resp.Choices) > 0 {
		content := resp.Choices[0].Message.Content
		if content != "" {
			caps.JSONMode = true
		}
	}

	// Context window from models endpoint if available
	models, err := p.Models(ctx)
	if err == nil {
		// Look for context size hints in model metadata
		// This is provider-specific; for now use a default
		_ = models
	}

	return caps, nil
}

// HealthCheck performs a quick health check on a duckling.
func (r *Registry) HealthCheck(ctx context.Context, id config.DucklingID) *Health {
	h := &Health{
		Status:    HealthUnknown,
		CheckedAt: time.Now(),
	}
	p, err := r.Provider(id)
	if err != nil {
		h.Status = HealthUnreachable
		h.Error = err.Error()
		return h
	}

	start := time.Now()
	_, err = p.Models(ctx)
	h.Latency = time.Since(start)

	if err != nil {
		h.Status = HealthUnreachable
		h.Error = err.Error()
		return h
	}
	h.Status = HealthHealthy
	return h
}

// Test sends a test prompt to a duckling and returns the response.
func (r *Registry) Test(ctx context.Context, id config.DucklingID, prompt string, stream bool) (string, int, int, float64, error) {
	d, err := r.Get(id)
	if err != nil {
		return "", 0, 0, 0, err
	}
	p, err := r.Provider(id)
	if err != nil {
		return "", 0, 0, 0, err
	}

	req := provider.ChatRequest{
		Model: d.Model,
		Messages: []provider.Message{
			{Role: "user", Content: prompt},
		},
	}

	if stream {
		ch := make(chan provider.Delta, 100)
		go func() {
			defer close(ch)
			resp, err := p.ChatStream(ctx, req, ch)
			_ = resp
			_ = err
		}()
		var text string
		for delta := range ch {
			text += delta.Text
		}
		return text, 0, 0, 0, nil // tokens/cost not available in this simplified path
	}

	resp, err := p.Chat(ctx, req)
	if err != nil {
		return "", 0, 0, 0, err
	}
	if len(resp.Choices) == 0 {
		return "", 0, 0, 0, fmt.Errorf("no response choices")
	}

	calc := provider.CostCalculator{
		InputPerMTok:  d.Cost.InputPerMTok,
		OutputPerMTok: d.Cost.OutputPerMTok,
	}
	cost := calc.Cost(resp.Usage)

	return resp.Choices[0].Message.Content,
		resp.Usage.PromptTokens,
		resp.Usage.CompletionTokens,
		cost,
		nil
}

// FromConfig creates a Duckling from config.
func FromConfig(id config.DucklingID, cfg config.Duckling) *Duckling {
	return &Duckling{
		ID:       id,
		Provider: cfg.Provider,
		Model:    cfg.Model,
		Roles:    cfg.Roles,
		Notes:    cfg.Notes,
		Params:   cfg.Params,
		Cost:     cfg.Cost,
	}
}

func intPtr(i int) *int {
	return &i
}
