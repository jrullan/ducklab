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
// x probed thinking control x suppression x configured max_tokens, and the
// context window, which bounds every result (review of #134).
func TestOneShotCapFollowsWhetherThinkingIsReallySuppressed(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	s.cfg.Providers["openrouter"] = config.Provider{Kind: config.ProviderKindOpenAI, BaseURL: "https://openrouter.ai/api/v1"}
	s.cfg.Providers["local"] = config.Provider{Kind: config.ProviderKindOpenAI, BaseURL: "http://localhost:8080/v1"}
	s.cfg.Providers["lan"] = config.Provider{Kind: config.ProviderKindOpenAI, BaseURL: "http://10.0.0.5:8000/v1"}
	s.cfg.Providers["remote"] = config.Provider{Kind: config.ProviderKindOpenAI, BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"}
	s.cfg.Providers["anthropic"] = config.Provider{Kind: config.ProviderKindAnthropic, BaseURL: "https://api.anthropic.com"}
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
		{"LAN template server (aitopatom), suppressed", seat("lan", true, &big), control(""), 2000},
		// Review of #134: a remote endpoint that is not OpenRouter is not a
		// local template server; unknown control there is unverified.
		{"remote OpenAI-compatible, control unknown", seat("remote", true, &big), control(""), 131072},
		{"anthropic, control unknown", seat("anthropic", true, &big), control(""), 131072},
		// Reasoning cannot be assumed off: the configured room.
		{"openrouter, mandatory (B-479)", seat("openrouter", true, &big), control("mandatory"), 131072},
		{"openrouter, control never probed", seat("openrouter", true, &big), control(""), 131072},
		{"openrouter, no caps at all", seat("openrouter", true, &big), nil, 131072},
		{"local, mandatory", seat("local", true, &big), control("mandatory"), 131072},
		{"thinking seat, not suppressed", seat("openrouter", false, &big), control("disabled"), 131072},
		// The configured max_tokens is the seat's declared output ceiling:
		// never raised past it, except to the caller's floor as before.
		{"mandatory, small max_tokens", seat("openrouter", true, &small), control("mandatory"), 2000},
		// Nothing configured: room to think and answer.
		{"not suppressed, unconfigured", seat("openrouter", false, nil), control(""), unsuppressedFloor},
		{"no seat", nil, nil, 2000},
	}
	for _, c := range cases {
		got := 0
		if c.d == nil {
			got = 2000 // the caller's floor; a nil seat is never called
		} else {
			got = s.oneShotCap(c.d, c.caps, 2000, 0)
		}
		if got != c.want {
			t.Errorf("%s: oneShotCap = %d, want %d", c.name, got, c.want)
		}
	}

	// The context window. Prompt and output share it, and an endpoint
	// rejects a max_tokens that does not fit beside the prompt: a 16K floor
	// on an 8K seat failed every advisor and digest call.
	ctxCaps := func(c string, window int) *duckling.Capabilities {
		return &duckling.Capabilities{ThinkingControl: c, ContextTokens: window}
	}
	room := func(window, prompt int) int { return window - prompt - prompt/10 - oneShotContextMargin }
	fits := []struct {
		name   string
		d      *duckling.Duckling
		caps   *duckling.Capabilities
		prompt int
		want   int
	}{
		{"8K, mandatory, unconfigured", seat("openrouter", true, nil), ctxCaps("mandatory", 8192), 3000, room(8192, 3000)},
		{"8K, not suppressed, unconfigured", seat("openrouter", false, nil), ctxCaps("", 8192), 3000, room(8192, 3000)},
		{"8K, mandatory, configured past the window", seat("openrouter", true, &big), ctxCaps("mandatory", 8192), 1000, room(8192, 1000)},
		{"4K, verified suppression, floor does not fit", seat("openrouter", true, &big), ctxCaps("disabled", 4096), 3000, room(4096, 3000)},
		{"8K, verified suppression, floor fits", seat("openrouter", true, &big), ctxCaps("disabled", 8192), 3000, 2000},
		{"32K, unconfigured, floor fits", seat("openrouter", false, nil), ctxCaps("", 32768), 3000, unsuppressedFloor},
		{"256K, configured, fits", seat("openrouter", true, &big), ctxCaps("mandatory", 262144), 3000, big},
		{"prompt fills the window", seat("openrouter", true, nil), ctxCaps("mandatory", 8192), 8000, minOneShotOutput},
		{"window unknown (0)", seat("openrouter", true, nil), ctxCaps("mandatory", 0), 3000, unsuppressedFloor},
	}
	for _, c := range fits {
		got := s.oneShotCap(c.d, c.caps, 2000, c.prompt)
		if got != c.want {
			t.Errorf("%s: oneShotCap = %d, want %d", c.name, got, c.want)
		}
		if c.caps.ContextTokens > 0 && c.want != minOneShotOutput && got+c.prompt > c.caps.ContextTokens {
			t.Errorf("%s: %d output + %d prompt exceeds the %d window", c.name, got, c.prompt, c.caps.ContextTokens)
		}
	}
}
