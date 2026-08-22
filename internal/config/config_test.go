package config

import (
	"os"
	"path/filepath"
	"reflect"
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

// A minimal config file must inherit defaults rather than fail validation.
// Unmarshalling into a zero value left every unset key at Go's zero value,
// so even a valid two-line config was rejected.
func TestLoadGlobalAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	minimal := `schema = 1

[provider.fake]
kind = "openai"
base_url = "fake://"
`
	if err := os.WriteFile(path, []byte(minimal), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("minimal config rejected: %v", err)
	}
	def := DefaultGlobal()
	if g.Defaults.Autonomy != def.Defaults.Autonomy {
		t.Errorf("autonomy = %q, want default %q", g.Defaults.Autonomy, def.Defaults.Autonomy)
	}
	if g.Defaults.Mode != def.Defaults.Mode {
		t.Errorf("mode = %q, want default %q", g.Defaults.Mode, def.Defaults.Mode)
	}
	if g.Engine.MaxConcurrentRuns != def.Engine.MaxConcurrentRuns {
		t.Errorf("max_concurrent_runs = %d, want default %d",
			g.Engine.MaxConcurrentRuns, def.Engine.MaxConcurrentRuns)
	}
	if _, ok := g.Providers["fake"]; !ok {
		t.Error("provider from the file was lost")
	}
}

// An explicit value in the file must still win over the default.
func TestLoadGlobalFileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `schema = 1

[defaults]
autonomy = "manual"

[engine]
max_concurrent_runs = 7
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if g.Defaults.Autonomy != "manual" {
		t.Errorf("autonomy = %q, want manual", g.Defaults.Autonomy)
	}
	if g.Engine.MaxConcurrentRuns != 7 {
		t.Errorf("max_concurrent_runs = %d, want 7", g.Engine.MaxConcurrentRuns)
	}
}

// The hand-rolled writer this replaced dropped roster, modes, budget, github
// and shell, so `roster set` appeared to work and lost the assignment on the
// next load. A full round trip is the only thing that catches that.
func TestSaveProjectRoundTripsEveryField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.toml")

	original := DefaultProject("miempresa", "MiEmpresa")
	original.Autonomy = AutonomyManual
	original.Roster = Roster{
		RoleImplementer: "pato-local",
		RoleReviewer:    "pato-nube",
		RoleJudge:       "pato-nube",
	}
	original.Modes = Modes{StageBuild: ModePair, StageIntake: ModeCouncil}
	original.Budget = Budget{MaxUSD: 5, MaxTokens: 400000, MaxWallclockS: 1800, MaxTurns: 24}
	original.Verify = Verify{Mode: "tests", Tests: "go test ./...", TimeoutS: 900}
	original.Git = Git{BranchPrefix: "ducklab/", BaseBranch: "main", CommitTrailer: true}
	original.GitHub = GitHub{Enabled: true, Repo: "jrullan/ducklab"}
	original.Shell = ShellPolicy{Mode: "guarded", TimeoutS: 120, Network: "deny",
		AllowPrefixes: []string{"go ", "npm "}, Deny: []string{"rm -rf /"}}

	if err := SaveProject(path, original); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProject(path)
	if err != nil {
		t.Fatalf("saved config does not load back: %v", err)
	}

	if loaded.Autonomy != original.Autonomy {
		t.Errorf("autonomy = %q, want %q", loaded.Autonomy, original.Autonomy)
	}
	for role, want := range original.Roster {
		if got := loaded.Roster[role]; got != want {
			t.Errorf("roster[%s] = %q, want %q", role, got, want)
		}
	}
	for stage, want := range original.Modes {
		if got := loaded.Modes[stage]; got != want {
			t.Errorf("modes[%s] = %q, want %q", stage, got, want)
		}
	}
	if loaded.Budget != original.Budget {
		t.Errorf("budget = %+v, want %+v", loaded.Budget, original.Budget)
	}
	if loaded.Verify.Tests != original.Verify.Tests || loaded.Verify.TimeoutS != original.Verify.TimeoutS {
		t.Errorf("verify = %+v", loaded.Verify)
	}
	if loaded.Git.BranchPrefix != original.Git.BranchPrefix ||
		loaded.Git.BaseBranch != original.Git.BaseBranch ||
		loaded.Git.CommitTrailer != original.Git.CommitTrailer {
		t.Errorf("git = %+v, want %+v", loaded.Git, original.Git)
	}
	if loaded.GitHub != original.GitHub {
		t.Errorf("github = %+v, want %+v", loaded.GitHub, original.GitHub)
	}
	if loaded.Shell.Mode != original.Shell.Mode || len(loaded.Shell.AllowPrefixes) != 2 {
		t.Errorf("shell = %+v", loaded.Shell)
	}
}

// Saving must survive strict decoding: an encoder emitting a key the loader
// rejects would make every write unloadable.
func TestSavedProjectPassesStrictDecoding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.toml")
	if err := SaveProject(path, DefaultProject("p", "P")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProject(path); err != nil {
		t.Fatalf("a config ducklab wrote was rejected by its own loader: %v", err)
	}
}

// max_concurrent is deliberately an optional provider setting: a configured
// value survives strict decode and save, while omission retains zero so queue
// defaults remain a live runtime decision rather than persisted migration data.
func TestProviderMaxConcurrentRoundTripsAndOmissionStaysZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `schema = 1

[provider.local]
kind = "openai"
base_url = "http://localhost:8081/v1"
max_concurrent = 3

[provider.hosted]
kind = "openai"
base_url = "https://api.example.test/v1"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("max_concurrent was not accepted by strict config loading: %v", err)
	}
	if got := providerMaxConcurrent(t, loaded.Providers["local"]); got != 3 {
		t.Errorf("loaded local max_concurrent = %d, want 3", got)
	}
	if got := providerMaxConcurrent(t, loaded.Providers["hosted"]); got != 0 {
		t.Errorf("omitted hosted max_concurrent = %d, want zero", got)
	}
	if err := SaveGlobal(path, loaded); err != nil {
		t.Fatal(err)
	}
	roundTripped, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := providerMaxConcurrent(t, roundTripped.Providers["local"]); got != 3 {
		t.Errorf("round-tripped local max_concurrent = %d, want 3", got)
	}
	if got := providerMaxConcurrent(t, roundTripped.Providers["hosted"]); got != 0 {
		t.Errorf("round-tripped omitted max_concurrent = %d, want zero", got)
	}
}

func providerMaxConcurrent(t *testing.T, p Provider) int {
	t.Helper()
	field := reflect.ValueOf(p).FieldByName("MaxConcurrent")
	if !field.IsValid() || field.Kind() != reflect.Int {
		t.Fatal("provider has no MaxConcurrent int")
	}
	return int(field.Int())
}

func TestSaveGlobalRoundTripsProvidersAndDucklings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	g := DefaultGlobal()
	g.Providers = map[ProviderID]Provider{
		"openrouter": {Kind: ProviderKindOpenAI, BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY"},
	}
	nativeTools := true
	g.Ducklings = map[DucklingID]Duckling{
		"pato-nube": {Provider: "openrouter", Model: "qwen/qwen3.6",
			Roles: []Role{RoleImplementer, RoleReviewer},
			Caps:  Caps{NativeTools: &nativeTools},
			Cost:  Cost{InputPerMTok: 0.2, OutputPerMTok: 0.6}},
	}
	if err := SaveGlobal(path, g); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("saved global config does not load back: %v", err)
	}
	p, ok := loaded.Providers["openrouter"]
	if !ok || p.APIKeyEnv != "OPENROUTER_API_KEY" {
		t.Errorf("provider lost: %+v", loaded.Providers)
	}
	d, ok := loaded.Ducklings["pato-nube"]
	if !ok || d.Model != "qwen/qwen3.6" || len(d.Roles) != 2 {
		t.Errorf("duckling lost: %+v", loaded.Ducklings)
	}
	if d.Caps.NativeTools == nil || !*d.Caps.NativeTools {
		t.Errorf("declared caps lost: %+v", d.Caps)
	}
	if d.Cost.OutputPerMTok != 0.6 {
		t.Errorf("cost lost: %+v", d.Cost)
	}
}

// I10: the global config file must never contain a secret value.
func TestSaveGlobalWritesNoSecretValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("SECRET_TEST_KEY", "sk-do-not-write-me")
	g := DefaultGlobal()
	g.Providers = map[ProviderID]Provider{
		"p": {Kind: ProviderKindOpenAI, BaseURL: "https://x/v1", APIKeyEnv: "SECRET_TEST_KEY"},
	}
	if err := SaveGlobal(path, g); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "sk-do-not-write-me") {
		t.Error("an API key value was written to config.toml")
	}
	if !strings.Contains(string(data), "SECRET_TEST_KEY") {
		t.Error("the env var name should be written; only the value is secret")
	}
}
