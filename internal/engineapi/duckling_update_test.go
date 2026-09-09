package engineapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/service"
)

// PUT historically decoded a sparse body into a complete DucklingView and
// replaced the record, so the CLI's one-field edits erased every omitted
// setting. The route now preserves omission while retaining explicit false.
func TestDucklingPutMergesSparseNestedFields(t *testing.T) {
	maxTokens := 20000
	contextTokens := 256000
	vision := true
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{
		"fake": {Kind: config.ProviderKindOpenAI, BaseURL: "fake://"},
	}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{
		"pato": {
			Provider: "fake", Model: "qwen38-27b", Color: 6,
			Params: config.SamplingParams{MaxTokens: &maxTokens, DisableThinking: true},
			Caps:   config.Caps{ContextTokens: &contextTokens, Vision: &vision},
		},
	}
	svc, err := service.New(cfg, service.Options{
		Bus: bus.New(16), ConfigPath: filepath.Join(t.TempDir(), "config.toml"),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(svc, bus.New(16), "token", "test", ""))
	t.Cleanup(server.Close)

	req, err := http.NewRequest(http.MethodPut, server.URL+"/v1/ducklings/pato",
		bytes.NewBufferString(`{"params":{"disable_thinking":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sparse PUT status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	got, err := svc.DucklingGet(context.Background(), "pato")
	if err != nil {
		t.Fatal(err)
	}
	if got.Params.DisableThinking {
		t.Error("explicit false was not applied")
	}
	if got.Model != "qwen38-27b" || got.Color != 6 || got.Params.MaxTokens == nil || *got.Params.MaxTokens != 20000 {
		t.Fatalf("sparse PUT erased omitted fields: %+v", got)
	}
	if got.Caps.Vision == nil || !*got.Caps.Vision || got.Caps.ContextTokens == nil || *got.Caps.ContextTokens != 256000 {
		t.Fatalf("sparse PUT erased omitted capabilities: %+v", got.Caps)
	}
}
