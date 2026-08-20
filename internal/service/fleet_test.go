package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
	"github.com/jrullan/ducklab/internal/provider"
)

func fleetService(t *testing.T) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{
		"local": {Kind: config.ProviderKindOpenAI, BaseURL: "http://127.0.0.1:8081/v1"},
	}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{
		"pato-local": {Provider: "local", Model: "qwen"},
	}
	if err := config.SaveGlobal(path, cfg); err != nil {
		t.Fatal(err)
	}
	s := &Service{
		cfg:        cfg,
		configPath: path,
		ducklings:  duckling.NewRegistry(),
		providers:  map[config.ProviderID]provider.Provider{},
	}
	return s, path
}

// I10: a key is never on disk in project state, never in the API. A provider
// records the *name* of an environment variable and nothing else, so this can
// report "the key is missing" without ever holding one.
func TestProviderListNeverCarriesAKey(t *testing.T) {
	s, _ := fleetService(t)
	t.Setenv("FLEET_TEST_KEY", "sk-do-not-leak-this")
	if err := s.ProviderSet("hosted", ProviderView{
		Kind: "openai", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "FLEET_TEST_KEY",
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(s.ProviderList())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-do-not-leak-this") {
		t.Fatalf("an API key reached the API payload:\n%s", raw)
	}
	if !strings.Contains(string(raw), "FLEET_TEST_KEY") {
		t.Error("the variable's name should be reported, so a person can fix a missing key")
	}

	// And the same for the file on disk.
	onDisk, err := os.ReadFile(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), "sk-do-not-leak-this") {
		t.Fatalf("an API key was written to config.toml:\n%s", onDisk)
	}
}

// A provider configured correctly with its key unset is the commonest reason a
// duckling fails, and it is invisible unless something says so.
func TestProviderListReportsWhetherTheKeyIsSet(t *testing.T) {
	s, _ := fleetService(t)
	if err := s.ProviderSet("hosted", ProviderView{
		BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "FLEET_UNSET_KEY",
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range s.ProviderList() {
		switch p.ID {
		case "hosted":
			if p.KeyPresent {
				t.Error("an unset variable was reported as present")
			}
		case "local":
			// Keyless: a local model needs no key, and reporting it as missing
			// one would be a permanent false alarm.
			if !p.KeyPresent {
				t.Error("a keyless provider was reported as missing a key")
			}
		}
	}
}

func TestProviderSetIsWrittenAndReadable(t *testing.T) {
	s, path := fleetService(t)
	if err := s.ProviderSet("hosted", ProviderView{
		BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY",
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Providers["hosted"]; got.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("provider not persisted: %+v", got)
	}
}

// A provider that vanishes under a duckling leaves something that lists fine
// and fails the moment it is used — worse than the state being fixed.
func TestProviderRemoveRefusesWhileInUse(t *testing.T) {
	s, _ := fleetService(t)
	err := s.ProviderRemove("local")
	if err == nil {
		t.Fatal("removed a provider a duckling still points at")
	}
	if !strings.Contains(err.Error(), "pato-local") {
		t.Errorf("the error does not name what is using it: %v", err)
	}

	if err := s.DucklingRemove("pato-local"); err != nil {
		t.Fatal(err)
	}
	if err := s.ProviderRemove("local"); err != nil {
		t.Errorf("removal refused after the last user went away: %v", err)
	}
}

// A duckling pointing at a provider that is not there is a typo, and the fix
// is the same as for any typo: say what does exist.
func TestDucklingSetNamesTheProvidersThatExist(t *testing.T) {
	s, _ := fleetService(t)
	err := s.DucklingSet("pato-new", DucklingView{Provider: "lcoal", Model: "qwen"})
	if err == nil {
		t.Fatal("accepted a duckling with an unknown provider")
	}
	if !strings.Contains(err.Error(), "local") {
		t.Errorf("the error does not list the real providers: %v", err)
	}
}

func TestDucklingSetAndRemoveReachTheRegistry(t *testing.T) {
	s, _ := fleetService(t)
	if err := s.ProviderSet("hosted", ProviderView{BaseURL: "https://openrouter.ai/api/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DucklingSet("pato-hosted", DucklingView{
		Provider: "hosted", Model: "anthropic/claude", Roles: []string{"reviewer"},
	}); err != nil {
		t.Fatal(err)
	}
	// Usable immediately: a duckling that needs an engine restart before it
	// can run is a duckling the person will assume is broken.
	if _, err := s.ducklings.Get("pato-hosted"); err != nil {
		t.Errorf("a new duckling did not reach the registry: %v", err)
	}

	if err := s.DucklingRemove("pato-hosted"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ducklings.Get("pato-hosted"); err == nil {
		t.Error("a removed duckling is still usable until a restart")
	}
}

func TestDucklingGetRoundTrips(t *testing.T) {
	s, _ := fleetService(t)
	if err := s.DucklingSet("pato-local", DucklingView{
		Provider: "local", Model: "qwen3.6", Roles: []string{"implementer", "reviewer"},
		Notes: "the local one", Cost: config.Cost{InputPerMTok: 0, OutputPerMTok: 0},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.DucklingGet(context.Background(), "pato-local")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "qwen3.6" || len(got.Roles) != 2 || got.Notes != "the local one" {
		t.Errorf("round trip lost data: %+v", got)
	}
}

// A rejected write must leave memory and disk saying the same thing. A config
// the engine believes and the file contradicts is the worst outcome available.
func TestAFailedWriteLeavesNothingBehind(t *testing.T) {
	s, path := fleetService(t)
	// Make the file unwritable by pointing at a directory.
	s.configPath = filepath.Dir(path)

	if err := s.DucklingSet("pato-ghost", DucklingView{Provider: "local", Model: "x"}); err == nil {
		t.Fatal("a write that could not happen was reported as success")
	}
	if _, ok := s.cfg.Ducklings["pato-ghost"]; ok {
		t.Error("the duckling stayed in memory after the write failed")
	}
	if _, err := s.ducklings.Get("pato-ghost"); err == nil {
		t.Error("the duckling reached the registry after the write failed")
	}
}

// The write gate speaks the loader's rule, exactly. This test used to bless
// "pato_local-2" as reasonable — the write path's private, looser idea of an
// id — and that drift let the desktop save qwen38_27b, which the next engine
// start refused to load: writer-made lockout (B-104). Underscores are not
// refused for taste; they are refused because the loader refuses them.
func TestIDsAreValidated(t *testing.T) {
	s, _ := fleetService(t)
	for _, bad := range []string{"", "Pato Local", "pato/local", "PATO", "pato_local-2", "qwen38_27b"} {
		if err := s.DucklingSet(bad, DucklingView{Provider: "local", Model: "x"}); err == nil {
			t.Errorf("id %q was accepted; the loader would refuse it at the next start", bad)
		}
	}
	if err := s.DucklingSet("pato-local-2", DucklingView{Provider: "local", Model: "x"}); err != nil {
		t.Errorf("a loader-valid id was refused: %v", err)
	}
}

// An engine started without a config file must say so rather than accept
// changes that go nowhere.
func TestChangesWithoutAConfigFileAreRefused(t *testing.T) {
	s := &Service{cfg: config.DefaultGlobal(), ducklings: duckling.NewRegistry()}
	err := s.DucklingSet("pato", DucklingView{Provider: "local", Model: "x"})
	if err == nil || !strings.Contains(err.Error(), "config file") {
		t.Errorf("err = %v", err)
	}
}
