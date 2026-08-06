package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// OpenRouter's listing declares context_length and USD-per-token pricing as
// strings. The parse turns them into the units ducklab's cost model uses.
func TestModelInfoReadsOpenRouterShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
			{"id":"other/model","context_length":8192,"pricing":{"prompt":"0.000001","completion":"0.000002"}},
			{"id":"anthropic/claude-sonnet-4.5","context_length":200000,"top_provider":{"max_completion_tokens":64000},"pricing":{"prompt":"0.000003","completion":"0.000015"}}
		]}`))
	}))
	defer srv.Close()

	p := NewOpenAICompat("test", srv.URL, "")
	info, err := p.ModelInfo(context.Background(), "anthropic/claude-sonnet-4.5")
	if err != nil {
		t.Fatal(err)
	}
	if info.ContextTokens != 200000 {
		t.Errorf("context = %d, want 200000", info.ContextTokens)
	}
	if info.PromptPerMTok != 3.0 || info.CompletionPerMTok != 15.0 {
		t.Errorf("pricing = %v/%v per MTok, want 3/15", info.PromptPerMTok, info.CompletionPerMTok)
	}
	if info.MaxOutputTokens != 64000 {
		t.Errorf("reply ceiling = %d, want 64000", info.MaxOutputTokens)
	}
}

// A plain OpenAI-compatible server lists names only; absence of the fields is
// zeros, not an error — and an unknown model is named.
func TestModelInfoOnAPlainServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"qwen3"}]}`))
	}))
	defer srv.Close()
	p := NewOpenAICompat("test", srv.URL, "")
	info, err := p.ModelInfo(context.Background(), "qwen3")
	if err != nil {
		t.Fatal(err)
	}
	if info.ContextTokens != 0 || info.PromptPerMTok != 0 {
		t.Errorf("bare listing invented values: %+v", info)
	}
	if _, err := p.ModelInfo(context.Background(), "missing"); err == nil {
		t.Error("an unlisted model did not error")
	}
}
