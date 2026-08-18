package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

// A legacy positional line-up has an unambiguous, once-only translation. The
// migrated file is the durable configuration: loading it again must not retain
// the positional key or rewrite a different representation.
func TestEnsureGlobalMigratesLegacyModeDucklingsToCanonicalModeSeatsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	legacy := `schema = 1

[defaults]
mode_ducklings = { pair = ["global-implementer", "global-reviewer"] }

[provider.fake]
kind = "openai"
base_url = "http://example.test/v1"

[duckling.global-implementer]
provider = "fake"
model = "implementer"

[duckling.global-reviewer]
provider = "fake"
model = "reviewer"
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, created, err := EnsureGlobal(path); err != nil {
		t.Fatal(err)
	} else if created {
		t.Fatal("existing legacy configuration was treated as a new configuration")
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var persisted struct {
		Defaults struct {
			ModeSeats     map[string]map[string][]string `toml:"mode_seats"`
			ModeDucklings map[string][]string            `toml:"mode_ducklings"`
		} `toml:"defaults"`
	}
	if _, err := toml.Decode(string(first), &persisted); err != nil {
		t.Fatalf("decode migrated TOML: %v", err)
	}
	if got := persisted.Defaults.ModeSeats["pair"]["implementer"]; !bytes.Equal([]byte(joinStrings(got)), []byte("global-implementer")) {
		t.Errorf("pair implementer seats = %v, want [global-implementer]", got)
	}
	if got := persisted.Defaults.ModeSeats["pair"]["reviewer"]; !bytes.Equal([]byte(joinStrings(got)), []byte("global-reviewer")) {
		t.Errorf("pair reviewer seats = %v, want [global-reviewer]", got)
	}
	if len(persisted.Defaults.ModeDucklings) != 0 {
		t.Errorf("migrated file retained positional mode_ducklings: %v", persisted.Defaults.ModeDucklings)
	}

	if _, _, err := EnsureGlobal(path); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("a second load rewrote an already migrated configuration")
	}
}

func joinStrings(v []string) string {
	out := ""
	for _, s := range v {
		out += s
	}
	return out
}
