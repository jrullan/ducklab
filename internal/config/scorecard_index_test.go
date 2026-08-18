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
	g, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal rejected declared external index: %v", err)
	}
	if g.Ducklings["pato"].Index == nil || g.Ducklings["pato"].Index.CodingScore != 87.5 || g.Ducklings["pato"].Index.Source != "Example Coding Index" || g.Ducklings["pato"].Index.AsOf != "2026-08-18" {
		t.Fatalf("index did not load with provenance: %+v", g.Ducklings["pato"].Index)
	}
	if err := SaveGlobal(path, g); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	idx := reloaded.Ducklings["pato"].Index
	if idx == nil || idx.CodingScore != 87.5 || idx.Source != "Example Coding Index" || idx.AsOf != "2026-08-18" {
		t.Fatalf("index did not round-trip with provenance: %+v", idx)
	}
}
