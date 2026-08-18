package config

import (
	"os"
	"path/filepath"
	"testing"
)

// An external index is a declaration made by the configuration author, not a
// ranking Ducklab fetched or inferred. Strict parsing must accept its complete
// provenance table.
func TestLoadGlobalAcceptsDeclaredDucklingExternalIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := `schema = 1

[provider.remote]
kind = "openai"
base_url = "https://api.example.com/v1"

[duckling.pato]
provider = "remote"
model = "pato-coder"

[duckling.pato.index]
coding_score = 87.5
source = "Example Coding Index"
as_of = "2026-08-18"
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGlobal(path); err != nil {
		t.Fatalf("LoadGlobal rejected declared external index: %v", err)
	}
}
