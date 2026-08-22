// Package config handles global and project configuration: loading, merging,
// validation, defaults, and environment overrides. Strict TOML decoding is
// enforced — unknown keys are rejected.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/jrullan/ducklab/internal/xplat"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// ID is a validated identifier.
type ID string

// ProviderID is a provider identifier.
type ProviderID string

// DucklingID is a duckling identifier.
type DucklingID string

// Role is a job a duckling performs in a turn.
type Role string

const (
	RoleArchitect   Role = "architect"
	RoleImplementer Role = "implementer"
	RoleReviewer    Role = "reviewer"
	RoleJudge       Role = "judge"
	RoleTriager     Role = "triager"
	RoleAdvisor     Role = "advisor"
	RoleScribe      Role = "scribe"
	RoleConsultant  Role = "consultant"
	RoleHuman       Role = "human"
)

// ValidRoles returns all valid roles.
func ValidRoles() []Role {
	return []Role{
		RoleArchitect, RoleImplementer, RoleReviewer,
		RoleJudge, RoleTriager, RoleAdvisor, RoleScribe, RoleConsultant, RoleHuman,
	}
}

// Autonomy is the autonomy level.
type Autonomy string

const (
	AutonomyManual  Autonomy = "manual"
	AutonomyGuarded Autonomy = "guarded"
	AutonomyAuto    Autonomy = "auto"
	AutonomyYolo    Autonomy = "yolo"
)

// ValidAutonomies returns all valid autonomy levels.
func ValidAutonomies() []Autonomy {
	return []Autonomy{AutonomyManual, AutonomyGuarded, AutonomyAuto, AutonomyYolo}
}

// Mode is a duck mode.
type Mode string

const (
	ModeSolo       Mode = "solo"
	ModePair       Mode = "pair"
	ModeTournament Mode = "tournament"
	ModeCouncil    Mode = "council"
	ModeSplit      Mode = "split"
)

// ValidModes returns all valid modes.
func ValidModes() []Mode {
	return []Mode{ModeSolo, ModePair, ModeTournament, ModeCouncil, ModeSplit}
}

// Stage is a lifecycle stage.
type Stage string

const (
	StageIntake  Stage = "intake"
	StageSpec    Stage = "spec"
	StagePlan    Stage = "plan"
	StageBuild   Stage = "build"
	StageReview  Stage = "review"
	StageRelease Stage = "release"
	StageOperate Stage = "operate"
)

// ValidStages returns all valid stages.
func ValidStages() []Stage {
	return []Stage{StageIntake, StageSpec, StagePlan, StageBuild, StageReview, StageRelease, StageOperate}
}

// Budget is a set of caps.
type Budget struct {
	MaxUSD        float64 `toml:"max_usd" json:"max_usd"`
	MaxTokens     int64   `toml:"max_tokens" json:"max_tokens"`
	MaxWallclockS int     `toml:"max_wallclock_s" json:"max_wallclock_s"`
	MaxTurns      int     `toml:"max_turns" json:"max_turns"`
}

// Defaults holds global defaults.
type Defaults struct {
	Autonomy           Autonomy `toml:"autonomy" json:"autonomy"`
	Mode               Mode     `toml:"mode" json:"mode"`
	RepairAttempts     int      `toml:"repair_attempts" json:"repair_attempts"`
	ToolResultMaxBytes int      `toml:"tool_result_max_bytes" json:"tool_result_max_bytes"`
	AgentMaxTurns      int      `toml:"agent_max_turns" json:"agent_max_turns"`
	// AutopilotMaxTasks caps how many runs one autopilot activation may start
	// (0 = built-in default). AutopilotMaxFails is how many consecutive
	// failures stop the loop (0 = built-in default of 2).
	AutopilotMaxTasks int `toml:"autopilot_max_tasks" json:"autopilot_max_tasks"`
	AutopilotMaxFails int `toml:"autopilot_max_fails" json:"autopilot_max_fails"`
	// Rounds is how many rounds each mode runs, keyed by mode name. Absent or
	// zero leaves the script's own limit alone.
	//
	// The counts lived only in the scripts — pair three, council two, tournament
	// one — so the only way to change how many times a reviewer got to push back
	// was to edit Go and rebuild.
	Rounds map[string]int `toml:"rounds" json:"rounds"`
	// RoleTurns caps the model calls one turn of a given role may chain, keyed
	// by role. Absent or zero leaves the script's own cap alone.
	//
	// The caps were literals in five files — council 12, pair 24 and 8, triage
	// 6 — so a triager that used all six of its turns calling tools and never
	// answered told its reader to raise a number that could not be raised.
	RoleTurns map[string]int `toml:"role_turns" json:"role_turns"`
	// ModeDucklings is the duckling line-up to use for each mode when a run does
	// not name one. Ordered: tournament and split assign positionally, and pair
	// takes the first as implementer and the second as reviewer.
	//
	// A combination that works is a finding, and re-ticking the same boxes on
	// every run is how a finding gets lost.
	ModeDucklings map[string][]string `toml:"mode_ducklings" json:"mode_ducklings"`
	// ModeSeats is the canonical role-keyed global roster. Legacy ModeDucklings
	// is read only to support migration of existing installations.
	ModeSeats map[string]map[string][]string `toml:"mode_seats" json:"mode_seats"`
	// RolePins are mode-independent global seats (notably triager, scribe, and consultant).
	RolePins map[string][]string `toml:"role_pins" json:"role_pins"`
	// CandidateCriteria orders the evidence a seat's suggestions are ranked
	// by, per role: e.g. implementer = ["coding_index", "pass_rate",
	// "cost_per_run"]. One developer wants cost first, another the coding
	// index; the engine ships a default per role and this overrides it. An
	// empty list for a role turns its suggestions off. Keys are the
	// service's criterion catalog; unknown keys are refused at write.
	CandidateCriteria map[string][]string `toml:"candidate_criteria,omitempty" json:"candidate_criteria,omitempty"`
	// BuildMode and TestMode are the modes a launcher opens on: the person
	// who always builds in pair and tests in solo should not re-pick both on
	// every task.
	BuildMode        string `toml:"build_mode" json:"build_mode"`
	TestMode         string `toml:"test_mode" json:"test_mode"`
	HTTPTimeoutS     int    `toml:"http_timeout_s" json:"http_timeout_s"`
	TransientRetries int    `toml:"transient_retries" json:"transient_retries"`
	Budget           Budget `toml:"budget" json:"budget"`
}

// Engine holds engine configuration.
type Engine struct {
	Autostart             bool   `toml:"autostart" json:"autostart"`
	Port                  int    `toml:"port" json:"port"`
	Path                  string `toml:"path" json:"path"`
	MaxConcurrentRuns     int    `toml:"max_concurrent_runs" json:"max_concurrent_runs"`
	ShutdownGraceS        int    `toml:"shutdown_grace_s" json:"shutdown_grace_s"`
	ProjectMemoryMaxBytes int    `toml:"project_memory_max_bytes" json:"project_memory_max_bytes"`
}

// ProviderKind is the provider kind.
type ProviderKind string

const (
	ProviderKindOpenAI    ProviderKind = "openai"
	ProviderKindAnthropic ProviderKind = "anthropic"
)

// Provider is a configured endpoint.
type Provider struct {
	Kind          ProviderKind      `toml:"kind" json:"kind"`
	BaseURL       string            `toml:"base_url" json:"base_url"`
	APIKeyEnv     string            `toml:"api_key_env" json:"api_key_env"`
	Headers       map[string]string `toml:"headers" json:"headers"`
	MaxConcurrent int               `toml:"max_concurrent" json:"max_concurrent,omitempty"`
}

// SamplingParams holds sampling parameters.
type SamplingParams struct {
	Temperature     *float64 `toml:"temperature" json:"temperature"`
	TopP            *float64 `toml:"top_p" json:"top_p"`
	MaxTokens       *int     `toml:"max_tokens" json:"max_tokens"`
	DisableThinking bool     `toml:"disable_thinking" json:"disable_thinking"`
	Stop            []string `toml:"stop" json:"stop"`
}

// Cost holds cost configuration.
type Cost struct {
	InputPerMTok  float64 `toml:"input_per_mtok" json:"input_per_mtok"`
	OutputPerMTok float64 `toml:"output_per_mtok" json:"output_per_mtok"`
}

// Caps holds capability overrides.
type Caps struct {
	NativeTools   *bool `toml:"native_tools" json:"native_tools"`
	ContextTokens *int  `toml:"context_tokens" json:"context_tokens"`
	// Vision says the model accepts images. Declared, not probed: a probe
	// would cost an image round-trip per duckling.
	Vision   *bool `toml:"vision" json:"vision"`
	JSONMode *bool `toml:"json_mode" json:"json_mode"`
}

// Install declares the project's reinstall chain.
// References bounds how much attached reference material a stage ingests.
// Zero means the built-in default: the defaults suit a README-sized brief,
// and a mature multi-module wiki (MiEmpresa's) legitimately needs more.
// Declared per project, like verify — the budget is a property of the
// project's documentation, not of the binary.
type References struct {
	// PerFileChars caps each document; 0 means 12000.
	PerFileChars int `toml:"per_file_chars" json:"per_file_chars,omitempty"`
	// TotalChars caps the whole reference section; 0 means 80000.
	TotalChars int `toml:"total_chars" json:"total_chars,omitempty"`
	// MaxFiles caps how many documents load; 0 means 40.
	MaxFiles int `toml:"max_files" json:"max_files,omitempty"`
}

type Install struct {
	// Command rebuilds and installs the project's runnable form, run from
	// the project root ("make desktop && make install"). Empty means the
	// project declares none and the door stays closed.
	Command string `toml:"command" json:"command"`
	// TimeoutS bounds it; 0 means 600.
	TimeoutS int `toml:"timeout_s" json:"timeout_s,omitempty"`
}

// ExternalIndex is a third-party score, retained with provenance. Declared
// by hand in config, or fetched by the engine from a source it can name
// (OpenRouter's benchmarks endpoint carries Artificial Analysis's indices);
// Source says which, AsOf says when.
type ExternalIndex struct {
	CodingScore float64 `toml:"coding_score" json:"coding_score"`
	// Coding is the canonical short TOML spelling. CodingScore is retained for
	// the original on-disk spelling and the typed JSON contract.
	Coding float64 `toml:"coding,omitempty" json:"-"`
	// Companion indices from the same source, when it has them. Not
	// declared by hand today; carried so a future seat rule can use them.
	IntelligenceScore float64 `toml:"intelligence_score,omitempty" json:"intelligence_score,omitempty"`
	AgenticScore      float64 `toml:"agentic_score,omitempty" json:"agentic_score,omitempty"`
	Source            string  `toml:"source" json:"source"`
	AsOf              string  `toml:"as_of" json:"as_of"`
}

// Duckling is a named, configured model participant.
type Duckling struct {
	Provider ProviderID     `toml:"provider" json:"provider"`
	Model    string         `toml:"model" json:"model"`
	Roles    []Role         `toml:"roles" json:"roles"`
	Notes    string         `toml:"notes" json:"notes"`
	Params   SamplingParams `toml:"params" json:"params"`
	Caps     Caps           `toml:"caps" json:"caps"`
	Cost     Cost           `toml:"cost" json:"cost"`
	Index    *ExternalIndex `toml:"index,omitempty" json:"index,omitempty"`
	// Fallback names the duckling that takes this one's seats when its
	// provider is unreachable — declared here by the person, never chosen by
	// a router. Availability only; quality-based switching is Switchyard's
	// road and not ours.
	Fallback string `toml:"fallback,omitempty" json:"fallback,omitempty"`
	// Color is which of the eight series slots this duckling is drawn in.
	// 0 means the fleet order decides. A slot number rather than a hex, so the
	// palette keeps its light and dark variants and a duckling cannot be given
	// a colour that fails contrast in one theme.
	Color int `toml:"color" json:"color"`
}

// MCP holds MCP server configuration.
type MCP struct {
	Command string            `toml:"command" json:"command"`
	Args    []string          `toml:"args" json:"args"`
	Env     map[string]string `toml:"env" json:"env"`
	Enabled bool              `toml:"enabled" json:"enabled"`
}

// Global is the global configuration.
type Global struct {
	Schema    int                     `toml:"schema" json:"schema"`
	Defaults  Defaults                `toml:"defaults" json:"defaults"`
	Engine    Engine                  `toml:"engine" json:"engine"`
	Providers map[ProviderID]Provider `toml:"provider" json:"provider"`
	Ducklings map[DucklingID]Duckling `toml:"duckling" json:"duckling"`
	MCPs      map[string]MCP          `toml:"mcp" json:"mcp"`
	Notify    Notify                  `toml:"notify" json:"notify"`
}

// Notify is the outbound webhook: where the engine announces the moments a
// person (or the agent speaking for one) must know about — a run waiting at
// its gate, a run ending, the autopilot stopping. Integration plumbing, so
// it lives in the config file like the provider keys' env names do.
type Notify struct {
	WebhookURL string `toml:"webhook_url" json:"webhook_url"`
	// Secret signs each payload (X-Ducklab-Signature: hex HMAC-SHA256) so
	// the receiver can refuse forgeries. Optional on a loopback URL.
	Secret string `toml:"secret" json:"secret,omitempty"`
}

// ShellPolicy holds shell policy configuration.
type ShellPolicy struct {
	Mode          string   `toml:"mode" json:"mode"`
	Deny          []string `toml:"deny" json:"deny"`
	AllowPrefixes []string `toml:"allow_prefixes" json:"allow_prefixes"`
	TimeoutS      int      `toml:"timeout_s" json:"timeout_s"`
	Network       string   `toml:"network" json:"network"`
}

// Verify holds verification configuration.
type Verify struct {
	Mode   string `toml:"mode" json:"mode"`
	Tests  string `toml:"tests" json:"tests"`
	Build  string `toml:"build" json:"build"`
	Lint   string `toml:"lint" json:"lint"`
	Custom string `toml:"custom" json:"custom"`
	// LinkDeps are installed dependency trees borrowed from the live project
	// into an acceptance checkout. They must not be build products.
	LinkDeps []string `toml:"link_deps" json:"link_deps"`
	// Setup prepares build products in the acceptance checkout before its gate.
	Setup    string `toml:"setup" json:"setup"`
	TimeoutS int    `toml:"timeout_s" json:"timeout_s"`
	// TestGlobs says where this project's tests live, for the tampering
	// guard (05 §5.3). Empty means verify.DefaultTestGlobs, which covers the
	// usual Go, Python and TypeScript conventions.
	TestGlobs []string `toml:"test_globs" json:"test_globs"`
}

// Roster maps roles to ducklings.
type Roster map[Role]DucklingID

// Modes maps stages to modes.
type Modes map[Stage]Mode

// Git holds git configuration.
type Git struct {
	BranchPrefix   string   `toml:"branch_prefix" json:"branch_prefix"`
	BaseBranch     string   `toml:"base_branch" json:"base_branch"`
	CommitTrailer  bool     `toml:"commit_trailer" json:"commit_trailer"`
	ProtectedPaths []string `toml:"protected_paths" json:"protected_paths"`
}

// GitHub holds GitHub configuration.
type GitHub struct {
	Enabled    bool   `toml:"enabled" json:"enabled"`
	Repo       string `toml:"repo" json:"repo"`
	MirrorBugs bool   `toml:"mirror_bugs" json:"mirror_bugs"`
}

// Project is the project configuration.
type Project struct {
	Schema   int      `toml:"schema" json:"schema"`
	ID       string   `toml:"id" json:"id"`
	Name     string   `toml:"name" json:"name"`
	Describe string   `toml:"describe" json:"describe"`
	Created  string   `toml:"created" json:"created"`
	Autonomy Autonomy `toml:"autonomy" json:"autonomy"`
	Verify   Verify   `toml:"verify" json:"verify"`
	// Install declares how this project's own executables and assets are
	// rebuilt and installed, so a developer never has to leave ducklab to
	// make accepted work runnable (the self-hosted case: T-075's avatar sat
	// accepted and invisible until a terminal make install). Declared, not
	// guessed — the same rule as verify.
	Install    Install    `toml:"install" json:"install"`
	References References `toml:"references" json:"references"`
	Roster     Roster     `toml:"roster" json:"roster"`
	// RosterSeats preserves ordered multi-slot project pins; Roster is retained for legacy scalar pins.
	// Both are ROLE pins: they apply to every mode that seats the role.
	RosterSeats map[Role][]DucklingID `toml:"roster_seats" json:"roster_seats,omitempty"`
	// ModeSeats are the project's per-mode pins, the same shape as the
	// global defaults.mode_seats: mode → real role name → ordered ids. A pin
	// made on the board's Pair column lands here and only here; the role pins
	// above remain the mode-independent form (triager, scribe) and the
	// project's own fallback for a mode that pins nobody for that role.
	ModeSeats map[string]map[string][]string `toml:"mode_seats" json:"mode_seats,omitempty"`
	Modes     Modes                          `toml:"modes" json:"modes"`
	Budget    Budget                         `toml:"budget" json:"budget"`
	Git       Git                            `toml:"git" json:"git"`
	GitHub    GitHub                         `toml:"github" json:"github"`
	Shell     ShellPolicy                    `toml:"shell" json:"shell"`
	Run       RunApp                         `toml:"run" json:"run"`
}

// RunApp is how the built application actually starts — the stage the gate
// cannot see. A project shipped 48 accepted tasks and 161 green tests with
// no HTTP server and no built frontend: every task passed what the gate
// measured, and nothing measured "it boots". This section is the mirror of
// [verify] for the running system.
type RunApp struct {
	// Command starts the application, run through the platform shell from the
	// project root. The engine manages the process: start, stop, logs.
	Command string `toml:"command" json:"command"`
	// URL is where a person opens the running app.
	URL string `toml:"url" json:"url"`
	// Health is a URL the engine probes to report whether the running app is
	// actually answering — a process alive and a service alive are different
	// claims.
	Health string `toml:"health" json:"health"`
	// Preflight is a command that checks the ENVIRONMENT is ready — the
	// database reachable, the port free — run before every start. Exit 0
	// means go; anything else refuses the launch with the command's own
	// output, so "the app won't start" arrives as "postgres is not running"
	// instead of a crash to decode. The environment is the person's; this is
	// how the engine makes its edge visible instead of discoverable.
	Preflight string `toml:"preflight" json:"preflight"`
	// Requires is the human checklist for what the engine cannot check —
	// "PostgreSQL running on :55433; database exercise_tracker created" —
	// shown on the app card before the first launch. Semicolons separate
	// items.
	Requires string `toml:"requires" json:"requires"`
}

// Error is a configuration error.
type Error struct {
	File string
	Key  string
	Msg  string
}

func (e *Error) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("%s: %s: %s", e.File, e.Key, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.File, e.Msg)
}

var idRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// ValidateID validates an identifier.
func ValidateID(id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("invalid id %q: must match [a-z0-9][a-z0-9-]{0,31}", id)
	}
	return nil
}

// ValidateRole validates a role.
func ValidateRole(r Role) error {
	for _, v := range ValidRoles() {
		if r == v {
			return nil
		}
	}
	return fmt.Errorf("invalid role %q", r)
}

// ValidateAutonomy validates an autonomy level.
func ValidateAutonomy(a Autonomy) error {
	for _, v := range ValidAutonomies() {
		if a == v {
			return nil
		}
	}
	return fmt.Errorf("invalid autonomy %q", a)
}

// ValidateMode validates a mode.
func ValidateMode(m Mode) error {
	for _, v := range ValidModes() {
		if m == v {
			return nil
		}
	}
	return fmt.Errorf("invalid mode %q", m)
}

// ValidateStage validates a stage.
func ValidateStage(s Stage) error {
	for _, v := range ValidStages() {
		if s == v {
			return nil
		}
	}
	return fmt.Errorf("invalid stage %q", s)
}

// ValidateProviderKind validates a provider kind.
func ValidateProviderKind(k ProviderKind) error {
	if k != ProviderKindOpenAI && k != ProviderKindAnthropic {
		return fmt.Errorf("invalid provider kind %q", k)
	}
	return nil
}

// ValidateURL validates a URL is absolute.
func ValidateURL(u string) error {
	parsed, err := url.Parse(u)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", u, err)
	}
	if !parsed.IsAbs() {
		return fmt.Errorf("URL %q is not absolute", u)
	}
	return nil
}

// LoadGlobal loads the global configuration from a TOML file.
func LoadGlobal(path string) (*Global, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &Error{File: path, Msg: err.Error()}
	}
	// Start from the defaults and let the file override only what it sets.
	// Unmarshalling into a zero value made every unset key fail validation,
	// so a minimal config.toml was impossible to write.
	g := DefaultGlobal()
	md, err := toml.Decode(string(data), g)
	if err != nil {
		return nil, &Error{File: path, Msg: err.Error()}
	}
	for id, d := range g.Ducklings {
		if d.Index != nil && d.Index.CodingScore == 0 && d.Index.Coding != 0 {
			d.Index.CodingScore = d.Index.Coding
			g.Ducklings[id] = d
		}
	}
	if len(g.Defaults.ModeDucklings) > 0 {
		// Migrate legacy positional configuration exactly once; canonical values win.
		if len(g.Defaults.ModeSeats) == 0 {
			g.Defaults.ModeSeats = migrateModeSeats(g.Defaults.ModeDucklings)
		}
		g.Defaults.ModeDucklings = nil
		if err := SaveGlobal(path, g); err != nil {
			return nil, err
		}
	}
	if err := rejectUndecoded(path, md); err != nil {
		return nil, err
	}
	if err := g.Validate(path); err != nil {
		return nil, err
	}
	return g, nil
}

// Validate validates the global configuration.
func (g *Global) Validate(path string) error {
	if g.Schema != 1 {
		return &Error{File: path, Key: "schema", Msg: fmt.Sprintf("expected 1, got %d", g.Schema)}
	}
	if err := ValidateAutonomy(g.Defaults.Autonomy); err != nil {
		return &Error{File: path, Key: "defaults.autonomy", Msg: err.Error()}
	}
	if err := ValidateMode(g.Defaults.Mode); err != nil {
		return &Error{File: path, Key: "defaults.mode", Msg: err.Error()}
	}
	if g.Defaults.Budget.MaxUSD <= 0 {
		return &Error{File: path, Key: "defaults.budget.max_usd", Msg: "must be positive"}
	}
	if g.Defaults.Budget.MaxTokens <= 0 {
		return &Error{File: path, Key: "defaults.budget.max_tokens", Msg: "must be positive"}
	}
	if g.Defaults.Budget.MaxWallclockS <= 0 {
		return &Error{File: path, Key: "defaults.budget.max_wallclock_s", Msg: "must be positive"}
	}
	if g.Defaults.Budget.MaxTurns <= 0 {
		return &Error{File: path, Key: "defaults.budget.max_turns", Msg: "must be positive"}
	}
	for id, p := range g.Providers {
		if err := ValidateID(string(id)); err != nil {
			return &Error{File: path, Key: fmt.Sprintf("provider.%s", id), Msg: err.Error()}
		}
		if err := ValidateProviderKind(p.Kind); err != nil {
			return &Error{File: path, Key: fmt.Sprintf("provider.%s.kind", id), Msg: err.Error()}
		}
		if err := ValidateURL(p.BaseURL); err != nil {
			return &Error{File: path, Key: fmt.Sprintf("provider.%s.base_url", id), Msg: err.Error()}
		}
	}
	for id, d := range g.Ducklings {
		if err := ValidateID(string(id)); err != nil {
			return &Error{File: path, Key: fmt.Sprintf("duckling.%s", id), Msg: err.Error()}
		}
		if _, ok := g.Providers[d.Provider]; !ok {
			return &Error{File: path, Key: fmt.Sprintf("duckling.%s.provider", id), Msg: fmt.Sprintf("provider %q not defined", d.Provider)}
		}
		for _, r := range d.Roles {
			if err := ValidateRole(r); err != nil {
				return &Error{File: path, Key: fmt.Sprintf("duckling.%s.roles", id), Msg: err.Error()}
			}
		}
		if d.Cost.InputPerMTok < 0 || d.Cost.OutputPerMTok < 0 {
			return &Error{File: path, Key: fmt.Sprintf("duckling.%s.cost", id), Msg: "cost must be non-negative"}
		}
		if d.Index != nil {
			if _, err := time.Parse("2006-01-02", d.Index.AsOf); err != nil {
				return &Error{File: path, Key: fmt.Sprintf("duckling.%s.index.as_of", id), Msg: "must be YYYY-MM-DD"}
			}
		}
	}
	return nil
}

// DefaultGlobal returns the default global configuration.
func DefaultGlobal() *Global {
	return &Global{
		Schema: 1,
		Defaults: Defaults{
			Autonomy:           AutonomyGuarded,
			Mode:               ModeSolo,
			RepairAttempts:     2,
			ToolResultMaxBytes: 32768,
			AgentMaxTurns:      24,
			HTTPTimeoutS:       300,
			TransientRetries:   3,
			Budget: Budget{
				MaxUSD:        2.00,
				MaxTokens:     400000,
				MaxWallclockS: 1800,
				MaxTurns:      24,
			},
		},
		Engine: Engine{
			Autostart:             true,
			Port:                  0,
			Path:                  "",
			MaxConcurrentRuns:     2,
			ShutdownGraceS:        30,
			ProjectMemoryMaxBytes: 8192,
		},
		Providers: make(map[ProviderID]Provider),
		Ducklings: make(map[DucklingID]Duckling),
		MCPs:      make(map[string]MCP),
	}
}

// LoadProject loads the project configuration from a TOML file.
func LoadProject(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &Error{File: path, Msg: err.Error()}
	}
	p := DefaultProject("", "")
	if _, err := toml.Decode(string(data), p); err != nil {
		return nil, &Error{File: path, Msg: err.Error()}
	}
	// Tolerant at READ, strict at WRITE. An unknown key here used to refuse
	// the whole project — which turned every schema-extending task into a
	// self-deadlock: T-071's implementer declared the very key it was adding
	// (verify.link_deps) in the live project.toml, and the running engine —
	// one version older — could no longer load the project to finish the
	// run (B-075). A key this binary does not know is most likely a key a
	// NEWER tree just taught the next binary; it is preserved on disk (the
	// engine never rewrites the file wholesale except through SaveProject,
	// which is why writes stay strict) and reported as a warning by
	// UnknownProjectKeys for surfaces that can say it.
	if err := p.Validate(path); err != nil {
		return nil, err
	}
	return p, nil
}

// UnknownProjectKeys names the keys in a project file this binary does not
// know — worth a warning banner, never a refusal (see LoadProject).
func UnknownProjectKeys(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	p := DefaultProject("", "")
	md, err := toml.Decode(string(data), p)
	if err != nil {
		return nil
	}
	undecoded := md.Undecoded()
	keys := make([]string, 0, len(undecoded))
	for _, k := range undecoded {
		keys = append(keys, k.String())
	}
	sort.Strings(keys)
	return keys
}

// Validate validates the project configuration.
func (p *Project) Validate(path string) error {
	if p.Schema != 1 {
		return &Error{File: path, Key: "schema", Msg: fmt.Sprintf("expected 1, got %d", p.Schema)}
	}
	if err := ValidateID(p.ID); err != nil {
		return &Error{File: path, Key: "id", Msg: err.Error()}
	}
	if err := ValidateAutonomy(p.Autonomy); err != nil {
		return &Error{File: path, Key: "autonomy", Msg: err.Error()}
	}
	if p.Verify.Mode != "" && p.Verify.Mode != "auto" && p.Verify.Mode != "tests" &&
		p.Verify.Mode != "build" && p.Verify.Mode != "lint" && p.Verify.Mode != "none" &&
		p.Verify.Mode != "custom" {
		return &Error{File: path, Key: "verify.mode", Msg: fmt.Sprintf("invalid mode %q", p.Verify.Mode)}
	}
	if p.References.PerFileChars < 0 || p.References.TotalChars < 0 || p.References.MaxFiles < 0 {
		return &Error{File: path, Key: "references", Msg: "caps must be zero (default) or positive"}
	}
	for _, dep := range p.Verify.LinkDeps {
		if dep == "" || filepath.IsAbs(dep) || filepath.Clean(dep) != dep || dep == "." || strings.HasPrefix(dep, ".."+string(filepath.Separator)) || dep == ".." {
			return &Error{File: path, Key: "verify.link_deps", Msg: fmt.Sprintf("must contain clean relative paths, got %q", dep)}
		}
	}
	for role, ducklingID := range p.Roster {
		if err := ValidateRole(role); err != nil {
			return &Error{File: path, Key: fmt.Sprintf("roster.%s", role), Msg: err.Error()}
		}
		if err := ValidateID(string(ducklingID)); err != nil {
			return &Error{File: path, Key: fmt.Sprintf("roster.%s", role), Msg: err.Error()}
		}
	}
	for stage, mode := range p.Modes {
		if err := ValidateStage(stage); err != nil {
			return &Error{File: path, Key: fmt.Sprintf("modes.%s", stage), Msg: err.Error()}
		}
		if err := ValidateMode(mode); err != nil {
			return &Error{File: path, Key: fmt.Sprintf("modes.%s", stage), Msg: err.Error()}
		}
	}
	if p.Budget.MaxUSD < 0 {
		return &Error{File: path, Key: "budget.max_usd", Msg: "must be non-negative"}
	}
	return nil
}

// DefaultProject returns the default project configuration.
func DefaultProject(id, name string) *Project {
	return &Project{
		Schema:   1,
		ID:       id,
		Name:     name,
		Autonomy: AutonomyGuarded,
		Verify: Verify{
			Mode:     "auto",
			TimeoutS: 900,
		},
		Roster: make(Roster),
		Modes:  make(Modes),
		Budget: Budget{
			MaxUSD: 5.00,
		},
		Git: Git{
			BranchPrefix:  "ducklab/",
			BaseBranch:    "main",
			CommitTrailer: true,
		},
		Shell: ShellPolicy{
			Mode: "guarded",
			Deny: []string{"rm -rf /", "shutdown", "reboot", "mkfs", ":(){", "curl * | sh", "dd if="},
			AllowPrefixes: []string{
				"go ", "npm ", "pnpm ", "yarn ", "pytest", "python ", "cargo ",
				"make ", "ls", "cat", "grep", "rg", "sed -n", "find", "git status",
				"git diff", "git log", "node ", "tsc", "docker compose config",
			},
			TimeoutS: 120,
			Network:  "deny",
		},
	}
}

// ApplyEnvOverrides applies environment overrides to the global config.
func (g *Global) ApplyEnvOverrides() {
	// DUCKLAB_CONFIG is handled by the caller (path selection).
	// Provider overrides
	for id, p := range g.Providers {
		name := strings.ToUpper(string(id))
		name = strings.ReplaceAll(name, "-", "_")
		if v := os.Getenv("DUCKLAB_PROVIDER_" + name + "_BASE_URL"); v != "" {
			p.BaseURL = v
			g.Providers[id] = p
		}
		if v := os.Getenv("DUCKLAB_PROVIDER_" + name + "_API_KEY"); v != "" {
			// Direct value override — store in a special env var name
			// The actual key resolution happens at first use
			os.Setenv(p.APIKeyEnv, v)
		}
	}
	// Duckling overrides
	for id, d := range g.Ducklings {
		name := strings.ToUpper(string(id))
		name = strings.ReplaceAll(name, "-", "_")
		if v := os.Getenv("DUCKLAB_DUCKLING_" + name + "_MODEL"); v != "" {
			d.Model = v
			g.Ducklings[id] = d
		}
	}
}

// APIKey returns the API key for a provider, reading from the environment.
func (p *Provider) APIKey() (string, error) {
	if p.APIKeyEnv == "" {
		return "", nil // keyless
	}
	key := os.Getenv(p.APIKeyEnv)
	if key == "" {
		return "", fmt.Errorf("api_key_env %q is not set", p.APIKeyEnv)
	}
	return key, nil
}

// HasAPIKey returns whether the provider has an API key configured.
func (p *Provider) HasAPIKey() bool {
	if p.APIKeyEnv == "" {
		return false
	}
	return os.Getenv(p.APIKeyEnv) != ""
}

// ErrNotFound is returned when a config file does not exist.
var ErrNotFound = errors.New("config file not found")

// rejectUndecoded enforces strict decoding: any key in the file that no
// struct field claimed is a typo, and a silently ignored typo in a config
// file is a bug the user cannot see (02 §2.1).
//
// Keys under free-form maps (provider/duckling/mcp headers, env) are not
// reported by Undecoded, so this only fires on genuine unknowns.
func rejectUndecoded(path string, md toml.MetaData) error {
	undecoded := md.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}
	keys := make([]string, 0, len(undecoded))
	for _, k := range undecoded {
		keys = append(keys, k.String())
	}
	sort.Strings(keys)
	return &Error{File: path, Msg: fmt.Sprintf("unknown key %q", keys[0])}
}

// SaveProject writes a project config.
//
// Lives next to LoadProject deliberately: a hand-rolled writer in another
// package silently dropped roster, modes, budget, github and shell, so
// `roster set` appeared to work and lost the assignment on the next load.
// Encoding from the same struct the loader reads makes that class of drift
// impossible.
func SaveProject(path string, cfg *Project) error {
	if cfg == nil {
		return fmt.Errorf("nil project config")
	}
	var buf bytes.Buffer
	buf.WriteString("# Written by ducklab. Hand edits are preserved on the next write\n")
	buf.WriteString("# only for keys ducklab knows about.\n\n")
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encode project config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return xplat.AtomicWrite(path, buf.Bytes(), 0o644)
}

// SaveGlobal writes the global config, preserving the same guarantee.
// Secrets are never written: only api_key_env names, which are not secret (I10).
func SaveGlobal(path string, cfg *Global) error {
	if cfg == nil {
		return fmt.Errorf("nil global config")
	}
	var buf bytes.Buffer
	buf.WriteString("# mode_ducklings is retired; canonical seats are stored under defaults.mode_seats\n")
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encode global config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return xplat.AtomicWrite(path, buf.Bytes(), 0o600)
}

// EnsureGlobal loads the global config, creating a usable default if none
// exists.
//
// A first run must work. Refusing to start because config.toml is absent
// makes the app unusable until the user hand-writes TOML they have not been
// shown yet — the engine knows what a valid file looks like, so it writes one.
// An existing file is never rewritten: the user's edits are theirs.
// LegacyModeSeats translates the old positional line-up using the canonical
// role order. It is shared by file migration and API writes so the two paths
// cannot silently assign different seats.
func LegacyModeSeats(legacy map[string][]string) map[string]map[string][]string {
	out := make(map[string]map[string][]string)
	orders := map[string][]string{
		"solo":       {"implementer", "advisor"},
		"pair":       {"implementer", "reviewer"},
		"council":    {"architect", "reviewer"},
		"split":      {"architect", "implementer", "reviewer"},
		"tournament": {"implementer", "judge"},
	}
	for mode, ids := range legacy {
		roles, ok := orders[mode]
		if !ok || len(ids) == 0 {
			continue
		}
		seats := map[string][]string{}
		if mode == "council" {
			seats[roles[0]] = []string{ids[0]}
			if len(ids) > 1 {
				seats[roles[1]] = append([]string{}, ids[1:]...)
			}
		} else if mode == "pair" || mode == "solo" {
			seats[roles[0]] = []string{ids[0]}
			if len(ids) > 1 {
				seats[roles[1]] = []string{ids[1]}
			}
		} else if mode == "tournament" {
			// The positional tournament line-up named CONTESTANTS only; the
			// judge always came from the roster (the desktop labelled every
			// position "contestant N").
			seats[roles[0]] = append([]string{}, ids...)
		} else { // split: the positional line-up named WORKERS only; the
			// architect and reviewer came from the roster ("worker N").
			seats[roles[1]] = append([]string{}, ids...)
		}
		out[mode] = seats
	}
	return out
}

func migrateModeSeats(legacy map[string][]string) map[string]map[string][]string {
	return LegacyModeSeats(legacy)
}

func EnsureGlobal(path string) (*Global, bool, error) {
	if _, err := os.Stat(path); err == nil {
		cfg, err := LoadGlobal(path)
		return cfg, false, err
	} else if !os.IsNotExist(err) {
		return nil, false, &Error{File: path, Msg: err.Error()}
	}

	cfg := StarterGlobal()
	if err := SaveGlobal(path, cfg); err != nil {
		return nil, false, fmt.Errorf("write starter config %s: %w", path, err)
	}
	return cfg, true, nil
}

// StarterGlobal is the config a fresh install gets: a local endpoint and a
// hosted one, so a user with llama.cpp or vLLM already running can start
// immediately, and one with neither gets a file to edit rather than a blank.
//
// Neutral names only. The starter used to ship the maintainer's own LAN —
// a provider named after his inference box pointing at 10.0.0.5, and a
// duckling named for the machine on his desk — which every fresh public
// install then offered as if it were theirs (B-109).
func StarterGlobal() *Global {
	nativeFalse := false
	ctx := 32768
	g := DefaultGlobal()
	g.Providers = map[ProviderID]Provider{
		// llama.cpp's default --port; edit to wherever your server listens.
		"local":      {Kind: ProviderKindOpenAI, BaseURL: "http://localhost:8080/v1"},
		"openrouter": {Kind: ProviderKindOpenAI, BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY"},
	}
	g.Ducklings = map[DucklingID]Duckling{
		"pato-local": {
			Provider: "local", Model: "local-model",
			Notes: "edit model to match what your endpoint serves",
			Caps:  Caps{NativeTools: &nativeFalse, ContextTokens: &ctx},
		},
	}
	return g
}
