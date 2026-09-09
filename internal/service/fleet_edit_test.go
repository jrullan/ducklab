package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

// A service that can write its config back, which is what editing a duckling
// needs. serviceWithDucklings deliberately has no config path.
func writableService(t *testing.T, ids ...string) *Service {
	t.Helper()
	isolate(t)
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{
		"fake": {Kind: config.ProviderKindOpenAI, BaseURL: "fake://"},
	}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{}
	for _, id := range ids {
		cfg.Ducklings[config.DucklingID(id)] = config.Duckling{Provider: "fake", Model: "m-" + id}
	}
	s, err := New(cfg, Options{
		Bus:        bus.New(16),
		ConfigPath: filepath.Join(t.TempDir(), "config.toml"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Editing a duckling always failed.
//
// DucklingSet routed the update through Registry.Register, which refuses an id
// that already exists — so every save of an existing duckling came back
// `register "x": duckling "x" already registered`. And it failed AFTER
// saveConfig, so the new values were on disk, the old ones were still running,
// and the screen said the save had failed. Three states, all different.
func TestEditingAnExistingDucklingSucceeds(t *testing.T) {
	s := writableService(t, "pato-uno")

	view := DucklingView{
		Provider: "fake", Model: "deepseek/deepseek-v4-pro",
		Cost: config.Cost{OutputPerMTok: 0.85},
	}
	if err := s.DucklingSet("pato-uno", view); err != nil {
		t.Fatalf("editing an existing duckling failed: %v", err)
	}

	// The running engine must agree with the file it just wrote.
	d, err := s.ducklings.Get("pato-uno")
	if err != nil {
		t.Fatalf("the duckling vanished from the registry: %v", err)
	}
	if d.Model != "deepseek/deepseek-v4-pro" {
		t.Errorf("the registry still runs the old model: %q", d.Model)
	}
	if got := s.cfg.Ducklings["pato-uno"].Model; got != "deepseek/deepseek-v4-pro" {
		t.Errorf("config model = %q", got)
	}
}

// The sampling params the desktop form now sends must survive the round trip,
// or the form would appear to work and change nothing.
func TestEditingKeepsTheSamplingParams(t *testing.T) {
	s := writableService(t, "pato-uno")

	cap := 32000
	view := DucklingView{
		Provider: "fake", Model: "m",
		Params: config.SamplingParams{MaxTokens: &cap, DisableThinking: true},
	}
	if err := s.DucklingSet("pato-uno", view); err != nil {
		t.Fatal(err)
	}
	d, err := s.ducklings.Get("pato-uno")
	if err != nil {
		t.Fatal(err)
	}
	if d.Params.MaxTokens == nil || *d.Params.MaxTokens != 32000 {
		t.Errorf("max_tokens did not reach the registry: %+v", d.Params)
	}
	if !d.Params.DisableThinking {
		t.Error("disable_thinking did not reach the registry")
	}
}

// A one-field CLI edit must not erase fields that CLI did not repeat. This
// was impossible to use safely: allowing thinking zeroed max_tokens, vision,
// context, colour, cost, notes, and every other omitted setting.
func TestDucklingUpdateMergesOmittedFieldsAndExplicitZeroValues(t *testing.T) {
	s := writableService(t, "pato-uno")
	maxTokens := 20000
	contextTokens := 256000
	nativeTools := true
	vision := true
	if err := s.DucklingSet("pato-uno", DucklingView{
		Provider: "fake", Model: "qwen38-27b", Notes: "keep me", Color: 6,
		Params: config.SamplingParams{MaxTokens: &maxTokens, DisableThinking: true},
		Caps:   config.Caps{NativeTools: &nativeTools, ContextTokens: &contextTokens, Vision: &vision},
		Cost:   config.Cost{InputPerMTok: 1.25, OutputPerMTok: 2.5},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.DucklingUpdate("pato-uno", map[string]interface{}{
		"params": map[string]interface{}{"disable_thinking": false},
		"color":  0,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.DucklingGet(context.Background(), "pato-uno")
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "fake" || got.Model != "qwen38-27b" || got.Notes != "keep me" {
		t.Fatalf("sparse edit erased identity or notes: %+v", got)
	}
	if got.Params.DisableThinking || got.Color != 0 {
		t.Fatalf("explicit zero values were not applied: params=%+v color=%d", got.Params, got.Color)
	}
	if got.Params.MaxTokens == nil || *got.Params.MaxTokens != 20000 {
		t.Fatalf("omitted max_tokens was erased: %+v", got.Params)
	}
	if got.Caps.Vision == nil || !*got.Caps.Vision || got.Caps.ContextTokens == nil || *got.Caps.ContextTokens != 256000 {
		t.Fatalf("omitted capabilities were erased: %+v", got.Caps)
	}
	if got.Cost.InputPerMTok != 1.25 || got.Cost.OutputPerMTok != 2.5 {
		t.Fatalf("omitted cost was erased: %+v", got.Cost)
	}

	// Null is different from omission: the desktop uses it to return an
	// optional sampling value to the provider default.
	if err := s.DucklingUpdate("pato-uno", map[string]interface{}{
		"params": map[string]interface{}{"max_tokens": nil},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.DucklingGet(context.Background(), "pato-uno")
	if got.Params.MaxTokens != nil {
		t.Fatalf("explicit null did not clear max_tokens: %+v", got.Params)
	}
	if got.Caps.Vision == nil || !*got.Caps.Vision {
		t.Fatalf("clearing max_tokens erased an unrelated capability: %+v", got.Caps)
	}
}

func TestEditingKeepsTheDeclaredModelTier(t *testing.T) {
	s := writableService(t, "pato-uno")
	if err := s.DucklingSet("pato-uno", DucklingView{Provider: "fake", Model: "m", Tier: "large"}); err != nil {
		t.Fatal(err)
	}
	d, err := s.ducklings.Get("pato-uno")
	if err != nil {
		t.Fatal(err)
	}
	if d.Tier != config.ModelTierLarge {
		t.Fatalf("registry tier = %q, want large", d.Tier)
	}
	view, err := s.DucklingGet(context.Background(), "pato-uno")
	if err != nil || view.Tier != "large" {
		t.Fatalf("editable tier = %q, err = %v", view.Tier, err)
	}
}

// A duckling naming a provider that is not there must still be refused, and
// refused before anything is written.
func TestAnUnknownProviderIsStillRejected(t *testing.T) {
	s := writableService(t, "pato-uno")
	err := s.DucklingSet("pato-dos", DucklingView{Provider: "nope", Model: "m"})
	if err == nil {
		t.Fatal("a duckling on a nonexistent provider was accepted")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("the error does not name the missing provider: %v", err)
	}
	if _, err := s.ducklings.Get("pato-dos"); err == nil {
		t.Error("the rejected duckling was registered anyway")
	}
}

// A duckling saved without a context size or costs used to run on a 32k
// default and report $0: a 200k model starved and a paid one looked free.
// What the person leaves blank, the provider's own listing may declare.
func TestSavingADucklingFillsBlanksFromTheProvider(t *testing.T) {
	s := writableService(t, "pato-uno")
	fake, _ := s.providers["fake"].(*provider.Fake)
	fake.ModelInfoFn = func(model string) *provider.ModelInfo {
		return &provider.ModelInfo{ContextTokens: 200000, MaxOutputTokens: 64000, PromptPerMTok: 3, CompletionPerMTok: 15}
	}

	if err := s.DucklingSet("pato-nube", DucklingView{Provider: "fake", Model: "big-model"}); err != nil {
		t.Fatal(err)
	}
	d := s.cfg.Ducklings["pato-nube"]
	if d.Caps.ContextTokens == nil || *d.Caps.ContextTokens != 200000 {
		t.Errorf("context not filled: %v", d.Caps.ContextTokens)
	}
	if d.Cost.InputPerMTok != 3 || d.Cost.OutputPerMTok != 15 {
		t.Errorf("costs not filled: %+v", d.Cost)
	}
	if d.Params.MaxTokens == nil || *d.Params.MaxTokens != 64000 {
		t.Errorf("reply ceiling not filled: %v", d.Params.MaxTokens)
	}

	// What the person DID write is never overwritten.
	ctx := 64000
	if err := s.DucklingSet("pato-fijo", DucklingView{
		Provider: "fake", Model: "big-model",
		Caps: config.Caps{ContextTokens: &ctx},
		Cost: config.Cost{InputPerMTok: 1, OutputPerMTok: 2},
	}); err != nil {
		t.Fatal(err)
	}
	d = s.cfg.Ducklings["pato-fijo"]
	if *d.Caps.ContextTokens != 64000 || d.Cost.InputPerMTok != 1 {
		t.Errorf("explicit values were overwritten: ctx=%v cost=%+v", *d.Caps.ContextTokens, d.Cost)
	}
}

// A write gate looser than the load gate lets the writer lock the engine out
// of its own config: the desktop saved qwen38_27b, and the next start died on
// "invalid id" reading it back (B-104). One identity, one validator.
func TestDucklingSetRefusesIdsTheLoaderWould(t *testing.T) {
	s := writableService(t, "pato-uno")
	err := s.DucklingSet("qwen38_27b", DucklingView{Provider: "fake", Model: "qwen/qwen3.8-27b"})
	if err == nil || !strings.Contains(err.Error(), "invalid id") {
		t.Fatalf("an underscore id was accepted for writing: %v", err)
	}
}
