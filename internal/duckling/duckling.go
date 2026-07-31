// Package duckling manages duckling registration, health probing, and
// capability detection.
package duckling

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

// Duckling is a named, configured model participant.
type Duckling struct {
	ID       config.DucklingID     `json:"id"`
	Provider config.ProviderID     `json:"provider"`
	Model    string                `json:"model"`
	Roles    []config.Role         `json:"roles,omitempty"`
	Notes    string                `json:"notes,omitempty"`
	Params   config.SamplingParams `json:"params"`
	Caps     Capabilities          `json:"caps"`
	Cost     config.Cost           `json:"cost"`
	// Color is which of the eight series slots this duckling is drawn in, or 0
	// to let the fleet order decide.
	//
	// The colour used to come from a duckling's position in whatever list the
	// view had to hand: the run's roster in a transcript, the fleet listing on
	// the Ducklings page. So one model was blue as an architect and orange as an
	// implementer, and a reader could never learn "orange is that one".
	Color int `json:"color,omitempty"`
}

// Capabilities describes what a duckling can do.
type Capabilities struct {
	NativeTools   bool   `json:"native_tools"`
	JSONMode      bool   `json:"json_mode"`
	ContextTokens int    `json:"context_tokens"`
	Vision        bool   `json:"vision"`
	ProbedAt      string `json:"probed_at,omitempty"` // RFC3339; empty means never probed
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
	caps      *CapsCache
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

// Replace registers a duckling, overwriting any entry already under its id.
//
// Register deliberately refuses a duplicate — that guard is what catches the
// same duckling being loaded twice at startup. Editing one is the opposite
// case: the id is supposed to exist already. Routing an edit through Register
// meant every save of an existing duckling failed with "already registered",
// and it failed AFTER the new values had been written to config.toml — so the
// file, the running engine and the screen all disagreed.
//
// Probed capabilities are not lost: they live in the caps cache, keyed by
// provider and model, not on the entry being replaced.
func (r *Registry) Replace(d *Duckling) error {
	if d.ID == "" {
		return fmt.Errorf("duckling id is required")
	}
	r.ducklings[d.ID] = d
	return nil
}

// Unregister removes a duckling.
//
// Needed because ducklings can now be deleted while the engine runs. Without
// it a removed duckling stays reachable until a restart, so the config and the
// engine disagree about what exists.
func (r *Registry) Unregister(id config.DucklingID) {
	delete(r.ducklings, id)
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
// List returns every registered duckling, by id.
//
// Sorted, because it used to range a map: the fleet came back in a different
// order on every call, and anything downstream that assigned meaning by
// position — the colour a duckling is drawn in, for one — changed on reload.
func (r *Registry) List() []*Duckling {
	result := make([]*Duckling, 0, len(r.ducklings))
	for _, d := range r.ducklings {
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
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
	return r.probe(ctx, id, false)
}

// ProbeForce ignores the cache and re-probes. This is what
// `ducklab duckling probe` does: the user asked, so the cached answer is not
// what they want.
func (r *Registry) ProbeForce(ctx context.Context, id config.DucklingID) (*Capabilities, error) {
	return r.probe(ctx, id, true)
}

// CachedCaps returns a cached record without probing, for listings.
func (r *Registry) CachedCaps(id config.DucklingID) (*Capabilities, bool) {
	d, err := r.Get(id)
	if err != nil || r.caps == nil {
		return nil, false
	}
	return r.caps.Get(d.Provider, d.Model)
}

func (r *Registry) probe(ctx context.Context, id config.DucklingID, force bool) (*Capabilities, error) {
	d, err := r.Get(id)
	if err != nil {
		return nil, err
	}
	if r.caps == nil {
		r.caps = LoadCapsCache()
	}
	if !force {
		if cached, ok := r.caps.Get(d.Provider, d.Model); ok {
			return cached, nil
		}
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

	// Cache the result so the next run does not pay for these calls again.
	// A cache write failure must not fail the probe: the answer is correct,
	// it just will not be remembered.
	_ = r.caps.Put(d.Provider, d.Model, caps)
	caps.ProbedAt = time.Now().UTC().Format(time.RFC3339)

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
	d := &Duckling{
		ID:       id,
		Provider: cfg.Provider,
		Model:    cfg.Model,
		Roles:    cfg.Roles,
		Notes:    cfg.Notes,
		Params:   cfg.Params,
		Cost:     cfg.Cost,
		Color:    cfg.Color,
		Caps:     Capabilities{ContextTokens: 32768},
	}
	// Declared capabilities were dropped here, so a duckling that says
	// native_tools = true still listed as "text protocol" everywhere the
	// registry is read — including the desktop's Ducklings view.
	if cfg.Caps.NativeTools != nil {
		d.Caps.NativeTools = *cfg.Caps.NativeTools
	}
	if cfg.Caps.ContextTokens != nil {
		d.Caps.ContextTokens = *cfg.Caps.ContextTokens
	}
	return d
}

func intPtr(i int) *int {
	return &i
}

// ProviderCaps converts a probe record into the subset the provider layer
// needs. ProbedAt is deliberately dropped: it is metadata about the probe, not
// a capability, and the provider has no use for it.
func ProviderCaps(c *Capabilities) provider.Capabilities {
	if c == nil {
		return provider.Capabilities{ContextTokens: 32768}
	}
	return provider.Capabilities{
		NativeTools:   c.NativeTools,
		JSONMode:      c.JSONMode,
		ContextTokens: c.ContextTokens,
		Vision:        c.Vision,
	}
}
