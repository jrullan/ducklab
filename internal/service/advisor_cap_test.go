package service

import (
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
)

// The one-shot cap (advisor, reference digestion) keeps its small floor only
// when the seat verifiably runs without reasoning; otherwise reasoning shares
// the cap with the answer and the seat gets its configured room, at least
// unsuppressedFloor. B-479: qwen3.8-max on Alibaba has mandatory reasoning,
// was capped at 2000 because "suppress thinking" was ticked, spent all 2000
// thinking, and the advisor answered nothing. The whole matrix: provider kind
// x probed thinking control x suppression x configured max_tokens.
func TestOneShotCapFollowsWhetherThinkingIsReallySuppressed(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	s.cfg.Providers["openrouter"] = config.Provider{Kind: config.ProviderKindOpenAI, BaseURL: "https://openrouter.ai/api/v1"}
	s.cfg.Providers["local"] = config.Provider{Kind: config.ProviderKindOpenAI, BaseURL: "http://localhost:8080/v1"}
	big, small := 131072, 500
	seat := func(prov string, suppress bool, max *int) *duckling.Duckling {
		return &duckling.Duckling{Provider: config.ProviderID(prov), Params: config.SamplingParams{DisableThinking: suppress, MaxTokens: max}}
	}
	control := func(c string) *duckling.Capabilities { return &duckling.Capabilities{ThinkingControl: c} }
	cases := []struct {
		name string
		d    *duckling.Duckling
		caps *duckling.Capabilities
		want int
	}{
		// Verified suppression: the floor.
		{"openrouter, disabled verified", seat("openrouter", true, &big), control("disabled"), 2000},
		{"local template server, suppressed", seat("local", true, &big), control(""), 2000},
		// Reasoning cannot be assumed off: the configured room.
		{"openrouter, mandatory (B-479)", seat("openrouter", true, &big), control("mandatory"), 131072},
		{"openrouter, control never probed", seat("openrouter", true, &big), control(""), 131072},
		{"openrouter, no caps at all", seat("openrouter", true, &big), nil, 131072},
		{"local, mandatory", seat("local", true, &big), control("mandatory"), 131072},
		{"thinking seat, not suppressed", seat("openrouter", false, &big), control("disabled"), 131072},
		// Small or missing configuration still leaves room to think and answer.
		{"mandatory, small max_tokens", seat("openrouter", true, &small), control("mandatory"), unsuppressedFloor},
		{"not suppressed, unconfigured", seat("openrouter", false, nil), control(""), unsuppressedFloor},
		{"no seat", nil, nil, 2000},
	}
	for _, c := range cases {
		got := 0
		if c.d == nil {
			got = 2000 // the caller's floor; a nil seat is never called
		} else {
			got = s.oneShotCap(c.d, c.caps, 2000)
		}
		if got != c.want {
			t.Errorf("%s: oneShotCap = %d, want %d", c.name, got, c.want)
		}
	}
}
