package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// OpenRouter reports the actually-billed cost in usage.cost — caching
// discounts included — and for a year it never landed: we parsed only
// "cost_usd", a name OpenRouter does not use, so every recorded cost came
// from the configured flat rates. The provider-reported number must win.
func TestTheBilledCostLandsAndWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1000000,"completion_tokens":1000,"total_tokens":1001000,"cost":1.2345}}`)
	}))
	defer srv.Close()

	resp, err := NewOpenAICompat("t", srv.URL, "").Chat(context.Background(),
		ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "go"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.CostUSD != 1.2345 {
		t.Errorf("CostUSD = %v, want the billed 1.2345", resp.Usage.CostUSD)
	}
	// And the calculator lets the provider's number win over the flat rates.
	calc := CostCalculator{InputPerMTok: 3, OutputPerMTok: 15}
	if got := calc.Cost(resp.Usage); got != 1.2345 {
		t.Errorf("calculator = %v — the flat rates overrode the invoice", got)
	}
	if calc.CostSource(resp.Usage) != "provider" {
		t.Error("the cost source does not credit the provider")
	}
}

// The accounting must be asked for, and only OpenRouter understands the
// asking — OpenAI proper rejects unknown top-level params.
func TestUsageAccountingIsRequestedFromOpenRouterOnly(t *testing.T) {
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"openrouter", true},
		{"plain", false},
	} {
		var gotBody map[string]json.RawMessage
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &gotBody)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
		}))
		defer srv.Close()

		p := NewOpenAICompat("t", srv.URL, "")
		if tc.host == "openrouter" {
			// The heuristic keys on the base URL.
			p.baseURL = srv.URL + "/openrouter"
			// A path suffix breaks the endpoint; point it back while keeping
			// the marker for the heuristic. Simpler: exercise the heuristic
			// directly instead of the wire.
			p.baseURL = srv.URL
			req := ChatRequest{}
			pp := &OpenAICompat{baseURL: "https://openrouter.ai/api/v1"}
			pp.askForBilledCost(&req)
			if req.UsageDetail == nil || !req.UsageDetail.Include {
				t.Fatal("openrouter was not asked for usage accounting")
			}
			continue
		}
		if _, err := p.Chat(context.Background(),
			ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "go"}}}); err != nil {
			t.Fatal(err)
		}
		if _, present := gotBody["usage"]; present {
			t.Error("a plain OpenAI-compatible server was sent the usage param it may reject")
		}
	}
}
