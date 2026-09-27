package agent

import (
	"reflect"
	"testing"

	"github.com/jrullan/ducklab/internal/provider"
)

func TestRequestMapRecordsEveryWireControl(t *testing.T) {
	temperature, topP, maxTokens := 0.2, 0.8, 123
	noFallback := false
	req := provider.ChatRequest{
		Model:         "m",
		Messages:      []provider.Message{{Role: "user", Content: "hello"}},
		Tools:         []provider.Tool{{Type: "function"}},
		ToolChoice:    "auto",
		Temperature:   &temperature,
		TopP:          &topP,
		MaxTokens:     &maxTokens,
		Stop:          []string{"STOP"},
		Stream:        true,
		StreamOptions: &provider.StreamOptions{IncludeUsage: true},
		JSONMode:      true,
		Extra:         map[string]interface{}{"reasoning": map[string]interface{}{"enabled": false}},
		UsageDetail:   &provider.UsageDetail{Include: true},
		Provider:      &provider.ProviderPreferences{Only: []string{"p/fp4"}, AllowFallbacks: &noFallback},
	}

	got := requestMap(req)
	for _, key := range []string{"model", "messages", "tools", "tool_choice", "temperature", "top_p", "max_tokens", "stop", "stream", "stream_options", "json_mode", "reasoning", "usage", "provider"} {
		if _, ok := got[key]; !ok {
			t.Errorf("request record omitted %q: %#v", key, got)
		}
	}
	if !reflect.DeepEqual(got["reasoning"], req.Extra["reasoning"]) {
		t.Errorf("reasoning = %#v, want %#v", got["reasoning"], req.Extra["reasoning"])
	}
}
