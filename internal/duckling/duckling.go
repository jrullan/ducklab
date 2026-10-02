// Package duckling manages duckling registration, health probing, and
// capability detection.
package duckling

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

// Duckling is a named, configured model participant.
type Duckling struct {
	ID                 config.DucklingID     `json:"id"`
	Provider           config.ProviderID     `json:"provider"`
	Model              string                `json:"model"`
	OpenRouterProvider string                `json:"openrouter_provider,omitempty"`
	Tier               config.ModelTier      `json:"tier,omitempty"`
	Roles              []config.Role         `json:"roles,omitempty"`
	Notes              string                `json:"notes,omitempty"`
	Params             config.SamplingParams `json:"params"`
	Caps               Capabilities          `json:"caps"`
	Cost               config.Cost           `json:"cost"`
	// Color is which of the eight series slots this duckling is drawn in, or 0
	// to let the fleet order decide.
	//
	// The colour used to come from a duckling's position in whatever list the
	// view had to hand: the run's roster in a transcript, the fleet listing on
	// the Ducklings page. So one model was blue as an architect and orange as an
	// implementer, and a reader could never learn "orange is that one".
	Color int `json:"color,omitempty"`
	// Fallback is the declared stand-in for provider weather; "auto" delegates
	// the choice to the person's Flock candidate criteria at reseat time.
	Fallback string `json:"fallback,omitempty"`
}

// Capabilities describes what a duckling can do.
type Capabilities struct {
	NativeTools         bool   `json:"native_tools"`
	JSONMode            bool   `json:"json_mode"`
	ContextTokens       int    `json:"context_tokens"`
	Vision              bool   `json:"vision"`
	ThinkingControl     string `json:"thinking_control,omitempty"`
	ThinkingControlNote string `json:"thinking_control_note,omitempty"`
	ProbedAt            string `json:"probed_at,omitempty"` // RFC3339; empty means never probed
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
	ducklings     map[config.DucklingID]*Duckling
	providers     map[config.ProviderID]provider.Provider
	caps          *CapsCache
	probeMu       sync.RWMutex
	probeFailures map[config.DucklingID]string
}

// NewRegistry creates a new duckling registry.
func NewRegistry() *Registry {
	return &Registry{
		ducklings:     make(map[config.DucklingID]*Duckling),
		providers:     make(map[config.ProviderID]provider.Provider),
		probeFailures: make(map[config.DucklingID]string),
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
	previous := r.ducklings[d.ID]
	r.ducklings[d.ID] = d
	if previous == nil || previous.Provider != d.Provider || previous.Model != d.Model || previous.OpenRouterProvider != d.OpenRouterProvider {
		r.clearProbeFailure(d.ID)
	}
	return nil
}

// Unregister removes a duckling.
//
// Needed because ducklings can now be deleted while the engine runs. Without
// it a removed duckling stays reachable until a restart, so the config and the
// engine disagree about what exists.
func (r *Registry) Unregister(id config.DucklingID) {
	delete(r.ducklings, id)
	r.clearProbeFailure(id)
}

// LastProbeFailed reports whether the most recent explicit or launch-time
// capability probe could not get even one chat response. Failures are kept in
// memory only: provider weather should influence automatic seating in this
// engine session, not become a durable verdict about a model.
func (r *Registry) LastProbeFailed(id config.DucklingID) bool {
	r.probeMu.RLock()
	defer r.probeMu.RUnlock()
	_, failed := r.probeFailures[id]
	return failed
}

func (r *Registry) recordProbeFailure(id config.DucklingID, err error) {
	r.probeMu.Lock()
	defer r.probeMu.Unlock()
	r.probeFailures[id] = err.Error()
}

func (r *Registry) clearProbeFailure(id config.DucklingID) {
	r.probeMu.Lock()
	defer r.probeMu.Unlock()
	delete(r.probeFailures, id)
}

// RecordProviderResult folds real run traffic into the same short-lived
// health signal as a capability probe. A declared native-tools capability can
// skip launch-time probing, but a 404/refused/DNS failure from the actual chat
// still proves that endpoint should not be selected automatically again.
func (r *Registry) RecordProviderResult(id config.DucklingID, err error) {
	r.applyHealth(id, err, err, false)
}

// healthVerdict is what one chat result says about whether a duckling's
// endpoint can serve it.
type healthVerdict int

const (
	healthUnchanged healthVerdict = iota // says nothing about reachability
	healthAlive                          // the endpoint answered: clear any mark
	healthDead                           // the endpoint cannot serve this duckling
)

// chatHealth is the one rule both the capability probe and run traffic use
// (review of #131: they decided separately and drifted). An answer, and a
// rate limit — the endpoint answered, it is only throttling — prove the
// endpoint alive and clear an older mark. Not found, refused/unreachable and
// rejected credentials mark it dead. Anything else (a malformed reply, a 400,
// a vision rejection) is about the request, not the endpoint, and leaves the
// signal as it was — except for the probe's own minimal request (see
// applyHealth).
func chatHealth(err error) healthVerdict {
	switch {
	case err == nil, errors.Is(err, provider.ErrRateLimit):
		return healthAlive
	case errors.Is(err, provider.ErrChatUnavailable), errors.Is(err, provider.ErrAuth), errors.Is(err, provider.ErrProviderUnavailable):
		return healthDead
	}
	return healthUnchanged
}

// applyHealth records or clears the duckling's mark from one chat result;
// failure is what is retained when the verdict is dead. minimal is true for
// the probe's "Reply with ok.": when even that fails, for any reason but a
// rate limit, the duckling cannot chat (B-464: a non-LLM service that
// answered every request with an unclassified error), so an unclassified
// failure counts as dead there. In run traffic it is about the request.
func (r *Registry) applyHealth(id config.DucklingID, err, failure error, minimal bool) {
	verdict := chatHealth(err)
	if verdict == healthUnchanged && minimal {
		verdict = healthDead
	}
	switch verdict {
	case healthAlive:
		r.clearProbeFailure(id)
	case healthDead:
		r.recordProbeFailure(id, failure)
	}
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
		copy := *d
		if cached, ok := r.CachedCaps(d.ID); ok {
			copy.Caps = *cached
		}
		result = append(result, &copy)
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
	return provider.WithOpenRouterEndpoint(p, d.OpenRouterProvider), nil
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

// VerifyVision verifies a declared vision claim with one image request when
// no fresh answer is cached. Explicit image rejection is a definitive negative;
// other probe failures are returned so callers do not mistake endpoint weather
// for a text-only model.
func (r *Registry) VerifyVision(ctx context.Context, id config.DucklingID) (bool, error) {
	d, err := r.Get(id)
	if err != nil {
		return false, err
	}
	if !d.Caps.Vision {
		return false, nil
	}
	if cached, ok := r.CachedCaps(id); ok {
		return cached.Vision, nil
	}
	// Probe records the whole capability answer, including vision, and keeps
	// existing successful endpoints from paying a second image request.
	if _, err := r.Probe(ctx, id); err != nil {
		return false, err
	}
	if cached, ok := r.CachedCaps(id); ok {
		return cached.Vision, nil
	}
	p, err := r.Provider(id)
	if err != nil {
		return false, err
	}
	_, err = p.Chat(ctx, provider.ChatRequest{
		Model: d.Model,
		Messages: []provider.Message{{
			Role: "user", Content: "Reply with ok.",
			Images: []string{"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/bwAAAABJRU5ErkJggg=="},
		}},
		MaxTokens: intPtr(4),
	})
	if err != nil && !provider.IsVisionUnsupported(err) {
		// The endpoint did not answer the capability question. Leave the
		// declaration in place rather than turning temporary provider weather
		// into a durable text-only verdict.
		return true, nil
	}
	vision := err == nil
	if r.caps == nil {
		r.caps = LoadCapsCache()
	}
	caps := d.Caps
	caps.Vision = vision
	_ = r.caps.Put(d.Provider, capabilityCacheModel(d), &caps)
	return vision, nil
}

// CachedCaps returns a cached record without probing, for listings.
func (r *Registry) CachedCaps(id config.DucklingID) (*Capabilities, bool) {
	d, err := r.Get(id)
	if err != nil || r.caps == nil {
		return nil, false
	}
	return r.caps.Get(d.Provider, capabilityCacheModel(d))
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
		if cached, ok := r.caps.Get(d.Provider, capabilityCacheModel(d)); ok {
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

	// First: does the endpoint answer a chat at all? Every capability probe
	// below reads a failure as "not supported", so an endpoint that answered
	// nothing (a starter duckling at localhost:8080 where another service
	// returned 404 to everything) came back "probed", with vision inferred
	// true from an unrelated error and the result cached for a month (B-464).
	if _, err := p.Chat(ctx, provider.ChatRequest{
		Model:     d.Model,
		Messages:  []provider.Message{{Role: "user", Content: "Reply with ok."}},
		MaxTokens: intPtr(8),
	}); err != nil {
		failure := fmt.Errorf("%s did not answer a chat at %s: %w", d.ID, d.Model, err)
		// The same health rule as run traffic: a rate limit clears an older
		// mark (the endpoint answered), a dead endpoint sets one.
		r.applyHealth(id, err, failure, true)
		return nil, failure
	}
	r.clearProbeFailure(id)

	// OpenRouter has two materially different answers to "disable thinking":
	// some endpoints accept reasoning.enabled=false, while mandatory-reasoning
	// endpoints reject it. `exclude:true` is not suppression — it only hides the
	// paid reasoning from Ducklab — so probe the actual control and preserve the
	// endpoint's answer for the card, roster, and agent loop.
	if d.Params.DisableThinking && isOpenRouterDuckling(d) {
		thinkingReq := provider.ChatRequest{
			Model:     d.Model,
			Messages:  []provider.Message{{Role: "user", Content: "Reply with ok."}},
			MaxTokens: intPtr(4),
			Extra: map[string]interface{}{
				"reasoning": map[string]interface{}{"enabled": false},
			},
		}
		_, thinkingErr := p.Chat(ctx, thinkingReq)
		switch {
		case thinkingErr == nil:
			caps.ThinkingControl = "disabled"
			caps.ThinkingControlNote = "endpoint accepted reasoning.enabled=false"
		case strings.Contains(strings.ToLower(thinkingErr.Error()), "reasoning is mandatory"):
			caps.ThinkingControl = "mandatory"
			caps.ThinkingControlNote = "endpoint requires reasoning; Ducklab will keep it visible"
		default:
			caps.ThinkingControl = "unknown"
			caps.ThinkingControlNote = "thinking control probe failed: " + thinkingErr.Error()
		}
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

	// Verify vision with a real image part. A configured vision:true is only a
	// claim; local OpenAI-compatible servers can accept chat while lacking the
	// llama.cpp mmproj projector.
	visionReq := provider.ChatRequest{
		Model: d.Model,
		Messages: []provider.Message{{
			Role: "user", Content: "Reply with ok.",
			Images: []string{"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/bwAAAABJRU5ErkJggg=="},
		}},
		MaxTokens: intPtr(4),
	}
	_, err = p.Chat(ctx, visionReq)
	// Only an explicit image rejection disproves vision. A successful endpoint
	// may answer this tiny non-streaming probe in a streaming wire shape; that
	// protocol mismatch is not evidence that it cannot see.
	caps.Vision = !provider.IsVisionUnsupported(err)

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
	_ = r.caps.Put(d.Provider, capabilityCacheModel(d), caps)
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
		ID:                 id,
		Provider:           cfg.Provider,
		Model:              cfg.Model,
		OpenRouterProvider: cfg.OpenRouterProvider,
		Tier:               cfg.Tier,
		Roles:              cfg.Roles,
		Notes:              cfg.Notes,
		Params:             cfg.Params,
		Cost:               cfg.Cost,
		Color:              cfg.Color,
		Fallback:           cfg.Fallback,
		Caps:               Capabilities{ContextTokens: 32768},
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
	// Same disease as native_tools above, found the same way: vision was
	// declared in config, saved faithfully, and dropped here — so the list
	// reported false for every duckling and the edit form un-ticked the box
	// the person had just ticked.
	if cfg.Caps.Vision != nil {
		d.Caps.Vision = *cfg.Caps.Vision
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
		NativeTools:     c.NativeTools,
		JSONMode:        c.JSONMode,
		ContextTokens:   c.ContextTokens,
		Vision:          c.Vision,
		ThinkingControl: c.ThinkingControl,
	}
}

func isOpenRouterDuckling(d *Duckling) bool {
	return d != nil && (d.OpenRouterProvider != "" || strings.Contains(strings.ToLower(string(d.Provider)), "openrouter"))
}

// OpenRouter capabilities belong to a concrete upstream, not merely to the
// public model slug. Two ducklings can select endpoints with different
// mandatory-reasoning policies, prices, and quantizations.
func capabilityCacheModel(d *Duckling) string {
	if d == nil || d.OpenRouterProvider == "" {
		if d == nil {
			return ""
		}
		return d.Model
	}
	return d.Model + "@" + d.OpenRouterProvider
}
