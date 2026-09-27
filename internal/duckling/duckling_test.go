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
