package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
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
