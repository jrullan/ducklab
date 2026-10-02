package duckling

import (
	"context"
	"fmt"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

// Vision declared in config must survive into the registry: it was saved
// faithfully and dropped in the conversion, so the list reported false for
// every duckling and the edit form un-ticked the box the person had just
// ticked — while attached screenshots reached no model.
func TestDeclaredVisionSurvivesIntoTheRegistry(t *testing.T) {
	yes := true
	d := FromConfig("seer", config.Duckling{
		Provider: "p", Model: "m",
		Caps: config.Caps{Vision: &yes},
	})
	if !d.Caps.Vision {
		t.Fatal("vision = true in config listed as false")
	}
}

type mandatoryReasoningProvider struct{}

func (mandatoryReasoningProvider) ID() string                               { return "openrouter" }
func (mandatoryReasoningProvider) Models(context.Context) ([]string, error) { return nil, nil }
func (mandatoryReasoningProvider) ChatStream(context.Context, provider.ChatRequest, chan<- provider.Delta) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, provider.ErrUnsupported
}
func (mandatoryReasoningProvider) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if reasoning, ok := req.Extra["reasoning"].(map[string]interface{}); ok && reasoning["enabled"] == false {
		return provider.ChatResponse{}, fmt.Errorf("HTTP 400: Reasoning is mandatory for this endpoint and cannot be disabled")
	}
	return provider.ChatResponse{
		Choices: []provider.Choice{{Message: provider.Message{Content: `{"ok":true}`}}},
	}, nil
}

func TestProbeRecordsMandatoryOpenRouterReasoning(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	r := NewRegistry()
	r.RegisterProvider(mandatoryReasoningProvider{})
	if err := r.Register(&Duckling{
		ID: "reasoner", Provider: "openrouter", Model: "m",
		OpenRouterProvider: "provider/fp4",
		Params:             config.SamplingParams{DisableThinking: true},
	}); err != nil {
		t.Fatal(err)
	}
	caps, err := r.ProbeForce(context.Background(), "reasoner")
	if err != nil {
		t.Fatal(err)
	}
	if caps.ThinkingControl != "mandatory" {
		t.Fatalf("ThinkingControl = %q, note %q", caps.ThinkingControl, caps.ThinkingControlNote)
	}
	listed := r.List()
	if len(listed) != 1 || listed[0].Caps.ThinkingControl != "mandatory" {
		t.Fatalf("fleet did not expose probed control: %#v", listed)
	}
}

// deadEndpoint answers every chat with an error, like a non-LLM service on
// the starter duckling's localhost:8080.
type deadEndpoint struct{}

func (deadEndpoint) ID() string                               { return "local" }
func (deadEndpoint) Models(context.Context) ([]string, error) { return nil, nil }
func (deadEndpoint) ChatStream(context.Context, provider.ChatRequest, chan<- provider.Delta) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, fmt.Errorf("404 Not Found: No context found for request")
}
func (deadEndpoint) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, fmt.Errorf("404 Not Found: No context found for request")
}

// B-464: an endpoint that answers no chat used to "probe" fine — every
// capability probe read the error as "unsupported", vision was inferred true,
// and the result was cached for a month.
func TestProbeFailsAndCachesNothingWhenTheEndpointAnswersNoChat(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	r := NewRegistry()
	r.RegisterProvider(deadEndpoint{})
	if err := r.Register(&Duckling{ID: "pato-local", Provider: "local", Model: "local-model"}); err != nil {
		t.Fatal(err)
	}
	caps, err := r.ProbeForce(context.Background(), "pato-local")
	if err == nil {
		t.Fatalf("a dead endpoint probed as %+v", caps)
	}
	if _, cached := r.CachedCaps("pato-local"); cached {
		t.Fatal("a failed probe was cached")
	}
	if !r.LastProbeFailed("pato-local") {
		t.Fatal("the failed probe was not retained for automatic seating")
	}
}

// scriptedEndpoint answers every chat with err (nil: a plain "ok").
type scriptedEndpoint struct {
	deadEndpoint
	err error
}

func (e scriptedEndpoint) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	if e.err != nil {
		return provider.ChatResponse{}, e.err
	}
	return provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Content: "ok"}}}}, nil
}

func (e scriptedEndpoint) ChatStream(ctx context.Context, req provider.ChatRequest, _ chan<- provider.Delta) (provider.ChatResponse, error) {
	return e.Chat(ctx, req)
}

// One health rule for the capability probe and for run traffic (review of
// #131): the whole matrix of prior state x result x path. An answer or a rate
// limit clears any mark (the endpoint answered); not found, refused and
// rejected credentials set one; anything else leaves the prior state.
func TestDucklingHealthIsOneRuleForProbesAndRunTraffic(t *testing.T) {
	results := []struct {
		name string
		err  error
		want string // "alive" | "dead" | "unchanged"
	}{
		{"answer", nil, "alive"},
		{"rate limit", fmt.Errorf("%w: chat stream: 429 Too Many Requests", provider.ErrRateLimit), "alive"},
		{"not found", fmt.Errorf("%w: chat stream: 404 Not Found", provider.ErrChatUnavailable), "dead"},
		{"auth", fmt.Errorf("%w: chat stream: 401", provider.ErrAuth), "dead"},
		{"unreachable", fmt.Errorf("%w: dial tcp: connection refused", provider.ErrProviderUnavailable), "dead"},
		{"bad request", fmt.Errorf("chat stream: 400 Bad Request"), "unchanged"},
	}
	// The probe's minimal request failing for an unclassified reason means
	// the duckling cannot chat at all (B-464); in run traffic it is about the
	// request and leaves the signal alone.
	probeWant := map[string]string{"bad request": "dead"}
	for _, path := range []string{"probe", "traffic"} {
		for _, prior := range []bool{false, true} {
			for _, res := range results {
				t.Run(fmt.Sprintf("%s/prior-dead=%v/%s", path, prior, res.name), func(t *testing.T) {
					t.Setenv("XDG_DATA_HOME", t.TempDir())
					r := NewRegistry()
					r.RegisterProvider(scriptedEndpoint{err: res.err})
					if err := r.Register(&Duckling{ID: "k3", Provider: "local", Model: "kimi"}); err != nil {
						t.Fatal(err)
					}
					if prior {
						r.recordProbeFailure("k3", fmt.Errorf("earlier 404"))
					}
					if path == "probe" {
						_, _ = r.ProbeForce(context.Background(), "k3")
					} else {
						r.RecordProviderResult("k3", res.err)
					}
					expected := res.want
					if w, ok := probeWant[res.name]; ok && path == "probe" {
						expected = w
					}
					want := map[string]bool{"alive": false, "dead": true, "unchanged": prior}[expected]
					if got := r.LastProbeFailed("k3"); got != want {
						t.Fatalf("LastProbeFailed = %v, want %v", got, want)
					}
				})
			}
		}
	}
}

// throttledEndpoint answers every chat with a rate limit.
type throttledEndpoint struct{ deadEndpoint }

func (throttledEndpoint) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, fmt.Errorf("%w: chat: 429 Too Many Requests", provider.ErrRateLimit)
}

// A rate-limited probe fails (nothing is cached) but is weather, not a dead
// endpoint: the duckling stays eligible for automatic seating, matching the
// rule RecordProviderResult applies to run traffic. Every other way of not
// answering is still retained.
func TestARateLimitedProbeDoesNotMarkTheDucklingDead(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	r := NewRegistry()
	r.RegisterProvider(throttledEndpoint{})
	if err := r.Register(&Duckling{ID: "k3", Provider: "local", Model: "kimi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ProbeForce(context.Background(), "k3"); err == nil {
		t.Fatal("a rate-limited probe succeeded")
	}
	if _, cached := r.CachedCaps("k3"); cached {
		t.Fatal("a rate-limited probe was cached")
	}
	if r.LastProbeFailed("k3") {
		t.Fatal("a rate limit excluded the duckling from automatic seating")
	}
	r.RecordProviderResult("k3", fmt.Errorf("%w: after 3 attempts", provider.ErrRateLimit))
	if r.LastProbeFailed("k3") {
		t.Fatal("rate-limited run traffic excluded the duckling")
	}
}
