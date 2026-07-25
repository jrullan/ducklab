package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateID(t *testing.T) {
	valid := []string{"a", "abc", "a-b", "abc-def", "a1", "1a", "a" + strings.Repeat("b", 31)}
	for _, id := range valid {
		if err := ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q) should be valid: %v", id, err)
		}
	}
	invalid := []string{"", "A", "-a", "a-", "a_b", "a b", "a" + strings.Repeat("b", 32), "-", "a-"}
	for _, id := range invalid {
		if err := ValidateID(id); err == nil {
			t.Errorf("ValidateID(%q) should be invalid", id)
		}
	}
}

func TestValidateRole(t *testing.T) {
	for _, r := range ValidRoles() {
		if err := ValidateRole(r); err != nil {
			t.Errorf("ValidateRole(%q) should be valid: %v", r, err)
		}
	}
	if err := ValidateRole("invalid"); err == nil {
		t.Error("ValidateRole(\"invalid\") should be invalid")
	}
}

func TestValidateAutonomy(t *testing.T) {
	for _, a := range ValidAutonomies() {
		if err := ValidateAutonomy(a); err != nil {
			t.Errorf("ValidateAutonomy(%q) should be valid: %v", a, err)
		}
	}
	if err := ValidateAutonomy("invalid"); err == nil {
		t.Error("ValidateAutonomy(\"invalid\") should be invalid")
	}
}

func TestValidateURL(t *testing.T) {
	valid := []string{"http://localhost:8081", "https://api.example.com/v1", "http://127.0.0.1:1"}
	for _, u := range valid {
		if err := ValidateURL(u); err != nil {
			t.Errorf("ValidateURL(%q) should be valid: %v", u, err)
		}
	}
	invalid := []string{"", "not-a-url", "//relative/path"}
	for _, u := range invalid {
		if err := ValidateURL(u); err == nil {
			t.Errorf("ValidateURL(%q) should be invalid", u)
		}
	}
}

func TestDefaultGlobal(t *testing.T) {
	g := DefaultGlobal()
	if g.Schema != 1 {
		t.Errorf("Schema = %d, want 1", g.Schema)
	}
	if g.Defaults.Autonomy != AutonomyGuarded {
		t.Errorf("Defaults.Autonomy = %q, want %q", g.Defaults.Autonomy, AutonomyGuarded)
	}
	if g.Defaults.Budget.MaxUSD != 2.00 {
		t.Errorf("Defaults.Budget.MaxUSD = %f, want 2.00", g.Defaults.Budget.MaxUSD)
	}
	if g.Engine.MaxConcurrentRuns != 2 {
		t.Errorf("Engine.MaxConcurrentRuns = %d, want 2", g.Engine.MaxConcurrentRuns)
	}
}

func TestDefaultProject(t *testing.T) {
	p := DefaultProject("test", "Test Project")
	if p.Schema != 1 {
		t.Errorf("Schema = %d, want 1", p.Schema)
	}
	if p.ID != "test" {
		t.Errorf("ID = %q, want %q", p.ID, "test")
	}
	if p.Autonomy != AutonomyGuarded {
		t.Errorf("Autonomy = %q, want %q", p.Autonomy, AutonomyGuarded)
	}
	if p.Verify.Mode != "auto" {
		t.Errorf("Verify.Mode = %q, want %q", p.Verify.Mode, "auto")
	}
	if p.Shell.Mode != "guarded" {
		t.Errorf("Shell.Mode = %q, want %q", p.Shell.Mode, "guarded")
	}
}

func TestLoadGlobal(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	data := `schema = 1

[defaults]
autonomy = "guarded"
mode = "solo"
repair_attempts = 2
tool_result_max_bytes = 32768
agent_max_turns = 24
http_timeout_s = 300
transient_retries = 3

[defaults.budget]
max_usd = 2.00
max_tokens = 400000
max_wallclock_s = 1800
max_turns = 24

[engine]
autostart = true
port = 0
path = ""
max_concurrent_runs = 2
shutdown_grace_s = 30
project_memory_max_bytes = 8192

[provider.local]
kind = "openai"
base_url = "http://localhost:8081/v1"

[duckling.pato]
provider = "local"
model = "test-model"
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if g.Schema != 1 {
		t.Errorf("Schema = %d, want 1", g.Schema)
	}
	if g.Providers["local"].BaseURL != "http://localhost:8081/v1" {
		t.Errorf("Providers[local].BaseURL = %q", g.Providers["local"].BaseURL)
	}
	if g.Ducklings["pato"].Model != "test-model" {
		t.Errorf("Ducklings[pato].Model = %q", g.Ducklings["pato"].Model)
	}
}

func TestLoadGlobalStrict(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	// Unknown key should be rejected
	data := `schema = 1
unknown_key = "x"
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadGlobal(path)
	if err == nil {
		t.Error("LoadGlobal should reject unknown key")
	}
}

func TestLoadGlobalBadSchema(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	data := `schema = 2
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadGlobal(path)
	if err == nil {
		t.Error("LoadGlobal should reject schema != 1")
	}
}

func TestLoadGlobalBadDucklingProvider(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	data := `schema = 1

[duckling.pato]
provider = "nonexistent"
model = "test"
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadGlobal(path)
	if err == nil {
		t.Error("LoadGlobal should reject duckling with undefined provider")
	}
}

func TestLoadProject(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "project.toml")
	data := `schema = 1
id = "test"
name = "Test Project"
created = "2026-07-25T15:30:12Z"
autonomy = "guarded"

[verify]
mode = "auto"
tests = "go test ./..."
build = "go build ./..."
timeout_s = 900

[roster]
implementer = "pato-local"
reviewer = "pato-nube"

[modes]
build = "pair"
intake = "council"

[git]
branch_prefix = "ducklab/"
base_branch = "main"
commit_trailer = true
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "test" {
		t.Errorf("ID = %q, want %q", p.ID, "test")
	}
	if p.Roster["implementer"] != "pato-local" {
		t.Errorf("Roster[implementer] = %q", p.Roster["implementer"])
	}
	if p.Modes["build"] != "pair" {
		t.Errorf("Modes[build] = %q", p.Modes["build"])
	}
}

func TestProviderAPIKey(t *testing.T) {
	p := &Provider{APIKeyEnv: "TEST_API_KEY"}
	os.Setenv("TEST_API_KEY", "secret123")
	defer os.Unsetenv("TEST_API_KEY")
	key, err := p.APIKey()
	if err != nil {
		t.Fatal(err)
	}
	if key != "secret123" {
		t.Errorf("APIKey() = %q, want %q", key, "secret123")
	}
	if !p.HasAPIKey() {
		t.Error("HasAPIKey() should be true")
	}
}

func TestProviderAPIKeyMissing(t *testing.T) {
	p := &Provider{APIKeyEnv: "MISSING_API_KEY"}
	_, err := p.APIKey()
	if err == nil {
		t.Error("APIKey() should error when env var not set")
	}
	if p.HasAPIKey() {
		t.Error("HasAPIKey() should be false")
	}
}

func TestProviderAPIKeyless(t *testing.T) {
	p := &Provider{}
	key, err := p.APIKey()
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		t.Errorf("APIKey() = %q, want empty", key)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	g := DefaultGlobal()
	g.Providers["test"] = Provider{
		Kind:      ProviderKindOpenAI,
		BaseURL:   "http://localhost:8081/v1",
		APIKeyEnv: "TEST_KEY",
	}
	os.Setenv("DUCKLAB_PROVIDER_TEST_BASE_URL", "http://override:9999/v1")
	defer os.Unsetenv("DUCKLAB_PROVIDER_TEST_BASE_URL")
	g.ApplyEnvOverrides()
	if g.Providers["test"].BaseURL != "http://override:9999/v1" {
		t.Errorf("BaseURL = %q, want override", g.Providers["test"].BaseURL)
	}
}
