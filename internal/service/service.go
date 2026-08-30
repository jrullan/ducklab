// Package service is the capability layer. Every operation Ducklab can
// perform is a plain Go method on Service. Both the engine handlers and
// the in-process desktop fallback call only this. No HTTP here.
package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/build"
	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/registry"
	"github.com/jrullan/ducklab/internal/release"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/store"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/tools"
	"github.com/jrullan/ducklab/internal/vcs"
	"github.com/jrullan/ducklab/internal/verify"
)

// Service is the capability layer.
type Service struct {
	cfg       *config.Global
	registry  *registry.Registry
	ducklings *duckling.Registry
	// configPath is where cfg came from. Writing ducklings and providers back
	// is the only reason the service needs to know.
	configPath string
	// cfgMu guards cfg and the registries it feeds, which a config edit
	// rebuilds while runs may be reading them.
	cfgMu     sync.RWMutex
	bus       *bus.Bus
	runs      map[string]*runState
	runsMu    sync.RWMutex
	providers map[config.ProviderID]provider.Provider
	projects  map[string]*projectState
	projMu    sync.RWMutex
	// shuttingDown makes an in-flight run's cancellation read as a deliberate
	// pause rather than a failure, so a graceful stop never marks work FAILED.
	shuttingDown atomic.Bool
	// indexes caches third-party model indices fetched for the scorecards
	// (benchindex.go); zero value is ready to use.
	indexes indexFetcher
	// now and restartRecoveryDeadline make the bounded restart checkpoint
	// recoverable without tying tests or recovery logic to wall-clock sleeps.
	now                     func() time.Time
	restartRecoveryDeadline time.Duration
	queue                   *runQueue
	// The autopilot's per-project state; in-memory on purpose — an engine
	// restart lands every loop OFF (autopilot.go).
	autopilots map[string]*autopilotState
	apMu       sync.Mutex
	appMu      sync.Mutex
	apps       map[string]*appState
	// afterRunDiff is a lifecycle observation seam used by tests that must
	// inspect an isolated checkout before its normal cleanup.
	afterRunDiff func(*runState)
}

type projectState struct {
	cfg  *config.Project
	db   *store.DB
	git  *vcs.Git
	lock sync.Mutex
}

type runState struct {
	run    *runlog.Run
	writer *runlog.Writer // nil for rehydrated runs until a mutation needs it
	runDir string
	// execCtx is the run's toolbelt context, kept so a pause can describe
	// the run's SHAPE — a person deciding whether to lift a cap deserves to
	// know the gate has failed thirty times in a row before they feed it.
	execCtx *tools.ExecContext
	// gateRoot records the checkout used by the run's final gate.
	gateRoot string
	// projectPath is kept so a rehydrated run can open its writer without
	// a registry lookup that may have changed since the run started.
	projectPath string
	cancel      context.CancelFunc
	done        chan struct{}
	wmu         sync.Mutex
	// givenAnswers holds human answers keyed by question id, so a resumed run
	// replays its turn without asking the same question again.
	givenAnswers map[string]string
	// qa keeps the same answers WITH their question text, in the order given,
	// for the replayed prompt — the id match above only survives an exactly
	// reworded question, and models reword.
	qa []qaPair
	// tracker is what the run has spent. Kept here so a panic can still record
	// it: recordSpend runs before the error branch on an ordinary failure, but a
	// panic skips straight to the deferred recover, and the run was written out
	// with zero tokens and zero cost while its per-duckling breakdown — updated
	// on every call — said otherwise. A run that contradicts itself is worse
	// than one that admits it does not know.
	tracker *budget.Tracker
	// capLifted is the calls lift's live half: an atomic the agent loops
	// consult before every model call, flipped by RunBudgetLift("calls") from
	// another goroutine while a reply is in flight. The durable half lives on
	// the record (run.AgentTurns = -1) for resume.
	capLifted atomic.Bool
	// historyEscalated is set once when a live run crosses its historical
	// duration threshold; it prevents repeated suggestions while cancellation
	// unwinds the strategy.
	historyEscalated atomic.Bool
	// pauseAfterTurn asks the run to pause at its next safe point — the end
	// of the turn in flight — instead of cancelling mid-turn. A history
	// escalation used to cancel at once and throw away a 110 s reviewer turn
	// (Neocapture plan, 2026-08-29). pausePending holds the card to file when
	// the pause lands.
	pauseAfterTurn atomic.Bool
	pausePending   map[string]interface{}
	// refMu guards the run's reference bookkeeping: ref_read executes on
	// agent turns while critics may run concurrently.
	refMu sync.Mutex
	// refPaths are the resolved reference files this run was launched with;
	// refReads marks the ones ref_read actually opened. In digest mode the
	// difference is reported at the gate — "considered" must be observable.
	refPaths []string
	refReads map[string]bool
	// refMode is "inline" or "digest"; empty when the run has no references.
	refMode string
}

// armRefs records the run's reference corpus and mode for ref_read.
func (rs *runState) armRefs(files []string, mode string) {
	rs.refMu.Lock()
	defer rs.refMu.Unlock()
	rs.refPaths = files
	rs.refMode = mode
	rs.refReads = map[string]bool{}
}

func (rs *runState) refFiles() []string {
	rs.refMu.Lock()
	defer rs.refMu.Unlock()
	return append([]string(nil), rs.refPaths...)
}

func (rs *runState) markRefRead(path string) {
	rs.refMu.Lock()
	defer rs.refMu.Unlock()
	if rs.refReads != nil {
		rs.refReads[path] = true
	}
}

// unreadRefs names digest-mode references no one opened. Inline runs return
// nil: the full text was in the prompt, there was nothing left to open.
func (rs *runState) unreadRefs() []string {
	rs.refMu.Lock()
	defer rs.refMu.Unlock()
	if rs.refMode != "digest" {
		return nil
	}
	var out []string
	for _, p := range rs.refPaths {
		if !rs.refReads[p] {
			out = append(out, p)
		}
	}
	return out
}

// Options are service options.
type Options struct {
	Bus *bus.Bus
	// Now is a clock seam for bounded restart recovery. Nil uses time.Now.
	Now func() time.Time
	// RestartRecoveryDeadline bounds an uncompleted restart request. Zero uses
	// the production default.
	RestartRecoveryDeadline time.Duration
	// ConfigPath is the file the global config was loaded from, so changes to
	// ducklings and providers can be written back. Empty makes those
	// operations fail with a clear message rather than appear to work.
	ConfigPath string
}

// New creates a new service.
func New(cfg *config.Global, opts Options) (*Service, error) {
	reg, err := registry.Load()
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	restartRecoveryDeadline := opts.RestartRecoveryDeadline
	if restartRecoveryDeadline <= 0 {
		restartRecoveryDeadline = 30 * time.Second
	}
	s := &Service{
		cfg:                     cfg,
		now:                     now,
		restartRecoveryDeadline: restartRecoveryDeadline,
		configPath:              opts.ConfigPath,
		registry:                reg,
		ducklings:               duckling.NewRegistry(),
		bus:                     opts.Bus,
		runs:                    make(map[string]*runState),
		providers:               make(map[config.ProviderID]provider.Provider),
		projects:                make(map[string]*projectState),
		queue:                   newRunQueue(cfg.Engine.MaxConcurrentRuns),
		apps:                    make(map[string]*appState),
	}
	s.queue.held = s.projectHeld
	s.queue.limitFn = func() int {
		s.cfgMu.RLock()
		defer s.cfgMu.RUnlock()
		return s.cfg.Engine.MaxConcurrentRuns
	}
	s.queue.providerCap = func(id string) (int, bool) {
		s.cfgMu.RLock()
		defer s.cfgMu.RUnlock()
		p, ok := s.cfg.Providers[config.ProviderID(id)]
		if !ok {
			return 0, false
		}
		if p.MaxConcurrent > 0 {
			return p.MaxConcurrent, true
		}
		return func() int {
			if IsLocalHost(p.BaseURL) {
				return 1
			}
			return 8
		}(), true
	}

	// Register providers
	for id, p := range cfg.Providers {
		prov, err := createProvider(id, p)
		if err != nil {
			return nil, fmt.Errorf("create provider %s: %w", id, err)
		}
		s.providers[id] = prov
		s.ducklings.RegisterProvider(prov)
	}

	// Register ducklings
	for id, d := range cfg.Ducklings {
		s.ducklings.Register(duckling.FromConfig(id, d))
	}

	s.startNotifier()

	return s, nil
}

func createProvider(id config.ProviderID, cfg config.Provider) (provider.Provider, error) {
	apiKey, _ := cfg.APIKey() // keyless is OK
	// Special case: fake provider for testing
	if cfg.BaseURL == "http://127.0.0.1:1/v1" || cfg.BaseURL == "fake://" {
		fake := provider.NewFake(string(id))
		fake.AddTextResponse("OK")
		fake.AddTextResponse("I am a fake duckling.")
		// Script for implementation tasks: fix add.go via fs_patch
		fake.ScriptFunc = func(req provider.ChatRequest, callCount int) *provider.ChatResponse {
			// A task named T-ASK exercises the human-gate path: the fake asks
			// a question, which pauses the run until someone answers.
			for _, m := range req.Messages {
				if m.Role == "user" && strings.Contains(m.Content, "T-ASK") {
					if callCount == 1 {
						return &provider.ChatResponse{
							Choices: []provider.Choice{{
								Message: provider.Message{
									Role:    "assistant",
									Content: "I need to know which behaviour you want.\n```ducklab\n{\"tool\":\"ask_human\",\"args\":{\"question\":\"Should Add saturate or wrap on overflow?\"}}\n```",
								},
								FinishReason: provider.FinishStop,
							}},
							Usage: provider.Usage{PromptTokens: 60, CompletionTokens: 30},
						}
					}
					return &provider.ChatResponse{
						Choices: []provider.Choice{{
							Message:      provider.Message{Role: "assistant", Content: "Understood; proceeding with the stated behaviour."},
							FinishReason: provider.FinishStop,
						}},
						Usage: provider.Usage{PromptTokens: 60, CompletionTokens: 12},
					}
				}
			}

			// Reviewer and judge turns are recognised by their system prompt,
			// so the fake can satisfy their contracts. Without this every
			// multi-model mode fails at the first review with a contract
			// error, and the e2e tests can never exercise pair or tournament.
			for _, m := range req.Messages {
				if m.Role != "system" {
					continue
				}
				// Adoption inventory turns satisfy the json:inventory contract.
				if strings.Contains(m.Content, "You are the architect") {
					for _, um := range req.Messages {
						if um.Role == "user" && strings.Contains(um.Content, "return the inventory JSON") {
							return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: `{"items":[{"name":"sighting records","kind":"schema","evidence-path":"internal/service/service.go"}]}`}, FinishReason: provider.FinishStop}}, Usage: provider.Usage{PromptTokens: 90, CompletionTokens: 20}}
						}
					}
				}
				// Architect turns satisfy the markdown_sections contract, so
				// stage runs can produce a real proposal under test instead of
				// failing after the spend is recorded.
				if strings.Contains(m.Content, "You are the architect") {
					// The stage decides the prefix; the fake reads which
					// document was asked for so every stage can produce a
					// parseable proposal under test.
					doc := "## REQ-001 — Records a sighting\n\n**Priority:** must\n\nThe tool records one sighting per call.\n"
					for _, um := range req.Messages {
						if um.Role != "user" {
							continue
						}
						if strings.Contains(um.Content, "Write the specification") {
							doc = "## SPEC-001 — Sighting store\n\n**Implements:** REQ-001\n\nOne row per sighting.\n"
						} else if strings.Contains(um.Content, "milestones") {
							doc = "## M-001 — Core\n\n### T-001 — Store sightings\n\n**Implements:** SPEC-001\n\nBuild it.\n"
						}
					}
					return &provider.ChatResponse{
						Choices: []provider.Choice{{
							Message:      provider.Message{Role: "assistant", Content: doc},
							FinishReason: provider.FinishStop,
						}},
						Usage: provider.Usage{PromptTokens: 90, CompletionTokens: 30},
					}
				}
				if strings.Contains(m.Content, "You are the reviewer") {
					return &provider.ChatResponse{
						Choices: []provider.Choice{{
							Message:      provider.Message{Role: "assistant", Content: `{"verdict":"approve","findings":[]}`},
							FinishReason: provider.FinishStop,
						}},
						Usage: provider.Usage{PromptTokens: 80, CompletionTokens: 12},
					}
				}
				// Without this the operate loop cannot be exercised at all: the
				// triager's turn fails its contract, the run records
				// triage_failed, and no test can tell a working triage from a
				// broken one.
				if strings.Contains(m.Content, "You are the triager") {
					return &provider.ChatResponse{
						Choices: []provider.Choice{{
							Message: provider.Message{Role: "assistant", Content: `Looks like a hit-test bug.
{"severity":"critical","duplicate_of":"","component":"vertex drag",
 "suspected_files":["index.html"],"reproducible":true,
 "task_title":"Fix the vertex hit test","reason":"the hit test never matches"}`},
							FinishReason: provider.FinishStop,
						}},
						Usage: provider.Usage{PromptTokens: 70, CompletionTokens: 40},
					}
				}
				if strings.Contains(m.Content, "You are the judge") {
					return &provider.ChatResponse{
						Choices: []provider.Choice{{
							Message:      provider.Message{Role: "assistant", Content: `{"choice":"A","reason":"only candidate whose verification passed"}`},
							FinishReason: provider.FinishStop,
						}},
						Usage: provider.Usage{PromptTokens: 90, CompletionTokens: 14},
					}
				}
			}

			// Check if this is an implementation prompt about fixing a bug
			isImplPrompt := false
			for _, m := range req.Messages {
				if m.Role == "user" && (strings.Contains(m.Content, "Implement task") || strings.Contains(m.Content, "make TestAdd pass") || strings.Contains(m.Content, "fix")) {
					isImplPrompt = true
					break
				}
			}
			if !isImplPrompt {
				return nil
			}
			// A fake provider is shared by concurrent runs. Identify the current
			// conversation from its tool result rather than its global call count.
			hasToolResult := false
			for _, m := range req.Messages {
				if m.Role == "tool" || (m.Role == "user" && strings.HasPrefix(m.Content, "Tool result for ")) {
					hasToolResult = true
					break
				}
			}
			// Dialect B: no tools in request, respond with text protocol
			if len(req.Tools) == 0 {
				if !hasToolResult {
					return &provider.ChatResponse{
						Choices: []provider.Choice{{
							Message: provider.Message{
								Role:    "assistant",
								Content: "I will fix add.go.\n```ducklab\n{\"tool\":\"fs_patch\",\"args\":{\"path\":\"add.go\",\"edits\":[{\"search\":\"return a - b // BUG: should be a + b\",\"replace\":\"return a + b\"}]}}\n```",
							},
							FinishReason: provider.FinishStop,
						}},
						Usage: provider.Usage{PromptTokens: 100, CompletionTokens: 50},
					}
				}
				return &provider.ChatResponse{
					Choices: []provider.Choice{{
						Message:      provider.Message{Role: "assistant", Content: "Fixed add.go: changed a - b to a + b."},
						FinishReason: provider.FinishStop,
					}},
					Usage: provider.Usage{PromptTokens: 100, CompletionTokens: 20},
				}
			}
			// Dialect A: native tool calls.
			if !hasToolResult {
				return &provider.ChatResponse{
					Choices: []provider.Choice{{
						Message: provider.Message{
							Role: "assistant",
							ToolCalls: []provider.ToolCall{{
								ID:   "call_1",
								Type: "function",
								Function: struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								}{
									Name:      "fs_patch",
									Arguments: `{"path":"add.go","edits":[{"search":"return a - b // BUG: should be a + b","replace":"return a + b"}]}`,
								},
							}},
						},
						FinishReason: provider.FinishToolCalls,
					}},
					Usage: provider.Usage{PromptTokens: 100, CompletionTokens: 50},
				}
			}
			return &provider.ChatResponse{
				Choices: []provider.Choice{{
					Message:      provider.Message{Role: "assistant", Content: "Fixed add.go: changed a - b to a + b."},
					FinishReason: provider.FinishStop,
				}},
				Usage: provider.Usage{PromptTokens: 100, CompletionTokens: 20},
			}
		}
		return fake, nil
	}
	switch cfg.Kind {
	case config.ProviderKindOpenAI:
		return provider.NewOpenAICompat(string(id), cfg.BaseURL, apiKey,
			provider.WithHeaders(cfg.Headers)), nil
	case config.ProviderKindAnthropic:
		return provider.NewAnthropic(string(id), cfg.BaseURL, apiKey), nil
	default:
		return nil, fmt.Errorf("unknown provider kind %q", cfg.Kind)
	}
}

// Project represents a project.
type Project struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Name string `json:"name"`
	// Branch is the current checked-out branch, when the project is a git worktree.
	Branch   string          `json:"branch,omitempty"`
	Config   *config.Project `json:"config"`
	Gate     string          `json:"gate"`
	Autonomy string          `json:"autonomy"`
	// Missing is true when the path no longer exists. MarkMissing computed it
	// and ProjectList then dropped it on the floor, so every client saw a
	// deleted project as a perfectly healthy one.
	Missing bool `json:"missing,omitempty"`
	// HasCode is true when the tree holds committed files beyond .ducklab.
	// It decides which doors the Cycle's empty state offers: a codebase that
	// already exists is adopted — its requirements surveyed from the tree —
	// not interviewed into existence as if the product were an idea.
	HasCode bool `json:"has_code,omitempty"`
}

// Status is the project status.
type Status struct {
	Ahead              int               `json:"ahead,omitempty"`
	Behind             int               `json:"behind,omitempty"`
	StageProgress      map[string]string `json:"stage_progress"`
	WorkingTreeDirty   bool              `json:"working_tree_dirty,omitempty"`
	TaskCounts         map[string]int    `json:"task_counts"`
	BudgetSpent        float64           `json:"budget_spent_today"`
	ActiveRuns         int               `json:"active_runs"`
	AcceptedUnreleased int               `json:"accepted_unreleased"`
	UnreleasedBranches int               `json:"unreleased_branches"`
	Provenance         string            `json:"provenance,omitempty"`
}

// resolveProjectPath turns a path a person typed into one the engine can use.
//
// Two things go wrong otherwise, and both did.
//
// `~` is a shell feature. A client sending "~/dev/calculator" made a directory
// literally named "~", nested under wherever the engine was launched from. The
// project worked perfectly and was somewhere nobody would ever look.
//
// A relative path resolves against the engine's working directory, which is
// wherever someone happened to start the daemon — arbitrary, invisible from
// any client, and not what the person meant by "calculator". Refused rather
// than guessed: there is no correct guess.
func resolveProjectPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("a project path is required")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot expand ~: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/")), nil
	}
	if strings.HasPrefix(path, "~") {
		// "~user/x" is a shell feature this does not implement. Treating it as
		// a relative directory would repeat the original bug in a new shape.
		return "", fmt.Errorf("%q: another user's home directory is not supported; give a full path", path)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf(
			"%q is relative, and the engine has no idea what it is relative to — "+
				"it runs as a daemon, not in your shell. Give a full path, or use the folder chooser.", path)
	}
	return filepath.Clean(path), nil
}

// InitRequest is a project init request.
type InitRequest struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Describe string `json:"describe"`
	GitInit  bool   `json:"git_init"`
}

// ProjectOpen opens a project.
func (s *Service) ProjectOpen(ctx context.Context, path string) (*Project, error) {
	absPath, err := resolveProjectPath(path)
	if err != nil {
		return nil, err
	}
	// Check .ducklab exists
	ducklabDir := filepath.Join(absPath, ".ducklab")
	if _, err := os.Stat(ducklabDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("not a ducklab project (no .ducklab directory): %s", absPath)
	}
	// Load project config
	cfg, err := config.LoadProject(filepath.Join(ducklabDir, "project.toml"))
	if err != nil {
		return nil, err
	}
	// Register
	id, err := s.registry.Register(absPath, cfg.Name)
	if err != nil {
		return nil, err
	}
	// A re-opened project brings its in-flight runs back with it. Forget
	// drops them from memory, and RecoverRuns only ran at engine start —
	// so a forget-and-reopen showed an intake proposal whose run the
	// engine no longer knew, and every decision on it failed (B-083).
	if err := s.RecoverRuns(ctx); err != nil {
		return nil, fmt.Errorf("recover the project's runs: %w", err)
	}
	if cfg.Remote.FetchOnOpen {
		if err := vcs.New(absPath).Fetch(cfg.Remote.Name); err != nil {
			s.remoteWarning(id, "fetch failed: "+err.Error(), nil)
		}
	}
	s.auditRemote(ctx, id, absPath, cfg.Remote.Name)
	return &Project{
		ID:       id,
		Path:     absPath,
		Name:     cfg.Name,
		Config:   cfg,
		Autonomy: string(cfg.Autonomy),
	}, nil
}

// ProjectInit initializes a new project.
func (s *Service) ProjectInit(ctx context.Context, req InitRequest) (*Project, error) {
	absPath, err := resolveProjectPath(req.Path)
	if err != nil {
		return nil, err
	}
	// Check if already a project (project.toml exists)
	ducklabDir := filepath.Join(absPath, ".ducklab")
	projectTOMLPath := filepath.Join(ducklabDir, "project.toml")
	if _, err := os.Stat(projectTOMLPath); err == nil {
		// Already exists; open it
		return s.ProjectOpen(ctx, absPath)
	}
	// Create .ducklab
	if err := os.MkdirAll(ducklabDir, 0o755); err != nil {
		return nil, err
	}
	// Git init if needed
	git := vcs.New(absPath)
	if !git.HasGit() {
		if req.GitInit {
			if err := git.Init(); err != nil {
				return nil, fmt.Errorf("git init: %w", err)
			}
		} else {
			return nil, fmt.Errorf("not a git repository; use --git-init to initialize")
		}
	}
	// Create project config
	name := req.Name
	if name == "" {
		name = filepath.Base(absPath)
	}
	id := slugify(filepath.Base(absPath))
	cfg := config.DefaultProject(id, name)
	cfg.Created = time.Now().UTC().Format(time.RFC3339)
	cfg.Describe = req.Describe
	// Auto-detect gate
	gate, gateCmd, err := verify.Detect(absPath)
	if err == nil {
		cfg.Verify.Mode = string(gate)
		if gate == verify.GateTests {
			cfg.Verify.Tests = gateCmd
		} else if gate == verify.GateBuild {
			cfg.Verify.Build = gateCmd
		}
	}
	// Write project.toml
	projectTOML := filepath.Join(ducklabDir, "project.toml")
	if err := writeProjectTOML(projectTOML, cfg); err != nil {
		return nil, err
	}
	// Create DB
	db, err := store.Open(filepath.Join(ducklabDir, "ducklab.db"))
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(); err != nil {
		return nil, err
	}
	if err := db.CreateProject(&store.Project{ID: id, Name: name, CreatedAt: cfg.Created}); err != nil {
		return nil, err
	}
	db.Close()
	// Extend .gitignore
	gitignoreEntries := []string{
		".ducklab/runs/",
		".ducklab/ducklab.db",
		".ducklab/ducklab.db-wal",
		".ducklab/ducklab.db-shm",
		".ducklab/lock",
		".ducklab/app.log",
		// Common junk, seeded at birth. Accept commits the WHOLE tree
		// (git add -A), so a virtualenv created before anyone thought about
		// .gitignore was swept into a task commit — 2,010 files whose
		// recompiled bytecode then dirtied the tree on every test run and
		// blocked every clean-tree guard the engine has.
		".venv/",
		"venv/",
		"__pycache__/",
		"*.pyc",
		".pytest_cache/",
		"node_modules/",
		".DS_Store",
	}
	if err := vcs.EnsureGitignore(absPath, gitignoreEntries); err != nil {
		return nil, err
	}
	if err := vcs.EnsureGitattributes(absPath, []string{
		".ducklab/bugs/audit.jsonl merge=union",
	}); err != nil {
		return nil, err
	}
	// Register
	regID, err := s.registry.Register(absPath, name)
	if err != nil {
		return nil, err
	}
	return &Project{
		ID:       regID,
		Path:     absPath,
		Name:     name,
		Config:   cfg,
		Gate:     string(gate),
		Autonomy: string(cfg.Autonomy),
	}, nil
}

// ProjectList lists projects.
func (s *Service) ProjectList(ctx context.Context) ([]*Project, error) {
	s.registry.MarkMissing()
	entries := s.registry.List()
	var projects []*Project
	for _, e := range entries {
		p := &Project{
			ID:      e.ID,
			Path:    e.Path,
			Name:    e.Name,
			Missing: e.Missing,
		}
		if !e.Missing {
			p.Branch, _ = vcs.New(e.Path).CurrentBranch()
			p.HasCode = projectHasCode(e.Path)
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// projectHasCode reports whether the tree holds committed files beyond the
// harness's own. Committed, not present: an empty repo with a stray editor
// file is still a greenfield, and git already knows the difference.
func projectHasCode(path string) bool {
	for _, f := range vcs.New(path).LsFiles() {
		if f != "" && !strings.HasPrefix(f, ".ducklab/") && f != ".gitignore" {
			return true
		}
	}
	return false
}

// ProjectGet returns a registered project by id.
//
// The GET /v1/projects/{id} handler used to call ProjectOpen with the id,
// which takes a path: the id was resolved relative to the engine's working
// directory and the endpoint answered 404 for every project that existed.
func (s *Service) ProjectGet(ctx context.Context, id string) (*Project, error) {
	entry, err := s.registry.Get(id)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return nil, err
	}
	branch, _ := vcs.New(entry.Path).CurrentBranch()
	return &Project{ID: id, Path: entry.Path, Name: cfg.Name, Branch: branch, Config: cfg, Autonomy: string(cfg.Autonomy)}, nil
}

// ConfigDiagnostics is a read-only snapshot of the local tools used for remote work.
type ConfigDiagnostics struct {
	RemoteReachable  string `json:"remote_reachable"`
	GHAuth           string `json:"gh_auth"`
	CredentialHelper string `json:"credential_helper"`
}

// ConfigDiagnostics checks configured remote tooling without changing project state.
func (s *Service) ConfigDiagnostics(ctx context.Context, id string) (ConfigDiagnostics, error) {
	entry, err := s.registry.Get(id)
	if err != nil {
		return ConfigDiagnostics{}, err
	}
	cfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return ConfigDiagnostics{}, err
	}
	out := ConfigDiagnostics{RemoteReachable: "no named remote configured", GHAuth: "gh is not installed", CredentialHelper: "not configured"}
	if cfg.Remote.Name != "" {
		cmd := exec.CommandContext(ctx, "git", "ls-remote", "--exit-code", cfg.Remote.Name)
		cmd.Dir = entry.Path
		if err := cmd.Run(); err == nil {
			out.RemoteReachable = "reachable"
		} else {
			out.RemoteReachable = "unreachable"
		}
	}
	if _, err := exec.LookPath("gh"); err == nil {
		cmd := exec.CommandContext(ctx, "gh", "auth", "status")
		if err := cmd.Run(); err == nil {
			out.GHAuth = "authenticated"
		} else {
			out.GHAuth = "not authenticated"
		}
	}
	cmd := exec.CommandContext(ctx, "git", "config", "--get", "credential.helper")
	cmd.Dir = entry.Path
	if value, err := cmd.Output(); err == nil && strings.TrimSpace(string(value)) != "" {
		out.CredentialHelper = "configured"
	}
	return out, nil
}

// ConfigDoctor reports deterministic, read-only configuration findings.
func (s *Service) ConfigDoctor(ctx context.Context, id string) ([]config.Finding, error) {
	entry, err := s.registry.Get(id)
	if err != nil {
		return nil, err
	}
	return config.Doctor(entry.Path)
}

// ProjectUpdate applies dotted keys to a project's config and saves it.
//
// Keys are applied to a copy and written only if every one of them is valid,
// so a typo in the second key cannot leave the first half-applied.
func (s *Service) ProjectUpdate(ctx context.Context, id string, keys map[string]string, sources ...string) (*Project, error) {
	entry, err := s.registry.Get(id)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(entry.Path, ".ducklab", "project.toml")
	cfg, err := config.LoadProject(path)
	if err != nil {
		return nil, err
	}
	updated := *cfg
	for _, k := range sortedKeys(keys) {
		if err := config.SetKey(&updated, k, keys[k]); err != nil {
			return nil, err
		}
	}
	if err := config.SaveProject(path, &updated); err != nil {
		return nil, err
	}
	if len(sources) > 0 && sources[0] != "" {
		if receipt, receiptErr := os.OpenFile(filepath.Join(entry.Path, ".ducklab", "config-audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); receiptErr == nil {
			_ = json.NewEncoder(receipt).Encode(map[string]interface{}{"actor": "human", "source": sources[0], "keys": sortedKeys(keys), "ts": time.Now().UTC().Format(time.RFC3339)})
			_ = receipt.Close()
		}
	}
	// The registry carries the display name too, so a rename that only reached
	// project.toml would leave `project list` showing the old one.
	if updated.Name != cfg.Name {
		if err := s.registry.Rename(id, updated.Name); err != nil {
			return nil, err
		}
	}
	// Active projects cache their configuration. Drop that cache so remote
	// actions immediately use settings updated through this API.
	s.projMu.Lock()
	delete(s.projects, id)
	s.projMu.Unlock()
	return s.ProjectOpen(ctx, entry.Path)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ProjectForget unregisters a project. It does not touch the directory.
//
// Until this existed the registry was append-only: a project could be added
// and never removed, so a throwaway repo stayed in every client's list for
// good and the only remedy was hand-editing the daemon's state file.
func (s *Service) ProjectForget(ctx context.Context, id string) error {
	if _, err := s.registry.Get(id); err != nil {
		return err
	}
	// Refused while work is in flight. Forget deliberately leaves the
	// project's files alone — the directory is the person's — but a run
	// mid-flight or paused at a gate becomes an orphan the moment the
	// engine forgets its project, and it greeted the next open as an
	// undecidable proposal card (B-083). Settle the runs first; the
	// refusal names them.
	s.runsMu.RLock()
	var inFlight []string
	for rid, rs := range s.runs {
		switch rs.run.Status {
		case "running", "queued", "paused":
			if rs.run.ProjectID == id {
				inFlight = append(inFlight, rid)
			}
		}
	}
	s.runsMu.RUnlock()
	if len(inFlight) > 0 {
		sort.Strings(inFlight)
		return fmt.Errorf("%s has %d run(s) in flight (%s); decide or abort them first — forgetting now would orphan their gates",
			id, len(inFlight), strings.Join(inFlight, ", "))
	}
	s.projMu.Lock()
	delete(s.projects, id)
	s.projMu.Unlock()
	return s.registry.Unregister(id)
}

// ProjectRecover restores an orphaned accepted commit only after a person explicitly chooses a door.
func (s *Service) ProjectRecover(ctx context.Context, id, action, sha, actor string) (string, error) {
	if sha == "" || actor == "" {
		return "", fmt.Errorf("commit_sha and requester are required")
	}
	entry, err := s.registry.Get(id)
	if err != nil {
		return "", err
	}
	git := vcs.New(entry.Path)
	var landed string
	switch action {
	case "cherry-pick-chain":
		landed, err = git.CherryPick(sha)
	case "restore-as-fresh-commit":
		landed, err = git.RestoreAsFreshCommit(sha)
	default:
		return "", fmt.Errorf("unknown recovery door %q", action)
	}
	if err != nil {
		return "", err
	}
	s.runsMu.RLock()
	var recoveredRuns []*runState
	for _, rs := range s.runs {
		if rs.run.ProjectID == id && rs.run.CommitSHA == sha {
			recoveredRuns = append(recoveredRuns, rs)
		}
	}
	s.runsMu.RUnlock()
	for _, rs := range recoveredRuns {
		w, e := s.ensureWriter(rs)
		if e != nil {
			continue
		}
		_ = w.AppendEvent("recovery", map[string]interface{}{"action": action, "actor": actor, "commit_sha": sha, "landed_sha": landed})
		// The recovered commit is now on a branch. Clear the audit badge and
		// persist it so the recovery door cannot be offered again after reload.
		s.runsMu.Lock()
		rs.run.LocalOnly = false
		s.runsMu.Unlock()
		_ = w.WriteState()
	}
	return landed, nil
}

// auditRemote marks accepted commits absent from remote refs and warns loudly about commits
// no branch (local or remote) retains. Audit faults are warnings, never startup blockers.
func (s *Service) auditRemote(ctx context.Context, projectID, root, remote string) {
	git := vcs.New(root)
	if remote == "" {
		remote = "origin"
	}
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		s.remoteWarning(projectID, "orphan audit failed: "+err.Error(), nil)
		return
	}
	var orphans []string
	for _, run := range runs {
		if !run.Accepted || run.CommitSHA == "" {
			continue
		}
		remoteOK, err := git.RemoteContains(remote, run.CommitSHA)
		if err != nil {
			s.remoteWarning(projectID, "orphan audit failed for "+run.CommitSHA+": "+err.Error(), []string{run.CommitSHA})
			continue
		}
		reachable, err := git.AnyBranchContains(run.CommitSHA)
		if err != nil {
			s.remoteWarning(projectID, "orphan audit failed for "+run.CommitSHA+": "+err.Error(), []string{run.CommitSHA})
			continue
		}
		s.runsMu.Lock()
		rs := s.runs[run.ID]
		if rs != nil && rs.run.LocalOnly != !remoteOK {
			rs.run.LocalOnly = !remoteOK
			if w, e := s.ensureWriter(rs); e == nil {
				_ = w.WriteState()
			}
		}
		s.runsMu.Unlock()
		if !reachable {
			orphans = append(orphans, run.CommitSHA)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		s.remoteWarning(projectID, "ORPHANED ACCEPTED COMMITS: "+strings.Join(orphans, ", "), orphans)
	}
}

func (s *Service) remoteWarning(projectID, message string, shas []string) {
	if s.bus != nil {
		s.bus.Publish(bus.Event{Type: "remote_warning", ProjectID: projectID, TS: s.now(), Data: map[string]interface{}{"warning": message, "commit_shas": shas}})
	}
}

// ProjectStatus returns project status.
func (s *Service) ProjectStatus(ctx context.Context, id string) (*Status, error) {
	entry, err := s.registry.Get(id)
	if err != nil {
		return nil, err
	}
	// Count active runs for this project
	s.runsMu.RLock()
	active := 0
	for _, rs := range s.runs {
		if rs.run.ProjectID == id && rs.run.Status == "running" {
			active++
		}
	}
	s.runsMu.RUnlock()
	// Task counts come from the plan — the authoritative task source that
	// `task list` and the board read — not the db `task` table. That table is
	// a secondary mirror written once at bug promotion (status "todo", never
	// updated) and never pruned when a re-plan drops a task, so counting it
	// reported phantom tasks (a re-planned-out task lingered) and frozen
	// statuses (an accepted task still counted "todo"). Status then disagreed
	// with the very board it summarises. deriveTaskRunState, inside TaskList,
	// gives each plan task the same status the board shows.
	views, err := s.TaskList(ctx, id)
	if err != nil {
		return &Status{StageProgress: stageProgress(entry.Path), ActiveRuns: active, Provenance: build.Provenance()}, nil
	}
	taskCounts := make(map[string]int)
	for _, tv := range views {
		taskCounts[tv.Status]++
	}
	accepted, branches, err := s.acceptedUnreleased(ctx, id, entry.Path, views)
	if err != nil {
		return nil, err
	}
	st := &Status{StageProgress: stageProgress(entry.Path), TaskCounts: taskCounts, ActiveRuns: active,
		AcceptedUnreleased: accepted, UnreleasedBranches: branches, Provenance: build.Provenance()}
	cfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err == nil {
		branch, berr := vcs.New(entry.Path).CurrentBranch()
		if berr == nil {
			st.Ahead, st.Behind, _ = vcs.New(entry.Path).AheadBehind(cfg.Remote.Name, branch)
		}
	}
	return st, nil
}

// acceptedUnreleased counts accepted task commits not included in the latest
// release tag. Branch names are provenance only: they survive both merging and
// deletion, so cannot define whether work has shipped.
func (s *Service) acceptedUnreleased(ctx context.Context, projectID, root string, views []TaskView) (int, int, error) {
	git := vcs.New(root)
	var latest release.Version
	hasRelease := false
	if git.HasGit() {
		tags, err := git.Tags()
		if err != nil {
			return 0, 0, err
		}
		latest, hasRelease = release.Latest(tags)
	}

	current := map[string]TaskView{}
	for _, view := range views {
		if view.Status == "accepted" {
			current[view.ID] = view
		}
	}
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return 0, 0, err
	}
	seen := map[string]bool{}
	branches := map[string]bool{}
	count := 0
	for _, run := range runs { // newest first: a reaccepted task has one current commit
		view, wanted := current[run.TaskID]
		if !wanted || seen[run.TaskID] || !run.Accepted || run.CommitSHA == "" {
			continue
		}
		seen[run.TaskID] = true
		if hasRelease {
			shipped, err := git.IsAncestor(run.CommitSHA, latest.String())
			if err != nil {
				return 0, 0, err
			}
			if shipped {
				continue
			}
		}
		count++
		if view.Branch != "" && view.Branch != "main" {
			branches[view.Branch] = true
		}
	}
	return count, len(branches), nil
}

// stageProgress reports where each artifact stage stands. It was never
// populated, so `stage_progress` came back null and no client could show the
// one thing the cycle view exists to show.
func stageProgress(root string) map[string]string {
	out := map[string]string{}
	for stage, kind := range map[string]artifact.Kind{
		"intake": artifact.KindRequirements,
		"spec":   artifact.KindSpec,
		"plan":   artifact.KindPlan,
	} {
		doc, err := artifact.Load(root, kind)
		switch {
		case err != nil:
			out[stage] = "unknown"
		case doc == nil || len(doc.Sections) == 0:
			if prop, _ := artifact.LoadProposed(root, kind); prop != nil && len(prop.Sections) > 0 {
				out[stage] = "proposed"
			} else {
				out[stage] = "empty"
			}
		default:
			out[stage] = "approved"
		}
	}
	return out
}

// DucklingList lists ducklings.
func (s *Service) DucklingList(ctx context.Context) ([]*duckling.Duckling, error) {
	return s.ducklings.List(), nil
}

// DucklingProbe probes a duckling.
func (s *Service) DucklingProbe(ctx context.Context, id string) (*duckling.Capabilities, error) {
	return s.ducklings.Probe(ctx, config.DucklingID(id))
}

// DucklingTest tests a duckling.
func (s *Service) DucklingTest(ctx context.Context, id, prompt string, stream bool) (string, int, int, float64, error) {
	return s.ducklings.Test(ctx, config.DucklingID(id), prompt, stream)
}

// RunRequest is a run request.
type RunRequest struct {
	TaskID    string   `json:"task_id"`
	Mode      string   `json:"mode"`
	Ducklings []string `json:"ducklings"`
	// Seats names per-run role overrides. An empty value leaves that role to the
	// resolved roster; Ducklings remains the legacy positional/list contract.
	Seats    map[string]string `json:"seats,omitempty"`
	Rounds   int               `json:"rounds"`
	Verify   string            `json:"verify"`
	Budget   *budget.Budget    `json:"budget"`
	Autonomy string            `json:"autonomy"`
	// Origin marks a run started by the autopilot rather than a person.
	Origin string `json:"origin,omitempty"`
	// Redo is the explicit consent to start fresh work on a task that was
	// already accepted. Without it the engine refuses: T-001 was relaunched
	// by an overnight operator that had no idea the task was finished, and
	// the redundant runs this pattern leaves behind (T-101, T-102) are what
	// every stale-failure cleanup in this file exists to mop up.
	Redo bool `json:"redo,omitempty"`
	// NoStream turns off token streaming for this run. The default is to
	// stream, because the desktop exists to watch a run happen.
	NoStream     bool `json:"no_stream"`
	DryRun       bool `json:"dry_run"`
	Parallel     bool `json:"parallel"`
	UnsafeWrites bool `json:"unsafe_writes"`
	// AgentTurns overrides how many model calls ONE reply may chain, for
	// every role in this run. The budget's turn cap bounds the CONVERSATION;
	// this bounds the loop inside one turn — and a hard task can need more
	// looking than the default allows. Zero keeps the configured caps;
	// negative lifts the cap for this run, with the token and cost budgets
	// still guarding every call.
	AgentTurns int `json:"agent_turns,omitempty"`
	// Note rides the prompt as a section from the human — the channel for
	// "address the reviewer's findings" and every other instruction a task
	// body cannot carry because it was written before the history happened.
	Note string `json:"note,omitempty"`
	// chained is set only by continueChain, never by a client: this build is
	// the second half of a TDD chain and jumps the project's waiting line, so
	// the chain runs as the one unit the person authorized.
	chained bool
	// chainBase is the accepted red test on the preceding run branch.
	chainBase string
	// resumed is set only by RunResume: the run keeps its recorded ceilings
	// (including lifted ones) and its ledger continues from what it spent.
	resumed bool
}

// RunDetail is a run detail.
type RunDetail struct {
	Run          *runlog.Run     `json:"run"`
	Events       []*runlog.Event `json:"events,omitempty"`
	LandingOffer *LandingOffer   `json:"landing_offer,omitempty"`
}

type LandingOffer struct {
	CommitSHA string `json:"commit_sha"`
	Evidence  string `json:"evidence"`
}

// AcceptResult is the result of accepting a run.
type AcceptResult struct {
	CommitSHA string `json:"commit_sha"`
	Warning   string `json:"warning,omitempty"`
}

// RunFilter is a run filter.
type RunFilter struct {
	ProjectID string `json:"project_id"`
	Status    string `json:"status"`
}

// checkRunnable reports why a project cannot host a run, in words that say
// what to do about it.
func checkRunnable(path string) error {
	git := vcs.New(path)
	if !git.HasGit() {
		return fmt.Errorf("%s is not a git repository; run `ducklab project init --git-init` there", path)
	}
	if _, err := git.HeadSHA(); err != nil {
		return fmt.Errorf("%s has no commits yet, so there is nothing to branch from or diff against; make one first", path)
	}
	return nil
}

// resolveBuildMode is the one omitted-mode rule shared by every launch surface.
// Settings win, then the project's learned phase habit, then solo.
func (s *Service) resolveBuildMode(projectPath string) (string, string) {
	s.cfgMu.RLock()
	settings := s.cfg.Defaults.BuildMode
	s.cfgMu.RUnlock()
	if settings != "" {
		return settings, "settings"
	}
	if projCfg, err := config.LoadProject(filepath.Join(projectPath, ".ducklab", "project.toml")); err == nil {
		if mode := projCfg.Modes[config.StageBuild]; mode != "" {
			return string(mode), "project"
		}
	}
	return "solo", "fallback"
}

// RunStart starts a run. Returns immediately with the run in running status.
func (s *Service) RunStart(ctx context.Context, projectID string, req RunRequest) (*runlog.Run, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	// The explicit relaunch is the decision on a FAILED gate for this task;
	// without this the new run queued forever behind it.
	s.settleFailedGateForRetry(ctx, projectID, req.TaskID)
	// Checked before any model is asked for anything.
	//
	// A split run spent its architect's whole turn producing a good
	// decomposition and then died inside phase 3 on a raw "fatal: not a git
	// repository". Every mode needs a HEAD — to branch from, to diff against,
	// to build a worktree on — so a project without one cannot run at all, and
	// finding that out first costs nothing.
	if err := checkRunnable(entry.Path); err != nil {
		return nil, err
	}
	// The task must exist somewhere the prompt can be built from. A run
	// against a ghost id used to start fine and hand the implementer a prompt
	// of one line — "Implement task T-048" — with no title, no body and no
	// bug report, which it spent twenty turns trying to divine from the tree.
	// The relaunch panel on an old run offers exactly this trap: its task can
	// have been removed since.
	if req.TaskID != "" {
		tv := s.findTask(ctx, projectID, req.TaskID)
		if tv == nil {
			return nil, fmt.Errorf("no task %s in this project — it may have been removed; "+
				"pick one from the board", req.TaskID)
		}
		// The engine holds its own door. The board hides the Run button on a
		// dependency-blocked task, but the CLI, an old client, or a relaunch
		// panel pointing at yesterday's state can still ask — and for a long
		// time asking worked: T-023 ran and got ACCEPTED while T-022, which
		// it depended on, had never passed. The dependency check the plan
		// declared was display only.
		if tv.Status == "blocked" && !slices.Contains(tv.Next, "run") {
			return nil, fmt.Errorf("%s is not startable: %s", req.TaskID, tv.Blocked)
		}
		// Same door, other direction: a task that is DONE. The board hides
		// its buttons, but an operator working from memory — or an agent
		// working from a stale listing — can still ask, and what it gets is
		// fresh work against something committed, then an abort, then a
		// stale failure haunting two views.
		if tv.Status == "accepted" && !req.Redo {
			return nil, fmt.Errorf("%s is already accepted; its work is committed. "+
				"A new run would redo finished work — pass redo (and say why in a note) "+
				"if that is truly the intent", req.TaskID)
		}
	}

	// Create run
	runID := runlog.GenerateRunID()
	run := &runlog.Run{
		ID:           runID,
		ProjectID:    projectID,
		Stage:        "build",
		Mode:         req.Mode,
		ModeSource:   "request",
		TaskID:       req.TaskID,
		TaskBodyHash: taskBodyHashForTask(ctx, s, projectID, req.TaskID),
		Status:       "running",
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
		// Streaming on unless a caller opts out.
		//
		// It used to be off unless a client asked, and no client ever asked —
		// so onDelta was never installed, chatMaybeStreaming always took the
		// non-streaming branch, and not one token_delta was published in the
		// project's history. The whole streaming path, batcher and delta store
		// included, was dead code guarded by a flag nobody set.
		//
		// Defaulting it on is safe: streaming is display-only (01 §5.2), the
		// contract and tool dispatch always read the assembled response, and
		// an endpoint that cannot stream falls back and emits the finished
		// text as a single delta.
		Stream:       !req.NoStream,
		DryRun:       req.DryRun,
		UnsafeWrites: req.UnsafeWrites,
		Autonomy:     req.Autonomy,
		Origin:       req.Origin,
		Note:         req.Note,
		AgentTurns:   req.AgentTurns,
	}
	if run.Mode == "" {
		run.Mode, run.ModeSource = s.resolveBuildMode(entry.Path)
	}
	// Validate cardinality at the launch boundary as well as when defaults are edited.
	if run.Mode == "council" || run.Mode == "split" || run.Mode == "tournament" {
		ids := req.Ducklings
		if len(ids) == 0 {
			s.cfgMu.RLock()
			seats := s.cfg.Defaults.ModeSeats[run.Mode]
			role := "reviewer"
			if run.Mode == "split" || run.Mode == "tournament" {
				role = "implementer"
			}
			ids = seats[role]
			s.cfgMu.RUnlock()
		}
		n := len(ids)
		if run.Mode == "council" && n < 2 {
			return nil, fmt.Errorf("council requires at least one critic")
		}
		if (run.Mode == "split" || run.Mode == "tournament") && n < 2 {
			return nil, fmt.Errorf("%s requires at least two %s", run.Mode, map[string]string{"split": "workers", "tournament": "contestants"}[run.Mode])
		}
	}
	if run.Autonomy == "" {
		// The project's configured autonomy is the default the plan promised
		// and RunStart never read: a project.toml saying autonomy = "auto"
		// launched guarded runs regardless.
		if projCfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml")); err == nil {
			run.Autonomy = string(projCfg.Autonomy)
		}
	}
	if run.Autonomy == "" {
		run.Autonomy = "guarded"
	}

	if err := s.createRunWorktreeAt(run, entry.Path, req.chainBase); err != nil {
		return nil, err
	}

	// Create writer
	writer, err := runlog.NewWriter(entry.Path, run)
	if err != nil {
		if cleanupErr := vcs.New(entry.Path).WorktreeRemove(run.WorktreePath); cleanupErr != nil {
			return nil, fmt.Errorf("create run writer: %w (also could not remove worktree %s: %v)", err, run.WorktreePath, cleanupErr)
		}
		return nil, err
	}

	// Register run state
	ctx, cancel := context.WithCancel(context.Background())
	rs := &runState{
		run:         run,
		writer:      writer,
		runDir:      writer.RunDir(),
		projectPath: entry.Path,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	// Every persisted event reaches the bus through this hook, carrying its
	// seq. Without it the live SSE stream is nearly empty and subscribers
	// cannot deduplicate against a replayed backlog.
	s.attachWriter(rs, writer)
	s.runsMu.Lock()
	s.runs[runID] = rs
	s.runsMu.Unlock()

	// Emit run_start event
	writer.AppendEvent("run_start", map[string]interface{}{
		"mode": run.Mode, "mode_source": run.ModeSource, "task_id": run.TaskID,
	})

	// Dry-run is synchronous: render prompts, no model calls, exit immediately
	if req.DryRun {
		s.executeDryRun(rs, entry, req)
		return run, nil
	}

	// Submit to the queue: it starts the run now, or marks it queued and
	// starts it when a slot frees (AC-25).
	s.queue.submit(s, &queued{
		rs: rs, ctx: ctx, chained: req.chained,
		exec: func(c context.Context) { s.executeRun(c, rs, entry, req) },
	})

	return run, nil
}

// projectHeld reports whether the project belongs to work the queue is no
// longer counting, and why — empty means free to start a run for taskID.
//
// Two holds exist. A run paused at its gate has released its slot — its
// goroutine returned — but its uncommitted diff sits in the tree, and
// acceptance commits the whole tree (git add -A): a run started in that
// window would hand its files to whichever gate resolved first. And a broken
// chain — a committed test whose build failed or never came — leaves the
// suite deliberately red, so any other task's test-first lands UNVERIFIED
// and any other build's gate fails on a test that is not its own. The task
// with the outstanding test is exempt: building it is one cure, retiring the
// test the other, and both must be able to run.
//
// Document stages keep their drafts in proposal files, not the tree, so only
// tree-writing stages hold the project.
// settleFailedGateForRetry closes a FAILED run paused at its gate for the
// same task a person is explicitly relaunching.
//
// "Retry with this note" started the new run and left the old one pausing
// the project: the retry sat queued behind a gate that would never move
// without a second human action nobody knew they owed (T-074 — the rail
// showed an empty circle and the note went nowhere). A FAILED verdict has
// nothing to accept — reject is its only door — and the click that
// relaunches the task IS that decision. PASSED and UNVERIFIED gates are
// not touched: work someone might accept is never discarded on a side
// effect.
func (s *Service) settleFailedGateForRetry(ctx context.Context, projectID, taskID string) {
	if taskID == "" {
		return
	}
	s.runsMu.RLock()
	var ids []string
	for id, rs := range s.runs {
		r := rs.run
		if r.ProjectID == projectID && r.TaskID == taskID && r.Status == "paused" &&
			r.PendingKind == "gate" && r.Verdict == "FAILED" {
			ids = append(ids, id)
		}
	}
	s.runsMu.RUnlock()
	for _, id := range ids {
		if err := s.RunReject(ctx, id, "superseded: the task was explicitly retried"); err != nil {
			s.runsMu.RLock()
			if rs := s.runs[id]; rs != nil && rs.writer != nil {
				rs.writer.AppendEvent("warning", map[string]interface{}{
					"detail": "the failed run could not be closed for the retry: " + err.Error(),
				})
			}
			s.runsMu.RUnlock()
		}
	}
}

func (s *Service) projectHeld(projectID, taskID string) string {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()
	tested := map[string]bool{}
	built := map[string]bool{}
	for _, rs := range s.runs {
		r := rs.run
		if r.ProjectID != projectID {
			continue
		}
		// Build and test runs retain private worktrees while awaiting a decision;
		// only a document-stage pause holds the person's checkout.
		if r.Status == "paused" && (r.Stage == "document" || (r.WorktreePath == "" && (r.Stage == "build" || r.Stage == "test"))) {
			return "another run holds this project's working tree"
		}
		if r.Accepted && r.TaskID != "" {
			switch r.Stage {
			case "test":
				if r.RevertSHA == "" {
					tested[r.TaskID] = true
				}
			case "build":
				built[r.TaskID] = true
			}
		}
	}
	// A run for a broken-chain task is always a cure, never a victim.
	if tested[taskID] && !built[taskID] {
		return ""
	}
	for task := range tested {
		if !built[task] {
			return fmt.Sprintf("%s's committed test is red until its build lands — build it or retire the test", task)
		}
	}
	return ""
}

// QueueStats reports the run queue's live counters — the numbers that
// explain a run sitting in "queued": how many slots are taken, how many wait,
// against what limit. Surfaced in /v1/health because the one time they were
// needed they were invisible, and the diagnosis ran through disk archaeology.
func (s *Service) QueueStats() (running, waiting, limit int) {
	return s.queue.stats()
}

// executeDryRun renders prompts without calling any model. Synchronous.
func (s *Service) executeDryRun(rs *runState, entry *registry.ProjectEntry, req RunRequest) {
	defer rs.writer.Close()
	defer s.cleanupRunWorktree(rs, entry.Path)
	defer close(rs.done)

	// Load project config
	ducklabDir := filepath.Join(entry.Path, ".ducklab")
	projCfg, err := config.LoadProject(filepath.Join(ducklabDir, "project.toml"))
	if err != nil {
		s.failRun(rs, fmt.Errorf("load project config: %w", err))
		return
	}

	// Resolve duckling for dialect determination
	ducklingID := projCfg.Roster[config.RoleImplementer]
	if ducklingID == "" {
		for id := range s.cfg.Ducklings {
			ducklingID = id
			break
		}
	}
	useNative := false
	if d, err := s.ducklings.Get(ducklingID); err == nil && d.Caps.NativeTools {
		useNative = true
	}

	// Build exec context
	root := runRoot(rs.run, entry.Path)
	ectx := &tools.ExecContext{
		ProjectRoot:  root,
		RunID:        rs.run.ID,
		Autonomy:     config.Autonomy(rs.run.Autonomy),
		UnsafeWrites: rs.run.UnsafeWrites,
		ShellPolicy:  projCfg.Shell,
		Verify:       projCfg.Verify,
		Answers:      rs.answers(),
		// A project skill shadows a global one of the same name (05 §7).
		GlobalSkillsDir: globalSkillsDir(),
	}
	rs.execCtx = ectx

	// Build the turn that would be sent
	turn := &agent.Turn{
		Role:     config.RoleImplementer,
		Prompt:   fmt.Sprintf("Implement task %s", req.TaskID),
		Contract: "edits",
	}

	messages := agent.BuildMessages(turn, ectx, useNative)

	// Write prompts to a file for inspection
	promptsPath := filepath.Join(rs.writer.RunDir(), "prompts.json")
	promptsData := map[string]interface{}{
		"turn":     0,
		"role":     "implementer",
		"messages": messages,
	}
	if data, err := json.MarshalIndent(promptsData, "", "  "); err == nil {
		os.WriteFile(promptsPath, data, 0o644)
	}

	rs.run.Status = "done"
	rs.run.Verdict = "UNVERIFIED"
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	rs.writer.AppendEvent("run_end", map[string]interface{}{"verdict": "UNVERIFIED", "dry_run": true})
	rs.writer.WriteState()
}

// runLogAdapter adapts runlog.Writer to agent.RunLogWriter.
type runLogAdapter struct {
	w *runlog.Writer
	// run is here so a call with no usage can mark the whole run's totals as
	// estimated. Reports must not sum measured and estimated numbers without
	// saying so (AC-61), and this is the only place that sees both.
	run *runlog.Run
	// mu guards the run fields updated below. It is the run state's wmu, not
	// the adapter's own: split runs its subtasks concurrently on one adapter,
	// so two goroutines could write the spend map at once — and the API
	// goroutine serialises the same map whenever a client fetches the run. A
	// private lock covered the first hazard and left the second one a
	// concurrent map read-and-write crash waiting for a fetch mid-call.
	mu *sync.Mutex
	// onSpend, if set, runs after each call is attributed. Without it the
	// budget meter sat at zero for the whole run and jumped to the final number
	// at the end, which is exactly when knowing is no longer useful.
	onSpend func()
}

func (a *runLogAdapter) AppendLLM(call *agent.LLMCallRecord) error {
	err := a.w.AppendLLM(&runlog.LLMCall{
		Duckling:     call.Duckling,
		Provider:     call.Provider,
		Upstream:     call.Upstream,
		Model:        call.Model,
		Role:         call.Role,
		Request:      call.Request,
		Response:     call.Response,
		Usage:        call.Usage,
		CostUSD:      call.CostUSD,
		LatencyMs:    call.LatencyMs,
		Attempt:      call.Attempt,
		Estimated:    call.Estimated,
		CostSource:   call.CostSource,
		FinishReason: call.FinishReason,
	})
	// One estimated call makes the run's total an estimate. Reports mark it;
	// without this the marker in Render was unreachable and every number
	// looked measured.
	if a.run != nil {
		a.mu.Lock()
		if call.Estimated {
			a.run.TokensEstimated = true
		}
		// Attributed here because this is the only place that sees which
		// duckling made a call and what it cost, together.
		if a.run.Spend == nil {
			a.run.Spend = map[string]runlog.DucklingSpend{}
		}
		d := a.run.Spend[call.Duckling]
		d.Calls++
		d.Tokens += callTokens(call.Usage)
		d.CostUSD += call.CostUSD
		if call.Estimated {
			d.Estimated = true
		}
		a.run.Spend[call.Duckling] = d
		a.mu.Unlock()
		if a.onSpend != nil {
			a.onSpend()
		}
	}
	return err
}

// callTokens reads one call's total from whatever the provider reported.
//
// Providers disagree about the names. A missing key means a call whose tokens
// nobody counted, which is different from a call that used none — but the
// difference is already carried by Estimated, so this returns what it can.
func callTokens(usage map[string]interface{}) int64 {
	num := func(keys ...string) int64 {
		for _, k := range keys {
			switch v := usage[k].(type) {
			case float64:
				return int64(v)
			case int64:
				return v
			case int:
				return int64(v)
			}
		}
		return 0
	}
	if total := num("total_tokens", "total"); total > 0 {
		return total
	}
	return num("prompt_tokens", "input_tokens") + num("completion_tokens", "output_tokens")
}

// executeRun executes a run in the background.
func verifyOverride(cfg config.Verify, command string) config.Verify {
	cfg.Mode = string(verify.GateCustom)
	cfg.Custom = command
	return cfg
}

func (s *Service) executeRun(ctx context.Context, rs *runState, entry *registry.ProjectEntry, req RunRequest) {
	defer rs.writer.Close()
	// A worktree remains available at a human gate: acceptance must commit and
	// prove its isolated changes, and a rebase conflict is resolved there by
	// hand. Accept and reject remove it after recording their terminal decision.
	defer close(rs.done)
	defer recoverRun(rs)

	// Load project config
	ducklabDir := filepath.Join(entry.Path, ".ducklab")
	projCfg, err := config.LoadProject(filepath.Join(ducklabDir, "project.toml"))
	if err != nil {
		s.failRun(rs, fmt.Errorf("load project config: %w", err))
		return
	}
	if req.Verify != "" {
		// A task may select a narrower gate (for example Vitest for a UI
		// task) without changing the project's durable verification contract.
		projCfg.Verify = verifyOverride(projCfg.Verify, req.Verify)
	}

	// The tree as it stands, before the run touches it. What "reject" and
	// "failed" restore; without it they were words on a record while the
	// half-made edits stayed in the tree for the next attempt to trip over.
	// Only ever taken ONCE: a resumed run re-enters here, and re-snapshotting
	// would move the restore point to mid-run — reject would then keep the
	// half-made edits it exists to remove.
	if git := vcs.New(runRoot(rs.run, entry.Path)); rs.run.TreeSnapshot == "" && git.HasGit() {
		if snap, serr := git.SnapshotTree(); serr == nil {
			rs.run.TreeSnapshot = snap
			if head, herr := git.HeadSHA(); herr == nil {
				rs.run.TreeSnapshotHead = head
			} else {
				rs.writer.AppendEvent("warning", map[string]interface{}{
					"detail": "could not record HEAD with the tree snapshot; cleanup will refuse unless the snapshot matches HEAD: " + herr.Error(),
				})
			}
			rs.writer.WriteState()
		} else {
			rs.writer.AppendEvent("warning", map[string]interface{}{
				"detail": "could not snapshot the tree; a failure will leave its edits behind: " + serr.Error(),
			})
		}
	}

	// Budget: the defaults, with any limit the request named taking over.
	//
	// This used to be all-or-nothing — a request carrying a budget replaced the
	// whole thing — so a client raising only the token ceiling set the other
	// three limits to zero, and the tracker reads zero as a ceiling of zero. The
	// run would fail before its first call, which is the opposite of what asking
	// for more budget means.
	b := mergeBudget(projectBudget(budget.Budget{
		MaxUSD: s.cfg.Defaults.Budget.MaxUSD, MaxTokens: int64(s.cfg.Defaults.Budget.MaxTokens),
		MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS, MaxTurns: s.cfg.Defaults.Budget.MaxTurns,
	}, projCfg.Budget), req.Budget)
	tracker := budget.NewTracker(&b)
	if req.resumed {
		// A resumed run continues its own life, not a fresh one. Its ceilings
		// are the ones recorded on it — including any a person lifted while it
		// was paused — and its ledger continues from what it already spent: a
		// tracker reborn at zero would have made "resume" a way to double
		// every budget, and the record would undercount the run's true cost.
		b = budget.Budget{
			MaxUSD: rs.run.Budget.Limit.USD, MaxTokens: rs.run.Budget.Limit.Tokens,
			MaxTurns: rs.run.Budget.Limit.Turns, MaxWallclockS: rs.run.Budget.Limit.WallclockS,
		}
		tracker = budget.NewTracker(&b)
		tracker.Spend.AddTokens(rs.run.Budget.Tokens)
		tracker.Spend.AddUSD(rs.run.Budget.USD)
		for i := 0; i < rs.run.Budget.Turns; i++ {
			tracker.Spend.AddTurn()
		}
	}
	rs.setTracker(tracker)
	recordLimits(rs, &b)

	root := runRoot(rs.run, entry.Path)
	ectx := &tools.ExecContext{
		ProjectRoot:  root,
		RunID:        rs.run.ID,
		ProjectID:    rs.run.ProjectID,
		Autonomy:     config.Autonomy(rs.run.Autonomy),
		UnsafeWrites: rs.run.UnsafeWrites,
		ShellPolicy:  projCfg.Shell,
		Verify:       projCfg.Verify,
		Answers:      rs.answers(),
		// A project skill shadows a global one of the same name (05 §7).
		GlobalSkillsDir: globalSkillsDir(),
	}
	rs.execCtx = ectx
	rs.run.ExecutionRoot = root
	rs.writer.WriteState()

	// Tool-level brakes notify the operator. Governance refusals are also kept
	// on the run: a rejected project-settings edit is itself gate-relevant.
	ectx.OnDistress = func(reason string, data map[string]interface{}) {
		payload := map[string]interface{}{"reason": reason}
		for key, value := range data {
			payload[key] = value
		}
		if reason == "governance_write_refused" {
			rs.writer.AppendEvent(reason, payload)
		}
		s.publishTransition(rs, "distress", payload)
	}
	// The team, readable by the models that advise about it: a consultant
	// reconstructed the roster from run history because project.toml's
	// seats were empty — the resolver's answer was one closure away.
	ectx.OnRosterRead = func(ctx context.Context) (string, error) {
		return s.rosterSummary(ctx, rs.run.ProjectID)
	}

	// The rubber duck on demand: an implementer's ask_advisor reaches the
	// advisor seat without pausing the run.
	s.wireAdvisor(rs, ectx)

	// The line-up this run will use: what it asked for, else the one configured
	// for its mode. Filled onto the request itself so every consumer sees it —
	// tournament and split read req.Ducklings directly, and a preference that
	// only reached solo and pair would be a preference that worked in two modes
	// out of four.
	req.Ducklings = s.ducklingsFor(rs.run.Mode, req.Ducklings)

	roster, rosterWarning := s.resolveRoster(projCfg, rs.run.Mode)
	// Only tournament and split read req.Ducklings, so a person who picked a
	// duckling for a solo run got the project roster's implementer instead and
	// had no way to tell: the picker sits right there in the desktop offering a
	// choice that did nothing. Applied before the roster is recorded, so the
	// run says who actually ran.
	// Role-keyed seats supersede the legacy positional lineup. In particular,
	// pair's legacy index 1 is reviewer while the role-keyed lineup's index 1
	// is advisor; applying both would silently seat the advisor as reviewer.
	if len(req.Seats) > 0 {
		assignChosenSeats(roster, req.Seats)
	} else {
		assignChosenDucklings(roster, rs.run.Mode, req.Ducklings)
	}
	// Recomputed, not reused: a pick can put one duckling on both sides of a
	// pair, and the warning resolveRoster produced was decided against the
	// roster before the pick. It can also separate two that were the same, in
	// which case the warning must go.
	rosterWarning = bothSidesWarning(roster)
	// Required seats must be SEATED, by someone: a launch with nobody in the
	// implementer's chair is refused with the seat named, never staffed by
	// the alphabet (B-063).
	if err := unseatedRequired(rs.run.Mode, roster); err != nil {
		s.failRun(rs, err)
		return
	}
	rs.run.Roster = rosterStrings(roster)
	rs.run.RosterSources = s.rosterSources(projCfg, rs.run.Mode, req.Ducklings, req.Seats)
	s.emitLaunchEscalation(rs)
	if rosterWarning != "" {
		// Recorded, not fatal: running both sides on one duckling is a
		// legitimate experiment, but reports must be able to segment it.
		rs.run.Warning = rosterWarning
		rs.writer.AppendEvent("warning", map[string]interface{}{"detail": rosterWarning})
	}
	cache := &loopCache{
		svc: s, tracker: tracker,
		writer:  s.llmWriter(rs, tracker),
		capLift: rs.capLifted.Load,
		loops:   map[config.DucklingID]*agent.Loop{},
	}
	s.attachStreaming(rs, cache)
	// Sample independently of model-call accounting: a provider call may
	// remain in flight for minutes without producing a spend callback.
	go s.monitorWallclockEscalation(ctx, rs)

	dispatchErr := s.dispatchMode(ctx, &modeContext{
		entry: entry, projCfg: projCfg, rs: rs, ectx: ectx,
		cache: cache, roster: roster, req: req,
	})
	// Persist what the run actually spent. Without this every report shows
	// zero tokens and zero cost, and "measurable, or it didn't happen" (P9)
	// becomes a slogan.
	recordSpend(rs, tracker)
	// One last frame for anyone watching live: the streamed budget events can
	// predate the final turn's accounting, and a run that pauses right after
	// left the meter a turn behind its own record.
	s.publishSpend(rs, tracker)
	if dispatchErr != nil {
		// A pause is not a failure: the run is waiting for a person, and
		// waiting indefinitely is correct behaviour (01 §7.1).
		var pending *pendingErr
		if errors.As(dispatchErr, &pending) {
			s.pauseForQuestion(rs, pending.q)
			return
		}
		// The rubber duck said stop: not converging, reseat before spending
		// more. The record names who stopped it and what to change; failRun
		// keeps the work in place and the redo note carries the reshuffle.
		if stop, ok := strategy.StoppedByAdvisor(dispatchErr); ok {
			rs.run.Resolution = "stopped by advisor " + string(stop.Advisor)
			rs.writer.AppendEvent("advisor_stop", map[string]interface{}{
				"advisor": string(stop.Advisor), "reason": stop.Reason, "reshuffle": stop.Reshuffle,
			})
			s.publishTransition(rs, "advisor_stop", map[string]interface{}{
				"advisor": string(stop.Advisor), "reason": stop.Reason, "reshuffle": stop.Reshuffle,
			})
		}
		s.failRun(rs, dispatchErr)
		return
	}

	// Run the gate — announced first. The final verify ran 27 unannounced
	// seconds after the last round gate, then the green, the committing and
	// the reproduction all landed in one 23ms frame: the person read the
	// silence as the commit being slow and the commit as pre-finished, when
	// the mute stretch was THIS verify (the timestamps of r-...-w54v are the
	// evidence). Every other verify announces itself; the verdict's own
	// could not stay the exception.
	rs.writer.AppendEvent("gate_started", map[string]interface{}{
		"phase":  "final",
		"detail": "running the full gate — the verdict is its exit code",
	})
	gateRoot := ectx.ProjectRoot
	rs.gateRoot = gateRoot
	rs.run.GateRoot = gateRoot
	rs.writer.WriteState()
	gateResult, err := verify.Run(ctx, gateRoot, projCfg.Verify, verify.Identity{RunID: rs.run.ID, ProjectID: rs.run.ProjectID})
	if err != nil {
		s.failRun(rs, fmt.Errorf("verify: %w", err))
		return
	}
	rs.writer.WriteVerify(gateResult.Output)
	// Rendering is optional evidence and a failure is only a caveat.
	render := projCfg.Render
	if projCfg.RenderConfigured {
		if render.Command == "" {
			render.Command = projCfg.Run.Command
		}
		if render.Ready == "" {
			render.Ready = projCfg.Run.Health
		}
	}
	if projCfg.RenderConfigured && render.Command != "" {
		captures, renderErr := captureRender(ctx, ectx.ProjectRoot, render, rs.writer, rs.run.ID, rs.run.ProjectID)
		if len(captures) > 0 {
			rs.run.Captures = captures
			event := map[string]interface{}{"ok": true, "captures": captures}
			if renderErr != nil {
				event["note"] = "captures attached despite dirty render exit: " + renderErr.Error()
			}
			rs.writer.AppendEvent("render", event)
		} else if renderErr != nil {
			rs.run.Warning = "render failed: " + renderErr.Error()
			rs.writer.AppendEvent("render", map[string]interface{}{"ok": false, "reason": renderErr.Error()})
		}
	}
	// Persist render attachments and caveats before the gate state is exposed.
	rs.writer.WriteState()
	// The output rides the event, bounded: a FAILED run whose gate event
	// said only exit:1 sent the person re-running the whole suite by hand
	// to learn which test broke (B-122).
	rs.writer.AppendEvent("gate", map[string]interface{}{
		"gate":       string(gateResult.Gate),
		"command":    gateResult.Command,
		"exit_code":  gateResult.ExitCode,
		"output":     tailOf(gateResult.Output, 4000),
		"duration_s": gateResult.Duration,
	})
	if gateResult.ExitCode != 0 {
		rs.run.Failure = "gate failed (exit " + strconv.Itoa(gateResult.ExitCode) + "):\n" + tailOf(gateResult.Output, 1500)
	}

	// A run with no gate ends UNVERIFIED, which is honest and easy to miss.
	// Said here, once, with the fix — rather than leaving someone to wonder on
	// the third run why nothing ever passes.
	if advice := gateAdvice(entry.Path, projCfg.Verify); advice != "" {
		if rs.run.Warning != "" {
			rs.run.Warning += "; " + advice
		} else {
			rs.run.Warning = advice
		}
		rs.writer.AppendEvent("warning", map[string]interface{}{"detail": advice})
	}

	// Compute verdict
	verdict := verify.Verdict(gateResult)
	rs.run.Verdict = verdict
	rs.writer.AppendEvent("verdict", map[string]interface{}{"verdict": verdict})

	// Get diff
	git := vcs.New(ectx.ProjectRoot)
	diff, _ := git.DiffExcluding(rs.run.LinkedDeps...)
	rs.writer.WriteDiff(diff)
	if s.afterRunDiff != nil {
		s.afterRunDiff(rs)
	}
	governanceCallouts := governanceCallouts(diff)
	if len(governanceCallouts) > 0 {
		rs.run.GovernanceModified = true
	}
	rs.run.DesktopStale = desktopStale(diff)

	// A run that touched nothing is a distinct outcome, and every mode used to
	// invent its own: pair recorded PASSED, tournament died applying an empty
	// patch. Recorded here, once, where the diff is already in hand.
	if strings.TrimSpace(diff) == "" {
		rs.run.NoChanges = true
		rs.writer.AppendEvent("no_changes", map[string]interface{}{
			"detail": "this run changed no files — the work was already in the tree",
		})
	}

	// A gate is only worth what the tests are worth. A change that edits both
	// at once goes green either way, so the test hunks are pulled out and put
	// in front of the person deciding (05 §5.3). Never blocked: sometimes a
	// test is genuinely wrong. Only never hidden.
	//
	// The task's own words decide whether this is a surprise. A task that says
	// "add tests for X" does not need a warning about tests changing, and a
	// warning that is always on is one nobody reads.
	var taskText string
	if task := s.findTask(ctx, rs.run.ProjectID, rs.run.TaskID); task != nil {
		taskText = task.Title + "\n" + task.Body
	}
	if tamper := verify.CheckTampering(diff, taskText, projCfg.Verify.TestGlobs); tamper.Flagged() {
		rs.run.TestsModified = true
		rs.writer.WriteTestHunks(tamper.Hunks)
		rs.writer.AppendEvent("tests_modified", map[string]interface{}{
			"files":   tamper.Files,
			"message": verify.TamperMessage,
		})
	}

	// A run may have proposed a skill (05 §7.1). Validated here, on the tree
	// the human is about to accept, so an unusable skill is caught before it
	// is committed rather than the first time a duckling reaches for it.
	if problems := validateProposedSkills(entry.Path); len(problems) > 0 {
		rs.writer.AppendEvent("skill_problems", map[string]interface{}{"problems": problems})
	}

	// Check if human gate is needed
	if rs.run.Autonomy == "manual" || rs.run.Autonomy == "guarded" {
		if verdict == "PASSED" || verdict == "UNVERIFIED" {
			rs.run.Status = "paused"
			rs.run.PendingKind = "gate"
			rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
			rs.run.PendingData = map[string]interface{}{"verdict": verdict}
			gateData := map[string]interface{}{"kind": "gate", "verdict": verdict}
			if len(governanceCallouts) > 0 {
				rs.run.PendingData["governance_callouts"] = governanceCallouts
				gateData["governance_callouts"] = governanceCallouts
			}
			if rs.run.DesktopStale {
				rs.run.PendingData["desktop_stale"] = desktopStaleMessage
				gateData["desktop_stale"] = desktopStaleMessage
			}
			rs.writer.AppendEvent("human_needed", gateData)
			// Acceptance later commits and proves this isolated checkout.
			rs.run.PendingData["retain_worktree"] = rs.run.WorktreePath != ""
			rs.writer.WriteState()
			s.bus.Publish(bus.Event{
				Type:      "human_needed",
				RunID:     rs.run.ID,
				ProjectID: rs.run.ProjectID,
				TS:        time.Now(),
				Data:      gateData,
			})
			return
		}
	}

	// Auto-accept or finish.
	//
	// Only when the reviewer agreed. The gate is deterministic but partial —
	// it proves the command passed, not that the change is right — and this
	// branch used to look at the gate alone. T-028's reviewer said
	// request-changes three rounds straight under a green gate; with a human
	// at the gate that dissent was at least visible, under auto it would
	// have been rubber-stamped. Dissent turns auto-accept back into a human
	// gate, wearing the reason.
	if verdict == "PASSED" && (rs.run.Autonomy == "auto" || rs.run.Autonomy == "yolo") {
		if dv, n, dissent := finalDissent(rs.runDir); dissent {
			detail := fmt.Sprintf(
				"gate green, but the reviewer's final verdict was %s (%d finding(s)) — auto-accept declined; decide it yourself",
				dv, n)
			rs.run.Status = "paused"
			rs.run.PendingKind = "gate"
			rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
			rs.run.PendingData = map[string]interface{}{"verdict": verdict, "dissent": dv, "detail": detail}
			gateData := map[string]interface{}{"kind": "gate", "verdict": verdict, "detail": detail}
			if len(governanceCallouts) > 0 {
				rs.run.PendingData["governance_callouts"] = governanceCallouts
				gateData["governance_callouts"] = governanceCallouts
			}
			if rs.run.DesktopStale {
				rs.run.PendingData["desktop_stale"] = desktopStaleMessage
				gateData["desktop_stale"] = desktopStaleMessage
			}
			rs.writer.AppendEvent("human_needed", gateData)
			rs.writer.WriteState()
			s.bus.Publish(bus.Event{
				Type: "human_needed", RunID: rs.run.ID, ProjectID: rs.run.ProjectID,
				TS:   time.Now(),
				Data: gateData,
			})
			return
		}
		// An auto-accept that fails is a decision returned to the person, not
		// a run left in limbo: T-119 committed its work, the clean-checkout
		// reproduction failed, the error was discarded and the run read
		// "running" forever with nothing left alive. The work already passed
		// its own gate, so the honest state is paused-at-gate wearing the
		// acceptance failure, exactly like reviewer dissent.
		if aerr := s.acceptRun(ctx, rs, entry, "", ""); aerr != nil {
			detail := fmt.Sprintf("auto-accept failed: %v — decide it yourself", aerr)
			rs.run.Status = "paused"
			rs.run.PendingKind = "gate"
			rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
			rs.run.PendingData = map[string]interface{}{"verdict": verdict, "detail": detail}
			rs.run.PendingData["retain_worktree"] = rs.run.WorktreePath != ""
			gateData := map[string]interface{}{"kind": "gate", "verdict": verdict, "detail": detail}
			if rs.run.DesktopStale {
				rs.run.PendingData["desktop_stale"] = desktopStaleMessage
				gateData["desktop_stale"] = desktopStaleMessage
			}
			rs.writer.AppendEvent("human_needed", gateData)
			rs.writer.WriteState()
			s.bus.Publish(bus.Event{
				Type: "human_needed", RunID: rs.run.ID, ProjectID: rs.run.ProjectID,
				TS:   time.Now(),
				Data: gateData,
			})
		}
		return
	}
	// UNVERIFIED never auto-accepts; yolo still reaches human gate
	if verdict == "UNVERIFIED" && rs.run.Autonomy == "yolo" {
		rs.run.Status = "paused"
		rs.run.PendingKind = "gate"
		rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
		gateData := map[string]interface{}{
			"kind":    "gate",
			"verdict": verdict,
		}
		if len(governanceCallouts) > 0 {
			if rs.run.PendingData == nil {
				rs.run.PendingData = map[string]interface{}{}
			}
			rs.run.PendingData["governance_callouts"] = governanceCallouts
			gateData["governance_callouts"] = governanceCallouts
		}
		if rs.run.DesktopStale {
			if rs.run.PendingData == nil {
				rs.run.PendingData = map[string]interface{}{}
			}
			rs.run.PendingData["desktop_stale"] = desktopStaleMessage
			gateData["desktop_stale"] = desktopStaleMessage
		}
		rs.writer.AppendEvent("human_needed", gateData)
		rs.writer.WriteState()
		return
	}

	rs.run.Status = "done"
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	rs.writer.AppendEvent("run_end", map[string]interface{}{"verdict": verdict})
	rs.writer.WriteState()
}

func (s *Service) emitEscalationAtDecision(rs *runState, point string) {
	if rs == nil || rs.writer == nil {
		return
	}
	events, err := runlog.ReadEvents(rs.runDir)
	if err != nil {
		return
	}
	counts := map[int]int{}
	stuckItem, stuckReports, turns, redStreak, bestRed := 0, 0, 0, 0, 0
	modeMedian := s.modeTurnMedian(rs.run.Mode, rs.run.ID)
	for _, e := range events {
		switch e.Type {
		case "turn_start":
			if role, _ := e.Data["role"].(string); role == "implementer" {
				turns++
			}
		case "deliverables_report":
			current := map[int]bool{}
			ids, ok := e.Data["missing"].([]interface{})
			if !ok {
				ids, ok = e.Data["undelivered"].([]interface{})
			}
			if ok {
				for _, raw := range ids {
					if v, ok := raw.(float64); ok {
						current[int(v)] = true
					}
				}
			}
			for id := range counts {
				if !current[id] {
					delete(counts, id)
				}
			}
			for id := range current {
				counts[id]++
			}
			for id, n := range counts {
				if n >= 3 && n > stuckReports {
					stuckItem, stuckReports = id, n
				}
			}
		case "round_gate":
			if result, _ := e.Data["result"].(string); result == "red" {
				redStreak++
				if redStreak > bestRed {
					bestRed = redStreak
				}
			} else {
				redStreak = 0
			}
		}
	}
	// Strategy may already have emitted this at the safe distress boundary. Avoid
	// duplicating the durable suggestion when the resulting pause is handled here.
	for _, e := range events {
		if e.Type == "escalation_suggestion" && e.Data["point"] == point {
			return
		}
	}
	var triggers []string
	for _, e := range events {
		if e.Type == "distress_evidence" {
			if kind, _ := e.Data["kind"].(string); kind == "unanswered_death" {
				triggers = append(triggers, "unanswered_death")
				break
			}
		}
	}
	if stuckItem > 0 {
		triggers = append(triggers, "stuck_deliverable")
	}
	if bestRed >= 3 {
		triggers = append(triggers, "consecutive_red_round_gates")
	}
	if modeMedian > 0 && float64(turns) > 2*modeMedian {
		triggers = append(triggers, "turns_over_2x_mode_median")
	}
	if len(triggers) == 0 {
		return
	}
	cards, _ := s.Scorecards(context.Background())
	cands, floor := escalationCandidatesFor(string(config.RoleImplementer), rs.run.Roster[string(config.RoleImplementer)], cards)
	if len(cands) == 0 {
		return
	}
	rs.writer.AppendEvent("escalation_suggestion", map[string]interface{}{"point": point, "thresholds_fired": triggers, "stuck_item": stuckItem, "stuck_reports": stuckReports, "turns": turns, "mode_median": modeMedian, "red_gate_streak": bestRed, "current_wilson_floor": floor, "candidate": cands[0], "diagnoses": map[string]interface{}{"seat_at_capacity": map[string]interface{}{"turns": turns, "red_gate_streak": bestRed, "stuck_item": stuckItem}, "task_brief_quality": "at-capacity and badly-briefed look identical; improve the task body"}, "actions": []string{"relaunch_with_stronger_seat", "improve_task_body", "continue_as-is"}})
}

// emitLaunchEscalation warns before this run's first model call when the same
// task and stage have already failed twice, irrespective of which seat ran them.
func (s *Service) emitLaunchEscalation(rs *runState) {
	if rs == nil || rs.writer == nil || rs.run.TaskID == "" {
		return
	}
	failures := 0
	s.runsMu.RLock()
	for id, prior := range s.runs {
		if id == rs.run.ID || prior == nil || prior.run == nil {
			continue
		}
		r := prior.run
		if r.ProjectID == rs.run.ProjectID && r.TaskID == rs.run.TaskID && r.Stage == rs.run.Stage &&
			(r.Verdict == "FAILED" || r.Verdict == "ABORTED") {
			failures++
		}
	}
	s.runsMu.RUnlock()
	if failures < 2 {
		return
	}
	cards, _ := s.Scorecards(context.Background())
	cands, floor := escalationCandidatesFor(string(config.RoleImplementer), rs.run.Roster[string(config.RoleImplementer)], cards)
	data := map[string]interface{}{
		"point": "launch", "thresholds_fired": []string{"repeated_task_stage_failure"},
		"prior_failed_or_aborted_runs": failures, "current_wilson_floor": floor,
		"diagnoses": map[string]interface{}{"task_brief_quality": "this task has failed here twice; improve the task body before reseating"},
		"actions":   []string{"relaunch_with_stronger_seat", "improve_task_body", "continue_as-is"},
	}
	// A stronger candidate is useful reseating evidence, but its absence must
	// not silence the task-level warning: improving the brief remains actionable.
	if len(cands) > 0 {
		data["candidate"] = cands[0]
	}
	rs.writer.AppendEvent("escalation_suggestion", data)
}

func (s *Service) failRun(rs *runState, err error) {
	// A budget running out is a decision point, not a defect. The run did
	// nothing wrong — the person's own ceiling stopped it — and failing it
	// RESTORED THE TREE, so two million tokens of work were rolled back when
	// one click of headroom would have finished the task. It now pauses with
	// the work in place: lift the binding cap on the run's meter and resume,
	// or abort and get the restore.
	if errors.Is(err, agent.ErrBudgetExceeded) && !s.shuttingDown.Load() &&
		rs.run.Verdict != "ABORTED" {
		recordSpend(rs, rs.tracker)
		s.publishSpend(rs, rs.tracker)
		rs.run.Status = "paused"
		rs.run.PendingKind = "budget"
		rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
		// The decision the pause asks for is "lift, or stop?" — and the
		// answer depends on the run's shape, not just its bill. A person
		// shown only "budget exceeded" lifted the cap on a run whose gate
		// had already failed dozens of times straight, and fed 5.7M more
		// tokens to a loop the number would have named.
		detail := err.Error()
		if rs.execCtx != nil && rs.execCtx.ConsecGateFails >= 3 {
			detail += fmt.Sprintf(" — CAUTION: the gate has failed %d times in a row in this run; "+
				"lifting the cap may feed a loop, not finish the work", rs.execCtx.ConsecGateFails)
		}
		rs.run.Failure = detail
		rs.writer.AppendEvent("distress_evidence", map[string]interface{}{"kind": "unanswered_death", "reason": "budget_exceeded"})
		s.emitEscalationAtDecision(rs, "distress_pause")
		rs.writer.AppendEvent("human_needed", map[string]interface{}{
			"kind": "budget", "detail": detail,
		})
		rs.writer.WriteState()
		if s.bus != nil {
			s.bus.Publish(bus.Event{
				Type: "human_needed", RunID: rs.run.ID, ProjectID: rs.run.ProjectID,
				TS:   time.Now(),
				Data: map[string]interface{}{"kind": "budget", "detail": detail},
			})
		}
		return
	}
	// A document that does not fit its author's output cap is a settings
	// problem wearing a run's clothes. Failing it threw away the draft AND
	// the fix: the person raises max_tokens (or reseats the stage) and
	// RESUME replays with the fresh config — the run is the wrong thing to
	// lose over a number in Settings.
	if errors.Is(err, agent.ErrTruncated) && !s.shuttingDown.Load() &&
		rs.run.Verdict != "ABORTED" {
		recordSpend(rs, rs.tracker)
		s.publishSpend(rs, rs.tracker)
		rs.run.Status = "paused"
		rs.run.PendingKind = "error"
		rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
		rs.run.Failure = err.Error() + " — then resume: the run replays with the new settings"
		s.emitEscalationAtDecision(rs, "distress_pause")
		rs.writer.AppendEvent("human_needed", map[string]interface{}{
			"kind": "error", "detail": rs.run.Failure,
		})
		rs.writer.WriteState()
		if s.bus != nil {
			s.bus.Publish(bus.Event{
				Type: "human_needed", RunID: rs.run.ID, ProjectID: rs.run.ProjectID,
				TS:   time.Now(),
				Data: map[string]interface{}{"kind": "error", "detail": rs.run.Failure},
			})
		}
		return
	}
	// A provider that cannot be reached — retries exhausted — is weather, not
	// a verdict on the work. Failing here restored the tree: a sustained
	// OpenRouter hiccup rolled back everything a long run had built, when
	// waiting out the weather and resuming costs nothing. The guards matter:
	// an abort also surfaces as a dead connection, and an abort must stay
	// aborted.
	if errors.Is(err, provider.ErrProviderUnavailable) && !s.shuttingDown.Load() &&
		rs.run.Verdict != "ABORTED" && !strings.Contains(err.Error(), "context canceled") {
		recordSpend(rs, rs.tracker)
		s.publishSpend(rs, rs.tracker)
		rs.run.Status = "paused"
		rs.run.PendingKind = "provider"
		rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
		rs.run.Failure = err.Error()
		s.emitEscalationAtDecision(rs, "distress_pause")
		rs.writer.AppendEvent("human_needed", map[string]interface{}{
			"kind": "provider", "detail": err.Error(),
		})
		rs.writer.WriteState()
		if s.bus != nil {
			s.bus.Publish(bus.Event{
				Type: "human_needed", RunID: rs.run.ID, ProjectID: rs.run.ProjectID,
				TS:   time.Now(),
				Data: map[string]interface{}{"kind": "provider", "detail": err.Error()},
			})
		}
		return
	}
	// The general rule the budget and provider branches above are cases of:
	// NO error may discard work automatically. Tokens are money, but the
	// hours a long run represents are the person's — and a glitch on call 51
	// used to roll all of it back unasked. Any failure that would restore a
	// dirty tree pauses instead, work in place: resume re-enters the
	// strategy over what was built, abort is the one exit that restores. A
	// run that touched nothing still fails plainly — there is nothing to
	// lose, and a pause would just be a failure demanding a second click.
	if rs.run.Verdict != "ABORTED" && !s.shuttingDown.Load() &&
		!strings.Contains(err.Error(), "context canceled") && runHasUnsavedWork(rs) {
		recordSpend(rs, rs.tracker)
		s.publishSpend(rs, rs.tracker)
		rs.run.Status = "paused"
		rs.run.PendingKind = "error"
		rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
		rs.run.Failure = err.Error()
		s.emitEscalationAtDecision(rs, "distress_pause")
		rs.writer.AppendEvent("human_needed", map[string]interface{}{
			"kind": "error", "detail": err.Error(),
		})
		rs.writer.WriteState()
		if s.bus != nil {
			s.bus.Publish(bus.Event{
				Type: "human_needed", RunID: rs.run.ID, ProjectID: rs.run.ProjectID,
				TS:   time.Now(),
				Data: map[string]interface{}{"kind": "error", "detail": err.Error()},
			})
		}
		return
	}
	// A cancellation during shutdown or an attributed restart is a pause, not
	// a failure. RequestRestart writes its checkpoint before cancelling the
	// goroutine, so preserve that durable restart reason.
	if errors.Is(err, context.Canceled) && (s.shuttingDown.Load() || rs.run.PendingKind == "engine_restart" || rs.run.PendingKind == "history_duration") {
		if rs.run.PendingKind != "engine_restart" && rs.run.PendingKind != "history_duration" {
			rs.run.Status = "paused"
			rs.run.PendingKind = "engine_shutdown"
			rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
			rs.writer.AppendEvent("checkpoint", map[string]interface{}{
				"reason": "engine_shutdown",
				"status": "paused",
			})
			rs.writer.WriteState()
		}
		return
	}
	rs.run.Status = "failed"
	rs.run.Verdict = "FAILED"
	rs.run.Failure = err.Error()
	s.emitEscalationAtDecision(rs, "failed_run")
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	// A configuration-shaped failure needs a door to the consultant, not just
	// its raw error. The finding is recorded on this failed run so the desktop
	// can seed that consultation without changing configuration on its own.
	if finding, ok := configFindingForFailure(rs.projectPath, err); ok {
		old := ""
		if cfg, loadErr := config.LoadProject(filepath.Join(rs.projectPath, ".ducklab", "project.toml")); loadErr == nil {
			old, _ = config.ValueKey(cfg, finding.Key)
		}
		rs.writer.AppendEvent("config_amendment", map[string]interface{}{
			"key": finding.Key, "old": old, "new": finding.Proposed, "why": finding.Reason,
		})
	}
	rs.writer.AppendEvent("error", map[string]interface{}{"error": err.Error()})
	rs.writer.AppendEvent("run_end", map[string]interface{}{"verdict": "FAILED"})
	restoreAfterUnaccepted(rs)
	rs.writer.WriteState()
	s.autopilotOnFail(rs.run)
}

// configFindingForFailure connects a failure to a deterministic doctor finding
// only when the failure names that setting or its proposed value. A project can
// have unrelated doctor findings; surfacing one of those would misdiagnose the
// run and send the consultant an invented premise.
func configFindingForFailure(projectPath string, err error) (config.Finding, bool) {
	if projectPath == "" || err == nil {
		return config.Finding{}, false
	}
	findings, doctorErr := config.Doctor(projectPath)
	if doctorErr != nil {
		return config.Finding{}, false
	}
	detail := strings.ToLower(err.Error())
	for _, finding := range findings {
		if strings.Contains(detail, strings.ToLower(finding.Key)) ||
			(len(finding.Proposed) > 3 && strings.Contains(detail, strings.ToLower(finding.Proposed))) {
			return finding, true
		}
	}
	return config.Finding{}, false
}

// tailOf keeps the end of a long output — the part where test runners put
// the verdicts — bounded for an event payload.
func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// acceptRun accepts a run and commits.
func (s *Service) acceptRun(ctx context.Context, rs *runState, entry *registry.ProjectEntry, message, actor string) (err error) {
	// Every settled acceptance — the human RunAccept path, an automatic accept,
	// and an auto-triage — goes through here, so publication under the on_accept
	// policy lives in the common success path, not in any single API wrapper.
	// It runs once, only when a commit actually landed (a triage or artifact
	// promote accepts without a diff, so there is nothing to publish) and after
	// the landing and checkout sync made the commit durable. It never reverses
	// the accept: on push failure the run keeps a worded warning naming the
	// commit and the reason, with the existing push door as the retry (B-266).
	defer func() {
		if err == nil {
			s.autopilotOnAccept(rs.run)
			if rs.run.Accepted && rs.run.CommitSHA != "" {
				s.publishAccept(ctx, rs)
			}
		}
	}()

	if actor == "" {
		if rs.run.Autonomy == "yolo" {
			actor = "auto:yolo"
		} else {
			actor = "human"
		}
	}
	// A recorded worktree is a custody boundary. Check every known execution
	// root before promotion or staging can mutate a different checkout.
	if rs.run.WorktreePath != "" {
		expected, _ := filepath.Abs(rs.run.WorktreePath)
		checkRoot := func(label, actual string) error {
			if actual == "" {
				return nil
			}
			normalized, _ := filepath.Abs(actual)
			if filepath.Clean(expected) != filepath.Clean(normalized) {
				return fmt.Errorf("accept root mismatch: run worktree %s, %s tree %s", rs.run.WorktreePath, label, actual)
			}
			return nil
		}
		if rs.execCtx != nil {
			if err := checkRoot("turn execution", rs.execCtx.ProjectRoot); err != nil {
				return err
			}
		}
		if rs.run.ExecutionRoot == "" {
			return fmt.Errorf("accept root mismatch: run worktree %s, turn execution root was not recorded", rs.run.WorktreePath)
		}
		if rs.run.GateRoot == "" {
			return fmt.Errorf("accept root mismatch: run worktree %s, gate root was not recorded", rs.run.WorktreePath)
		}
		if err := checkRoot("turn execution record", rs.run.ExecutionRoot); err != nil {
			return err
		}
		if err := checkRoot("gate", rs.run.GateRoot); err != nil {
			return err
		}
	}

	// A stage run's human gate is the decision to promote its document. There
	// is one decision, so there is one action: accepting the run accepts the
	// artifact.
	//
	// Before this they were separate — accept the run on one screen, promote
	// the artifact on another — and the first real user accepted the run,
	// watched the Cycle view go on saying "proposal awaiting your decision",
	// and had no way to know why.
	// A triage's gate decides a classification, not a document and not a diff.
	// Without this it fell through to the code path below, staged nothing, found
	// the tree clean, and reported success — so Accept and Reject did the same
	// thing and the whole triage was discarded with a green tick.
	if rs.run.Stage == "triage" || rs.run.Stage == "operate" {
		n, err := s.ApplyTriage(ctx, rs.run.ProjectID, rs.run.PendingData["proposals"])
		if err != nil {
			return err
		}
		rs.writer.AppendEvent("triage_applied", map[string]interface{}{"bugs": n})
		s.resolveTriageSiblings(rs)
		clearPending(rs.run)
		rs.run.Accepted = true
		rs.run.Status = "done"
		rs.run.Resolution = "accepted by " + actor
		rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
		// Every other terminal path says so on the stream; without this the
		// desktop's store never hears the triage end — the run reads
		// "running" in the rail forever and the Bugs board keeps its
		// pre-triage columns until something else forces a refetch.
		rs.writer.AppendEvent("run_end", map[string]interface{}{"verdict": rs.run.Verdict})
		rs.writer.WriteState()
		return nil
	}

	if kind := artifactKindForStage(rs.run.Stage); kind != "" {
		// Accepting THIS proposal answers every sibling still waiting: an
		// older run of the same stage whose draft was superseded holds a gate
		// nobody can decide — its proposal file is already gone.
		defer func() {
			s.runsMu.RLock()
			var moot []string
			for id, other := range s.runs {
				if id != rs.run.ID && other.run.ProjectID == rs.run.ProjectID &&
					other.run.Stage == rs.run.Stage &&
					other.run.Status == "paused" && other.run.PendingKind == "gate" {
					moot = append(moot, id)
				}
			}
			s.runsMu.RUnlock()
			for _, id := range moot {
				s.resolveSuperseded(id, "superseded: "+rs.run.ID+"'s proposal was accepted")
			}
		}()
		if _, err := s.ArtifactPromote(ctx, rs.run.ProjectID, kind, actor); err != nil {
			// A stale proposal is the one promotion error that must STOP the
			// accept: the approved document moved while this proposal waited,
			// and writing the photograph over it would erase those edits in
			// silence. The run stays at its gate; the person decides with the
			// drift named.
			if errors.Is(err, artifact.ErrProposalStale) {
				return err
			}
			// Not fatal: a run whose proposal was already promoted by hand is
			// still a run worth accepting, and the promote says so itself.
			rs.writer.AppendEvent("warning", map[string]interface{}{
				"detail": fmt.Sprintf("promote %s: %v", kind, err),
			})
		}
	}

	git := vcs.New(entry.Path)
	// Isolated runs own a branch, never the person's checkout. Their acceptance
	// is therefore a proof about the rebased branch followed by an ff-only land.
	if rs.run.WorktreePath != "" {
		return s.acceptWorktreeRun(ctx, rs, entry, git, message, actor)
	}
	// Test-first acceptance records the red-test promise even for projects that
	// have not initialized git yet; the chained BUILD will enforce the normal
	// repository requirement when it starts.
	if rs.run.Stage == "test" && !git.HasGit() {
		defer s.continueChain(ctx, rs)
		rs.run.Accepted = true
		rs.run.Status = "done"
		rs.run.Resolution = "accepted by " + actor
		rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
		clearPending(rs.run)
		return s.logResolution(rs, "accept", actor)
	}
	if message == "" {
		message = acceptCommitSubject(rs.run)
	}
	// Announce the commit before any git mutation: staging and committing can
	// take long enough that a completed round gate otherwise looks like an
	// unexplained pause. Keep this event before branch creation as well, so it
	// is durable when AddAll or CommitWithTrailer aborts acceptance.
	rs.writer.AppendEvent("gate_started", map[string]interface{}{
		"phase":  "commit",
		"detail": "committing accepted work before clean-checkout verification",
	})
	// Create branch if needed
	branch := fmt.Sprintf("ducklab/%s", rs.run.TaskID)
	git.CreateBranch(branch)
	// Persist the work branch with the acceptance record. This is the provenance
	// that lets status distinguish accepted work from work shipped on main.
	rs.run.Branch, _ = git.CurrentBranch()
	// Stage candidate work, never runtime dependency links.
	if err := stageRun(git, rs.run, entry.Path); err != nil {
		s.failRun(rs, fmt.Errorf("git add: %w", err))
		return err
	}

	// Accepting work that is already committed is a no-op, not a failure.
	//
	// Two runs of the same task produce the same fix; accepting the second
	// after the first left the tree clean made git exit 1 with "nothing to
	// commit", the raw error reached the user, and a run whose gate was green
	// and whose reviewer approved was marked FAILED. The person did nothing
	// wrong and the run did nothing wrong.
	if clean, cerr := git.IsClean(); cerr == nil && clean {
		head, _ := git.HeadSHA()
		// Announced: this is the slow tail of every accept — a full suite
		// from a fresh checkout — and after the round gate's green it read
		// as minutes of nothing.
		rs.writer.AppendEvent("gate_started", map[string]interface{}{
			"phase":  "accept",
			"detail": "reproducing the gate from a clean checkout of the accepted commit — nothing lands that did not reproduce",
		})
		reproduction, err := verifyAcceptedCommitWithBase(ctx, git, entry.Path, head, rs.run.Stage, "", verify.Identity{RunID: rs.run.ID, ProjectID: rs.run.ProjectID})
		if err != nil {
			return err
		}
		rs.run.GateReproduced = reproduction
		rs.writer.AppendEvent("gate_reproduced", map[string]interface{}{"gate": reproduction.Gate, "command": reproduction.Command, "exit_code": reproduction.ExitCode, "green": reproduction.Green, "output": reproduction.Output, "duration_s": reproduction.Duration, "acceptance_gate": reproduction})
		defer s.continueChain(ctx, rs)
		rs.run.Accepted = true
		rs.run.CommitSHA = head
		rs.run.Status = "done"
		rs.run.Resolution = "accepted by " + actor + "; the tree already carried this change"
		rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
		clearPending(rs.run)
		// The report this task came from is fixed only by an accepted BUILD.
		if rs.run.Stage == "build" {
			if id, berr := s.BugFixedByTask(ctx, rs.run.ProjectID, rs.run.TaskID); berr == nil && id != "" {
				rs.writer.AppendEvent("bug_fixed", map[string]interface{}{
					"bug": id, "task": rs.run.TaskID,
				})
			}
		}

		if err := s.logResolution(rs, "accept", actor); err != nil {
			return err
		}
		if err := writeAcceptanceReceipt(entry.Path, rs.run, actor); err != nil {
			return fmt.Errorf("write acceptance receipt: %w", err)
		}
		return nil
	}

	// Commit
	trailers := map[string]string{
		"Ducklab-Run": rs.run.ID,
		"Duckling":    "implementer",
	}
	sha, err := git.CommitWithTrailer(message, trailers)
	if err != nil {
		s.failRun(rs, fmt.Errorf("commit: %w", err))
		return err
	}
	// The commit is now real; keep the clean-checkout gate as its own visible
	// step so this running event is closed by gate_reproduced below.
	rs.writer.AppendEvent("gate_started", map[string]interface{}{
		"phase":  "accept",
		"detail": "committed " + short(sha) + "; reproducing the gate from a clean checkout — nothing lands that did not reproduce",
	})
	reproduction, err := verifyAcceptedCommit(ctx, git, entry.Path, sha, rs.run.Stage, verify.Identity{RunID: rs.run.ID, ProjectID: rs.run.ProjectID})
	if err != nil {
		// Nothing lands that did not reproduce. The commit above was ours and
		// a moment old; leaving it on the branch made the run undecidable
		// (B-069): accept re-verified the same red sha forever, and reject
		// refused because "a commit landed since the run began" — its own.
		// Take the commit back, keep the diff in the tree, and return the
		// failure: the person can fix the tree and accept (a new commit,
		// verified again) or reject (the tree restores cleanly).
		if uerr := git.UncommitOwn(sha); uerr != nil {
			rs.writer.AppendEvent("warning", map[string]interface{}{
				"detail": fmt.Sprintf("the unreproducible commit %s could not be taken back: %v", short(sha), uerr),
			})
		} else {
			rs.writer.AppendEvent("commit_withdrawn", map[string]interface{}{
				"sha": sha, "detail": "the accept commit did not reproduce from a clean checkout; its diff stays in the tree, uncommitted",
			})
		}
		return err
	}
	rs.run.GateReproduced = reproduction
	rs.writer.AppendEvent("gate_reproduced", map[string]interface{}{"gate": reproduction.Gate, "command": reproduction.Command, "exit_code": reproduction.ExitCode, "green": reproduction.Green, "output": reproduction.Output, "duration_s": reproduction.Duration, "acceptance_gate": reproduction})
	defer s.continueChain(ctx, rs)
	rs.run.Accepted = true
	rs.run.CommitSHA = sha
	rs.run.Status = "done"
	// Named even on the ordinary path: the record must say WHO decided —
	// a person, or a chain the person pre-authorized.
	rs.run.Resolution = "accepted by " + actor
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	clearPending(rs.run)
	// The report this task came from is fixed only by an accepted BUILD.
	if rs.run.Stage == "build" {
		if id, berr := s.BugFixedByTask(ctx, rs.run.ProjectID, rs.run.TaskID); berr == nil && id != "" {
			rs.writer.AppendEvent("bug_fixed", map[string]interface{}{
				"bug": id, "task": rs.run.TaskID,
			})
		}
	}

	// Logged, not assumed. These appends were ignored, and when the writer had
	// been closed they failed silently: state.json recorded the commit while
	// the log never recorded that a person accepted anything. Every client
	// derives "is this still waiting for me" from those events, so the desktop
	// went on offering Accept on a run it had already committed.
	if err := s.logResolution(rs, "accept", actor); err != nil {
		return err
	}
	if err := writeAcceptanceReceipt(entry.Path, rs.run, actor); err != nil {
		return fmt.Errorf("write acceptance receipt: %w", err)
	}
	return nil
}

// acceptWorktreeRun proves exactly the commit that will be fast-forwarded into
// the default branch. A clean registered checkout on that branch is advanced
// after the ref so its files continue to match its HEAD.
// acceptCommitSubject names an accepted commit when the person gave no
// message. A task names itself; a taskless run — a release scribe, a triage,
// a stage — used to leave "ducklab: " and an empty subject in the history
// (6a5eecb, 2026-08-28).
func acceptCommitSubject(run *runlog.Run) string {
	if run.TaskID != "" {
		return fmt.Sprintf("ducklab: %s", run.TaskID)
	}
	if subject := strings.TrimSpace(run.Subject); subject != "" {
		return fmt.Sprintf("ducklab: %s — %s", run.Stage, subject)
	}
	if note := strings.TrimSpace(run.Note); note != "" {
		return fmt.Sprintf("ducklab: %s — %s", run.Stage, note)
	}
	return fmt.Sprintf("ducklab: %s run %s", run.Stage, run.ID)
}

func (s *Service) acceptWorktreeRun(ctx context.Context, rs *runState, entry *registry.ProjectEntry, defaultGit *vcs.Git, message, actor string) error {
	if rs.run.WorktreePath == "" || rs.run.Branch == "" || rs.run.BaseSHA == "" {
		return fmt.Errorf("worktree acceptance is missing its path, branch, or base sha")
	}
	if _, err := os.Stat(rs.run.WorktreePath); err != nil {
		return fmt.Errorf("worktree acceptance cannot find %s: %w", rs.run.WorktreePath, err)
	}
	if message == "" {
		message = acceptCommitSubject(rs.run)
	}
	workGit := vcs.New(rs.run.WorktreePath)
	if err := stageRun(workGit, rs.run, entry.Path); err != nil {
		return fmt.Errorf("stage worktree: %w", err)
	}
	if clean, err := workGit.IsClean(); err != nil {
		return err
	} else if !clean {
		if _, err := workGit.CommitWithTrailer(message, map[string]string{"Ducklab-Run": rs.run.ID, "Duckling": "implementer"}); err != nil {
			return fmt.Errorf("commit worktree: %w", err)
		}
	} else if committed, err := workGit.HeadHasTrailer("Ducklab-Run", rs.run.ID); err != nil {
		return fmt.Errorf("inspect worktree HEAD for run commit: %w", err)
	} else if committed {
		// A prior accept may have committed this run before its rebase failed.
		// Its clean, tagged HEAD is already the candidate; do not commit it again.
	}

	// Capture this run's diff before rebasing can introduce unrelated upstream
	// commits into acceptance diagnostics.
	candidateSHA, err := workGit.HeadSHA()
	if err != nil {
		return fmt.Errorf("read candidate worktree HEAD: %w", err)
	}
	candidateDiffBytes, err := workGit.DiffBetween(rs.run.BaseSHA, candidateSHA)
	if err != nil {
		return fmt.Errorf("read candidate worktree diff: %w", err)
	}
	candidateDiff := string(candidateDiffBytes)

	// The chained red test remains solely on its run branch. Its build may
	// later land the combined history through the normal acceptance path.
	if rs.run.Stage == "test" && rs.run.ChainBuild != nil {
		sha := candidateSHA
		rs.writer.AppendEvent("gate_started", map[string]interface{}{"phase": "accept", "detail": "reproducing chained red test " + short(sha) + " from a clean checkout"})
		reproduction, err := verifyAcceptedCommitWithTestDiff(ctx, defaultGit, entry.Path, sha, rs.run.Stage, candidateDiff, true, verify.Identity{RunID: rs.run.ID, ProjectID: rs.run.ProjectID})
		if reproduction != nil {
			rs.run.GateReproduced = reproduction
			rs.writer.AppendEvent("gate_reproduced", map[string]interface{}{"gate": reproduction.Gate, "command": reproduction.Command, "exit_code": reproduction.ExitCode, "green": reproduction.Green, "output": reproduction.Output, "duration_s": reproduction.Duration, "acceptance_gate": reproduction})
		}
		if err != nil {
			return err
		}
		rs.run.Accepted, rs.run.CommitSHA, rs.run.Status = true, sha, "done"
		rs.run.Resolution = "accepted by " + actor
		rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
		clearPending(rs.run)
		if err := s.logResolution(rs, "accept", actor); err != nil {
			return err
		}
		if err := writeAcceptanceReceipt(entry.Path, rs.run, actor); err != nil {
			return fmt.Errorf("write acceptance receipt: %w", err)
		}
		s.continueChain(ctx, rs)
		s.cleanupRunWorktree(rs, entry.Path)
		return nil
	}

	defaultSHA, err := defaultGit.DefaultBranchHead()
	if err != nil {
		return fmt.Errorf("read default HEAD: %w", err)
	}
	if defaultSHA != rs.run.BaseSHA {
		files, rebaseErr := workGit.RebaseOnto(defaultSHA)
		if rebaseErr != nil {
			workGit.AbortIntegration()
			detail := fmt.Sprintf("rebase conflict from base %s onto default %s; conflicting files: %s. Resolve by hand in the worktree %s, or reject.", short(rs.run.BaseSHA), short(defaultSHA), strings.Join(files, ", "), rs.run.WorktreePath)
			if len(files) == 0 {
				detail = fmt.Sprintf("rebase from base %s onto default %s failed: %v. Resolve by hand in the worktree %s, or reject.", short(rs.run.BaseSHA), short(defaultSHA), rebaseErr, rs.run.WorktreePath)
			}
			rs.run.Status, rs.run.PendingKind = "paused", "gate"
			rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
			rs.run.PendingData = map[string]interface{}{"verdict": rs.run.Verdict, "detail": detail, "base_sha": rs.run.BaseSHA, "default_sha": defaultSHA, "conflicting_files": files, "worktree": rs.run.WorktreePath, "retain_worktree": true}
			rs.writer.AppendEvent("human_needed", map[string]interface{}{"kind": "gate", "detail": detail})
			_ = rs.writer.WriteState()
			return fmt.Errorf("%s", detail)
		}
	}
	rebasedSHA, err := workGit.HeadSHA()
	if err != nil {
		return fmt.Errorf("read rebased worktree HEAD: %w", err)
	}
	rs.writer.AppendEvent("gate_started", map[string]interface{}{"phase": "accept", "detail": "reproducing rebased " + short(rebasedSHA) + " from a clean checkout before fast-forward merge"})
	reproduction, verifyErr := verifyAcceptedCommitWithTestDiff(ctx, defaultGit, entry.Path, rebasedSHA, rs.run.Stage, candidateDiff, true, verify.Identity{RunID: rs.run.ID, ProjectID: rs.run.ProjectID})
	if reproduction != nil {
		rs.run.GateReproduced = reproduction
		rs.writer.AppendEvent("gate_reproduced", map[string]interface{}{"gate": reproduction.Gate, "command": reproduction.Command, "exit_code": reproduction.ExitCode, "green": reproduction.Green, "output": reproduction.Output, "duration_s": reproduction.Duration, "acceptance_gate": reproduction})
	}
	if verifyErr != nil {
		output := ""
		if reproduction != nil {
			output = reproduction.Output
		}
		detail := fmt.Sprintf("rebased commit %s failed its gate after base %s diverged to default %s: %v", short(rebasedSHA), short(rs.run.BaseSHA), short(defaultSHA), verifyErr)
		rs.run.Status, rs.run.PendingKind = "paused", "gate"
		rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
		rs.run.PendingData = map[string]interface{}{"verdict": rs.run.Verdict, "detail": detail, "output": output, "base_sha": rs.run.BaseSHA, "default_sha": defaultSHA, "retain_worktree": true}
		rs.writer.AppendEvent("human_needed", map[string]interface{}{"kind": "gate", "detail": detail, "output": output})
		_ = rs.writer.WriteState()
		return verifyErr
	}
	// Check before update-ref: after it moves, the checkout's index appears
	// dirty against the new HEAD even when it was clean before acceptance.
	onDefault, err := defaultGit.OnDefaultBranch()
	if err != nil {
		return fmt.Errorf("check registered checkout branch: %w", err)
	}
	var touched []string
	clean := true
	if onDefault {
		touched, err = defaultGit.ChangedPaths(defaultSHA, rebasedSHA)
		if err != nil {
			return fmt.Errorf("list accepted paths: %w", err)
		}
		// Cleanliness is judged on the paths the sync will TOUCH, because the
		// sync is path-scoped: SyncPathsToRevision checks out only `touched`,
		// so those are the only files a local change can lose.
		//
		// The whole-checkout rule this replaces (B-267's first landing) was
		// safe-sounding and fatal in practice: .ducklab/bugs/audit.jsonl and
		// project.toml change on every bug move and Settings edit — the
		// engine dirties its own registered checkout continuously — so the
		// whole-tree check made the fast-forward DEAD CODE. Measured: the
		// first clean-tree accept of the feature's life (T-222) still warned
		// "left untouched" because the engine had filed bugs that evening,
		// and the person's checkout silently kept the INVERSE of the landed
		// work — the stale-tree class the sync exists to kill.
		clean, err = defaultGit.PathsAreClean(touched)
		if err != nil {
			return fmt.Errorf("check registered checkout paths: %w", err)
		}
	}
	if err := defaultGit.FastForwardDefault(rs.run.Branch, defaultSHA); err != nil {
		return fmt.Errorf("fast-forward-only merge of rebased %s: %w", short(rebasedSHA), err)
	}
	// update-ref above intentionally does not alter the person's checkout. When
	// it was on the landed branch, move its files too unless candidate paths have
	// local changes that the fast-forward could overwrite.
	if onDefault {
		if !clean {
			rs.run.Warning = fmt.Sprintf("main advanced to %s; your checkout is behind and was left untouched; a commit from this tree would revert landed work, and builds read stale sources", rebasedSHA)
			rs.writer.AppendEvent("warning", map[string]interface{}{"detail": rs.run.Warning})
		} else if err := defaultGit.SyncPathsToRevision(rebasedSHA, touched); err != nil {
			// The landing is already durable. A person (or another git
			// operation) may have changed this checkout between the clean
			// check and checkout, so never turn this into a failed accept.
			rs.run.Warning = fmt.Sprintf("main advanced to %s; your checkout raced the landing and could not be advanced; run git status", rebasedSHA)
			rs.writer.AppendEvent("warning", map[string]interface{}{"detail": rs.run.Warning})
		}
	}
	defer s.continueChain(ctx, rs)
	rs.run.Accepted, rs.run.CommitSHA, rs.run.Status = true, rebasedSHA, "done"
	rs.run.Resolution = "accepted by " + actor
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	clearPending(rs.run)
	if rs.run.Stage == "build" {
		if id, err := s.BugFixedByTask(ctx, rs.run.ProjectID, rs.run.TaskID); err == nil && id != "" {
			rs.writer.AppendEvent("bug_fixed", map[string]interface{}{"bug": id, "task": rs.run.TaskID})
		}
	}
	if err := s.logResolution(rs, "accept", actor); err != nil {
		return err
	}
	if err := writeAcceptanceReceipt(entry.Path, rs.run, actor); err != nil {
		return fmt.Errorf("write acceptance receipt: %w", err)
	}
	s.cleanupRunWorktree(rs, entry.Path)
	return nil
}

// verifyAcceptedCommit runs the configured gate from a detached worktree at
// sha. The working tree may contain ignored files that git add deliberately
// omitted; only this checkout can prove the recorded commit is reproducible.
func verifyAcceptedCommit(ctx context.Context, git *vcs.Git, root, sha, stage string, identities ...verify.Identity) (*runlog.GateReproduction, error) {
	return verifyAcceptedCommitWithBase(ctx, git, root, sha, stage, "", identities...)
}

func verifyAcceptedCommitWithBase(ctx context.Context, git *vcs.Git, root, sha, stage, base string, identities ...verify.Identity) (*runlog.GateReproduction, error) {
	return verifyAcceptedCommitWithTestDiff(ctx, git, root, sha, stage, "", false, identities...)
}

func verifyAcceptedCommitWithTestDiff(ctx context.Context, git *vcs.Git, root, sha, stage, testDiff string, testDiffProvided bool, identities ...verify.Identity) (*runlog.GateReproduction, error) {
	identity := verify.Identity{}
	if len(identities) > 0 {
		identity = identities[0]
	}
	cfg, err := config.LoadProject(filepath.Join(root, ".ducklab", "project.toml"))
	if err != nil {
		return nil, fmt.Errorf("load gate config for accepted commit: %w", err)
	}
	checkout, err := os.MkdirTemp("", "ducklab-accept-")
	if err != nil {
		return nil, fmt.Errorf("create clean checkout for acceptance: %w", err)
	}
	if err := os.Remove(checkout); err != nil {
		return nil, fmt.Errorf("prepare clean checkout for acceptance: %w", err)
	}
	defer os.RemoveAll(checkout)
	if err := git.WorktreeAddDetached(checkout, sha); err != nil {
		return nil, fmt.Errorf("create clean checkout for acceptance: %w", err)
	}
	defer git.WorktreeRemove(checkout)

	// Installed dependencies are build caches, not repository truth. Projects
	// may declare additional trees; the established common cases remain the
	// zero-config default.
	linkInstalledDeps(root, checkout, cfg.Verify.LinkDeps)
	if cfg.Verify.Setup != "" {
		setup := config.Verify{Mode: string(verify.GateCustom), Custom: cfg.Verify.Setup, TimeoutS: cfg.Verify.TimeoutS}
		if result, err := verify.Run(ctx, checkout, setup, identity); err != nil {
			return nil, fmt.Errorf("prepare clean checkout for acceptance: %w", err)
		} else if !verify.IsGreen(result) {
			return nil, fmt.Errorf("prepare clean checkout for acceptance failed:\n%s", result.Output)
		}
	}

	result, err := verify.Run(ctx, checkout, cfg.Verify, identity)
	if err != nil {
		return nil, fmt.Errorf("run gate from clean checkout: %w", err)
	}
	// A project with no configured executable gate retains its existing
	// UNVERIFIED semantics. There is no command to reproduce, not a failed
	// command to reject; executable gates must be green in the clean checkout.
	reproduction := &runlog.GateReproduction{Gate: string(result.Gate), Command: result.Command, ExitCode: result.ExitCode, Output: result.Output, Duration: result.Duration, Green: verify.IsGreen(result)}
	if result.Gate == verify.GateNone {
		return reproduction, nil
	}
	// Polarity follows the stage. A test-first commit is red BY DESIGN — the
	// committed failing test IS the deliverable — and demanding green here
	// made every TDD accept impossible (B-056): the human clicked Accept,
	// the checkout reproduced the intended red, and the accept refused,
	// forever. A test accept must reproduce an honest red: failing, and not
	// by a compile error. Green from the checkout is the test-stage failure.
	if stage == "test" {
		if verify.IsGreen(result) {
			diff := testDiff
			var diffErr error
			if !testDiffProvided {
				diff, diffErr = git.ShowCommit(sha)
			}
			if diffErr == nil && !containsTestChange(diff) {
				return reproduction, fmt.Errorf("the commit contains no test changes at all; the turn's test work did not enter the accepted commit")
			}
			return reproduction, fmt.Errorf("the committed test passes from a clean checkout — it asserts nothing that is not already true")
		}
		if compileFailure(result.Output) {
			return reproduction, fmt.Errorf("the committed test does not compile from a clean checkout:\n%s", result.Output)
		}
		return reproduction, nil
	}
	if !verify.IsGreen(result) {
		if missing := missingGatePath(checkout, result.Command); missing != "" {
			return reproduction, fmt.Errorf("accepted commit %s failed its gate from a clean checkout: the gate references %s/, which the commit does not include — declare it in setup or link_deps (verify.link_deps); gate command: %s\n%s", short(sha), missing, result.Command, result.Output)
		}
		return reproduction, fmt.Errorf("accepted commit %s failed its gate from a clean checkout:\n%s", short(sha), result.Output)
	}
	return reproduction, nil
}

func containsTestChange(diff string) bool {
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++ b/") || strings.HasPrefix(line, "diff --git ") {
			if strings.Contains(line, "_test.") || strings.HasSuffix(line, "_test.go") {
				return true
			}
		}
	}
	return false
}

// linkedDependencyPaths reports every dependency path that can be linked into
// a run checkout. It is also used by staging after a run is recovered, when
// LinkedDeps itself is intentionally absent from persisted state.
func linkedDependencyPaths(root string) []string {
	paths := []string{"node_modules", filepath.Join("frontend", "node_modules"), ".venv"}
	if cfg, err := config.LoadProject(filepath.Join(root, ".ducklab", "project.toml")); err == nil {
		paths = append(paths, cfg.Verify.LinkDeps...)
	}
	return paths
}

// linkInstalledDeps symlinks installed dependency trees from the live
// project into a clean checkout. Best effort by design: a missing link just
// means the gate pays the install, and a gate that cannot install fails with
// the package manager's own words.
func linkInstalledDeps(root, checkout string, declared []string) []string {
	deps := []struct {
		rel     string
		markers []string
	}{
		{"node_modules", []string{"package.json"}},
		{filepath.Join("frontend", "node_modules"), []string{"package.json"}},
		// Python's node_modules: gitignored, referenced by relative gate
		// commands like .venv/bin/pytest. pytest.ini earns its place in the
		// marker list the hard way: a real project carried loose .py files
		// and pytest.ini at root, nothing else, and the missing link
		// stranded a yolo accept.
		{".venv", []string{"pyproject.toml", "requirements.txt", "setup.py", "setup.cfg", "pytest.ini", "tox.ini", "Pipfile"}},
	}
	for _, rel := range declared {
		deps = append(deps, struct {
			rel     string
			markers []string
		}{rel: rel})
	}
	linked := make([]string, 0, len(deps))
	for _, d := range deps {
		src := filepath.Join(root, d.rel)
		dst := filepath.Join(checkout, d.rel)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		marked := len(d.markers) == 0
		for _, m := range d.markers {
			if _, err := os.Stat(filepath.Join(filepath.Dir(dst), m)); err == nil {
				marked = true
				break
			}
		}
		if !marked {
			continue
		}
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			continue
		}
		if os.Symlink(src, dst) == nil {
			linked = append(linked, d.rel)
		}
	}
	return linked
}

// missingGatePath returns the first relative path named by a gate that is not
// present in the checkout. Shell syntax is deliberately not parsed: path-like
// command tokens are enough to turn the common missing-artifact failure into
// useful configuration advice.
func missingGatePath(checkout, command string) string {
	tokens := strings.Fields(command)
	for i, token := range tokens {
		token = strings.Trim(strings.TrimPrefix(token, "./"), "\"'`;,()")
		// CMake's conventional build product is named as a bare --build
		// operand, rather than a path containing a slash.
		if i > 0 && tokens[i-1] == "--build" && token != "" && !filepath.IsAbs(token) && !strings.HasPrefix(token, "-") {
			if _, err := os.Lstat(filepath.Join(checkout, token)); os.IsNotExist(err) {
				return token
			}
		}
		if token == "" || filepath.IsAbs(token) || strings.HasPrefix(token, "-") || !strings.Contains(token, "/") {
			continue
		}
		parts := strings.Split(filepath.ToSlash(token), "/")
		if len(parts) < 2 || parts[0] == ".." || parts[0] == "." {
			continue
		}
		// Executables conventionally live below a dependency's bin/. Reporting
		// that dependency, rather than its broad top-level directory, makes the
		// missing link_deps entry directly actionable.
		for i, part := range parts {
			if i > 0 && (part == "bin" || part == ".bin") {
				dep := filepath.Join(parts[:i]...)
				if _, err := os.Lstat(filepath.Join(checkout, dep)); os.IsNotExist(err) {
					return filepath.ToSlash(dep)
				}
				break
			}
		}
		if _, err := os.Lstat(filepath.Join(checkout, filepath.FromSlash(parts[0]))); os.IsNotExist(err) {
			return parts[0]
		}
	}
	return ""
}

// logResolution records a decision and closes the run out.
func (s *Service) logResolution(rs *runState, action, actor string) error {
	w, err := s.ensureWriter(rs)
	if err != nil {
		return fmt.Errorf("open run log: %w", err)
	}
	data := map[string]interface{}{"action": action}
	if actor == "auto:yolo" {
		data["autonomy"] = rs.run.Autonomy
	}
	if err := w.AppendEvent(actor, data); err != nil {
		return err
	}
	if err := w.AppendEvent("run_end", map[string]interface{}{"verdict": rs.run.Verdict}); err != nil {
		return err
	}
	return w.WriteState()
}

// RunResume resumes a paused run.
//
// A run paused by engine_restart or engine_shutdown is resumed by re-entering
// the strategy from its checkpoint. A run paused at a human gate is left where
// it is: the gate is answered with RunAccept/RunReject, not by resuming.
func (s *Service) RunResume(ctx context.Context, id string) (*runlog.Run, error) {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found", id)
	}
	if rs.run.Status != "paused" {
		return nil, fmt.Errorf("run %q is not paused (status: %s)", id, rs.run.Status)
	}

	w, err := s.ensureWriter(rs)
	if err != nil {
		return nil, err
	}

	// A human gate is not a resume point — it is answered with accept/reject,
	// not continued.
	if rs.run.PendingKind == "gate" {
		return rs.run, nil
	}
	entry, err := s.entryFor(rs)
	if err != nil {
		return nil, err
	}

	// Resume re-enters the strategy the run was BORN with. Build and test
	// rebuild their requests from the record; a stage run re-enters through
	// the request persisted beside it — a spec whose architect asked the
	// human used to die here as "cannot be resumed", so the question was a
	// dead end even once the person had the answer.
	if rs.run.Stage != "build" && rs.run.Stage != "test" {
		sreq, ok := loadStageRequest(rs.runDir)
		if !ok {
			return nil, fmt.Errorf("a %s run cannot be resumed — abort it and launch it again", rs.run.Stage)
		}
		sreq.resumed = true
		runCtx, cancel := context.WithCancel(context.Background())
		rs.cancel = cancel
		rs.done = make(chan struct{})
		rs.run.Status = "running"
		startActiveWallclock(rs.run, time.Now())
		clearPending(rs.run)
		rs.run.Failure = ""
		w.AppendEvent("checkpoint", map[string]interface{}{"reason": "resume", "status": "running"})
		w.WriteState()
		go s.executeStage(runCtx, rs, entry.Path, sreq)
		return rs.run, nil
	}

	// A test-first re-enters executeTestFirst with its request rebuilt from
	// the record — RunAnswer lands here, and answering a test run's question
	// used to re-enter the BUILD strategy on a run whose whole point was to
	// not build anything.
	if rs.run.Stage == "test" {
		projCfg, cfgErr := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
		if cfgErr != nil {
			return nil, cfgErr
		}
		treq := TestFirstRequest{TaskID: rs.run.TaskID, Mode: rs.run.Mode}
		if imp := rs.run.Roster["implementer"]; imp != "" {
			treq.Ducklings = []string{imp}
			if rev := rs.run.Roster["reviewer"]; rev != "" && rs.run.Mode == "pair" {
				treq.Ducklings = append(treq.Ducklings, rev)
			}
		}
		// The chain promise stays ON the record (consumed at acceptance);
		// the request only needs to know it is there.
		if rs.run.ChainBuild != nil {
			treq.ThenBuild = true
			if raw, mErr := json.Marshal(rs.run.ChainBuild); mErr == nil {
				_ = json.Unmarshal(raw, &treq.Build)
			}
		}
		runCtx, cancel := context.WithCancel(context.Background())
		rs.cancel = cancel
		rs.done = make(chan struct{})
		rs.run.Status = "running"
		startActiveWallclock(rs.run, time.Now())
		clearPending(rs.run)
		// The failure text was the pause's reason; resuming answers it. Left
		// in place, a resumed, working run went on wearing "Why it failed".
		rs.run.Failure = ""
		w.AppendEvent("checkpoint", map[string]interface{}{"reason": "resume", "status": "running"})
		w.WriteState()
		s.queue.submit(s, &queued{
			rs: rs, ctx: runCtx, chained: true,
			exec: func(c context.Context) { s.executeTestFirst(c, rs, runRoot(rs.run, entry.Path), projCfg, treq) },
		})
		return rs.run, nil
	}

	req := resumeRequest(rs.run)

	runCtx, cancel := context.WithCancel(context.Background())
	rs.cancel = cancel
	rs.done = make(chan struct{})
	// Cleared BEFORE the queue looks: projectHeld counts paused build runs,
	// and a run still wearing "paused" would hold the project against its own
	// resume — queued forever behind itself.
	rs.run.Status = "running"
	startActiveWallclock(rs.run, time.Now())
	clearPending(rs.run)
	// The failure text was the pause's reason (a budget pause records it);
	// resuming answers it. Left in place, a resumed, working run went on
	// wearing "Why it failed" over a live conversation.
	rs.run.Failure = ""
	w.AppendEvent("checkpoint", map[string]interface{}{"reason": "resume", "status": "running"})
	w.WriteState()

	// Through the queue like everything else, at the FRONT: it was mid-flight
	// already. Resuming used to spawn its goroutine directly, so a resumed run
	// executed uncounted — the queue could start a second run in the same
	// project's tree right beside it.
	s.queue.submit(s, &queued{
		rs: rs, ctx: runCtx, chained: true,
		exec: func(c context.Context) { s.executeRun(c, rs, entry, req) },
	})
	return rs.run, nil
}

// resumeRequest rebuilds a paused run's request from its record: a resumed
// run keeps EVERYTHING it was started with. The note and the calls cap were
// dropped here once — so the instruction the person wrote and the ceiling
// they lifted both quietly reverted at exactly the moment they were resuming
// past.
func resumeRequest(run *runlog.Run) RunRequest {
	req := RunRequest{
		TaskID:       run.TaskID,
		Mode:         run.Mode,
		Autonomy:     run.Autonomy,
		Note:         run.Note,
		AgentTurns:   run.AgentTurns,
		NoStream:     !run.Stream,
		UnsafeWrites: run.UnsafeWrites,
		resumed:      true,
	}
	// The SEATS ride the record too. Without them the dispatch re-resolved
	// the roster from the config defaults, so a run the person had
	// explicitly seated came back speaking through another model — glm52's
	// record, luna's calls — and only the per-call upstream field told on
	// it. Mirrors what the test-first resume branch already carried.
	if imp := run.Roster["implementer"]; imp != "" {
		req.Ducklings = append(req.Ducklings, imp)
		if rev := run.Roster["reviewer"]; rev != "" && run.Mode == "pair" {
			req.Ducklings = append(req.Ducklings, rev)
		}
	}
	return req
}

// RunBudgetLift removes one cap — tokens, usd, turns, wallclock, or calls —
// from a LIVE run. One-way for the run's remaining life, and per-cap on
// purpose: lifting tokens leaves the dollar ceiling standing guard. Recorded
// on the run (who, what, what it was), because a ceiling that moves silently
// is a ceiling that never existed.
//
// "calls" is the odd one out: not a tracker limit but the per-reply call cap
// inside the agent loop. The lift lands mid-flight — every live loop
// consults it before its next call — because the alternative was watching a
// reviewer die on exactly its hundredth call and resuming it into the same
// ceiling.
func (s *Service) RunBudgetLift(ctx context.Context, id, kind string) (*runlog.Run, error) {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found", id)
	}
	switch rs.run.Status {
	case "running", "paused":
	case "queued":
		return nil, fmt.Errorf("not lifted — %s has not started; its budget begins with it", id)
	default:
		return nil, fmt.Errorf("not lifted — %s has ended (%s); a finished run spends nothing", id, rs.run.Status)
	}
	if kind == "calls" {
		w, werr := s.ensureWriter(rs)
		if werr != nil {
			return nil, werr
		}
		rs.wmu.Lock()
		was := rs.run.AgentTurns
		rs.run.AgentTurns = -1
		rs.wmu.Unlock()
		// The atomic is what a loop already in flight reads before its next
		// call; the record is what a resume re-enters with.
		rs.capLifted.Store(true)
		w.AppendEvent("budget_lifted", map[string]interface{}{
			"kind": kind, "was": was, "by": "human",
		})
		if err := w.WriteState(); err != nil {
			return nil, err
		}
		return rs.snapshotRun(), nil
	}
	rs.wmu.Lock()
	tracker := rs.tracker
	rs.wmu.Unlock()
	if tracker == nil {
		return nil, fmt.Errorf("not lifted — %s has no live budget yet", id)
	}
	was, err := tracker.Lift(kind)
	if err != nil {
		return nil, err
	}
	w, werr := s.ensureWriter(rs)
	if werr != nil {
		return nil, werr
	}
	rs.wmu.Lock()
	switch kind {
	case "tokens":
		rs.run.Budget.Limit.Tokens = 0
	case "usd":
		rs.run.Budget.Limit.USD = 0
	case "turns":
		rs.run.Budget.Limit.Turns = 0
	case "wallclock":
		rs.run.Budget.Limit.WallclockS = 0
	}
	rs.wmu.Unlock()
	w.AppendEvent("budget_lifted", map[string]interface{}{
		"kind": kind, "was": was, "by": "human",
	})
	if err := w.WriteState(); err != nil {
		return nil, err
	}
	// The meters everywhere update now, not at the next model call.
	s.publishSpend(rs, tracker)
	return rs.snapshotRun(), nil
}

// RunAbort aborts a run.
func (s *Service) RunAbort(ctx context.Context, id string) error {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok {
		return fmt.Errorf("run %q not found", id)
	}
	wasQueued := rs.run.Status == "queued"
	wasActive := rs.run.Status == "running"
	if rs.cancel != nil {
		rs.cancel()
	}
	// A terminal abort must not return while the isolated checkout is still
	// being used: callers commonly tear down the project immediately afterward.
	// Queued runs have no goroutine (and thus no done close) until promoted.
	if !wasQueued && rs.done != nil && (rs.run.Stage == "build" || rs.run.Stage == "test") {
		select {
		case <-rs.done:
		case <-ctx.Done():
		}
	}
	w, err := s.ensureWriter(rs)
	if err != nil {
		return err
	}
	rs.run.Status = "failed"
	rs.run.Verdict = "ABORTED"
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	clearPending(rs.run)
	w.AppendEvent("run_end", map[string]interface{}{"verdict": "ABORTED"})
	// A paused or already-failed run has no goroutine left to unwind, so its
	// cancellation cannot reach failRun. Restore those runs here. For an active
	// run, leave restoration to failRun after its last write; restoring while it
	// is still running would race the model's final filesystem operation.
	if wasQueued {
		s.cleanupRunWorktree(rs, rs.projectPath)
	}
	if rs.done == nil {
		restoreAfterUnaccepted(rs)
	} else {
		select {
		case <-rs.done:
			restoreAfterUnaccepted(rs)
			if !wasActive {
				s.cleanupRunWorktree(rs, rs.projectPath)
			}
		default:
		}
	}
	// The run stays in the map: it is still inspectable through RunGet and
	// still on disk. Deleting it made an aborted run vanish from run list.
	werr := w.WriteState()
	// The abort changes the queue's answers twice over: a QUEUED run must
	// leave the line (promoted later it would be resurrected), and whatever
	// this run was holding — a slot about to free, a paused tree — may now
	// let a waiting run start. Nothing else re-examines the line on abort:
	// T-075's relaunch sat queued forever in a project where nothing ran,
	// because the only pokes lived on the gate decisions.
	s.queue.remove(rs)
	s.queue.poke(s)
	return werr
}

// RunDir returns the run directory for a run ID, or empty if not found.
func (s *Service) RunDir(runID string) string {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()
	if rs, ok := s.runs[runID]; ok {
		// runDir is recorded on the state itself, so a rehydrated run with no
		// open writer still resolves. Returning "" here silently emptied the
		// SSE backlog replay after every engine restart.
		if rs.runDir != "" {
			return rs.runDir
		}
		if rs.writer != nil {
			return rs.writer.RunDir()
		}
	}
	return ""
}

// RunGet returns a run detail.
// snapshotRun returns a copy of the run that is safe to serve, and honest
// while the run is still going.
//
// Safe: the Spend map is deep-copied under the same lock the adapter writes
// it with, so a fetch mid-call cannot race the attribution of the call that
// is landing. Honest: the aggregate budget is read from the live tracker for
// a run that has not ended — recordSpend copies it onto the record only at
// the end, so a fetch made mid-run served zeros, and a run view opened while
// a slow local model worked showed a dead meter for the length of the call.
// setTracker publishes the run's tracker under the lock snapshotRun reads it
// with. Six run kinds assign it; a bare write at any of them would race the
// first fetch.
func (rs *runState) setTracker(t *budget.Tracker) {
	rs.wmu.Lock()
	rs.tracker = t
	rs.wmu.Unlock()
}

func (rs *runState) snapshotRun() *runlog.Run {
	rs.wmu.Lock()
	defer rs.wmu.Unlock()
	clone := *rs.run
	if rs.run.Spend != nil {
		clone.Spend = make(map[string]runlog.DucklingSpend, len(rs.run.Spend))
		for k, v := range rs.run.Spend {
			clone.Spend[k] = v
		}
	}
	if (clone.Status == "running" || clone.Status == "paused") &&
		rs.tracker != nil && rs.tracker.Spend != nil {
		snap := rs.tracker.Spend.Snapshot()
		clone.Budget.USD = snap.USD
		clone.Budget.Tokens = snap.Tokens
		clone.Budget.Turns = snap.Turns
		clone.Budget.WallclockS = snap.WallclockS
	}
	return &clone
}

func (s *Service) RunGet(ctx context.Context, id string) (*RunDetail, error) {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found", id)
	}
	events, _ := runlog.ReadEvents(s.RunDir(id))
	run := rs.snapshotRun()
	var offer *LandingOffer
	if run.Status == "done" && !run.Accepted && run.Resolution != "landed" {
		if sha, err := vcs.New(rs.projectPath).TrailerCommitOnDefault("Ducklab-Run", run.ID); err == nil && sha != "" {
			offer = &LandingOffer{CommitSHA: sha, Evidence: fmt.Sprintf("default branch commit %s carries Ducklab-Run: %s", sha, run.ID)}
		}
	}
	// Runs that failed before the reason was recorded on the run still have it
	// in their event stream, and the events are already in hand here.
	if run.Status == "failed" && run.Failure == "" {
		run.Failure = failureFromEvents(events)
	}
	// Failed runs carry a bounded, editable retry recommendation. Generation is
	// deterministic and uses only the run record and captured artefacts; it
	// never decides or relaunches anything.
	if run.RedoNote == nil && redoNoteEligible(run) {
		if note := s.draftRedoNote(ctx, rs); note != nil {
			run.RedoNote = note
		}
	}
	// Always recomputed: the stored copy cannot be allowed to disagree with
	// the rules.
	run.Next = runNext(run)
	return &RunDetail{
		Run: run, Events: events, LandingOffer: offer,
	}, nil
}

// failureFromEvents finds the reason a run failed in its event stream. The last
// error wins: a run that failed while handling an earlier error died of the
// second one.
func failureFromEvents(events []*runlog.Event) string {
	out := ""
	for _, e := range events {
		if e == nil || e.Type != "error" {
			continue
		}
		if msg, ok := e.Data["error"].(string); ok && msg != "" {
			out = msg
		}
	}
	return out
}

// RunList lists runs.
func (s *Service) RunList(ctx context.Context, f RunFilter) ([]*runlog.Run, error) {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()
	var runs []*runlog.Run
	for _, rs := range s.runs {
		if f.ProjectID != "" && rs.run.ProjectID != f.ProjectID {
			continue
		}
		if f.Status != "" && rs.run.Status != f.Status {
			continue
		}
		// A copy with Next recomputed: the shared record must not be written
		// while other readers hold it, and the stored list cannot be allowed to
		// disagree with the rules.
		clone := rs.snapshotRun()
		clone.Next = runNext(clone)
		runs = append(runs, clone)
	}
	// Newest first, and deterministically.
	//
	// This ranged over a map, so the order was reshuffled on every call: three
	// consecutive `run list` invocations returned three different orders, and
	// the newest run could land anywhere. Anything reading "the first run" —
	// a script, the desktop's recent list, a person scanning the top of the
	// table — got an arbitrary answer that changed under it.
	//
	// StartedAt is RFC3339, so lexical order is chronological. The id breaks
	// ties, since two runs can start within the same second.
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAt != runs[j].StartedAt {
			return runs[i].StartedAt > runs[j].StartedAt
		}
		return runs[i].ID > runs[j].ID
	})
	return runs, nil
}

func writeAcceptanceReceipt(root string, run *runlog.Run, actor string) error {
	git := vcs.New(root)
	base, err := git.ParentSHA(run.CommitSHA)
	if err != nil {
		// A root commit has no parent; git's canonical empty tree is its base.
		base = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	}
	diff, err := git.DiffSHA256(base, run.CommitSHA)
	if err != nil {
		return fmt.Errorf("receipt diff: %w", err)
	}
	g := run.GateReproduced
	verdict := "red"
	if g.Green {
		verdict = "green"
	}
	return runlog.WriteReceipt(root, run.ID, runlog.AcceptanceReceipt{BaseSHA: base, HeadSHA: run.CommitSHA, DiffSHA256: diff, GateCommand: g.Command, ExitCode: g.ExitCode, DurationS: g.Duration, ReproductionVerdict: verdict, AcceptedBy: actor, AcceptedAt: run.EndedAt})
}

// RunAccept accepts a run.
// RunAcceptAs is RunAccept with the decider named. The record must never say
// a human decided what an operator decided: actor lands in approved_by and in
// the run's resolution. Empty means human — the desktop and CLI, where a
// person is pressing the button.
func (s *Service) RunAcceptAs(ctx context.Context, id, msg, actor string) (*AcceptResult, error) {
	if actor == "" {
		actor = "human"
	}
	return s.runAccept(ctx, id, msg, actor)
}

func (s *Service) RunAccept(ctx context.Context, id string, msg string) (*AcceptResult, error) {
	return s.runAccept(ctx, id, msg, "human")
}

func (s *Service) runAccept(ctx context.Context, id string, msg string, actor string) (*AcceptResult, error) {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found", id)
	}
	entry, err := s.entryFor(rs)
	if err != nil {
		return nil, err
	}
	if _, err := s.ensureWriter(rs); err != nil {
		return nil, err
	}
	if err := s.acceptRun(ctx, rs, entry, msg, actor); err != nil {
		return nil, err
	}
	// The decision freed the working tree this run's diff was holding. Runs
	// queued behind that hold have no released slot to promote them — the
	// queue must be told the world changed. After continueChain (deferred
	// inside acceptRun), so a chained build is already at the line's front.
	s.queue.poke(s)
	return &AcceptResult{CommitSHA: rs.run.CommitSHA, Warning: rs.run.Warning}, nil
}

// RunReject rejects a run.
// continueChain starts the build a chained test-first pre-authorized, once
// the test is accepted — by the red-landing automation or by a person
// deciding a paused UNVERIFIED. The chain lives on the run record precisely
// so a pause cannot break the promise: before this, the person accepted by
// hand, the task fell back to Todo, and they re-selected it to click the
// Build the chain had already been asked for.
func (s *Service) continueChain(ctx context.Context, rs *runState) {
	if rs.run.Stage != "test" || rs.run.ChainBuild == nil {
		return
	}
	raw, err := json.Marshal(rs.run.ChainBuild)
	if err != nil {
		return
	}
	var build RunRequest
	if err := json.Unmarshal(raw, &build); err != nil {
		return
	}
	rs.run.ChainBuild = nil
	if build.TaskID == "" {
		build.TaskID = rs.run.TaskID
	}
	// The chain is one unit: if other work is waiting for this project, the
	// build goes first, so no other test lands on the suite this chain has
	// deliberately left red.
	build.chained = true
	build.chainBase = rs.run.CommitSHA
	run, err := s.RunStart(context.Background(), rs.run.ProjectID, build)
	if err != nil {
		if w, wErr := s.ensureWriter(rs); wErr == nil {
			w.AppendEvent("warning", map[string]interface{}{
				"detail": fmt.Sprintf("tdd chain: the build refused to start (%v); start it from the board", err),
			})
			_ = w.WriteState()
		}
		return
	}
	if w, wErr := s.ensureWriter(rs); wErr == nil {
		w.AppendEvent("tdd_build_started", map[string]interface{}{"run": run.ID})
		_ = w.WriteState()
	}
}

func (s *Service) resolveTriageSiblings(accepted *runState) {
	covered := triageBugIDs(accepted.run.PendingData)
	if len(covered) == 0 {
		return
	}
	s.runsMu.RLock()
	var moot []string
	for id, other := range s.runs {
		if id == accepted.run.ID || other.run.ProjectID != accepted.run.ProjectID || other.run.Stage != "triage" || other.run.Status != "paused" || other.run.PendingKind != "gate" {
			continue
		}
		for bugID := range triageBugIDs(other.run.PendingData) {
			if _, ok := covered[bugID]; ok {
				moot = append(moot, id)
				break
			}
		}
	}
	s.runsMu.RUnlock()
	for _, id := range moot {
		s.resolveSuperseded(id, "superseded: "+accepted.run.ID+" covered the same bug")
	}
}

func triageBugIDs(data map[string]interface{}) map[string]struct{} {
	ids := make(map[string]struct{})
	if raw, ok := data["proposals"].([]map[string]interface{}); ok {
		for _, p := range raw {
			if id, ok := p["bug"].(string); ok && id != "" {
				ids[id] = struct{}{}
			}
		}
	} else if raw, ok := data["proposals"].([]interface{}); ok {
		for _, item := range raw {
			if p, ok := item.(map[string]interface{}); ok {
				if id, ok := p["bug"].(string); ok && id != "" {
					ids[id] = struct{}{}
				}
			}
		}
	}
	return ids
}

// resolveSuperseded closes a run whose gate was answered by another act — a
// revision requested on its draft, or a sibling's proposal accepted over it.
// Quietly a no-op when the run is not waiting: only a paused gate is moot.
func (s *Service) resolveSuperseded(id, resolution string) {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok || rs.run.Status != "paused" || rs.run.PendingKind != "gate" {
		return
	}
	w, err := s.ensureWriter(rs)
	if err != nil {
		return
	}
	rs.run.Status = "done"
	rs.run.Resolution = resolution
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	if rs.run.Stage == "intake" {
		_ = artifact.ResolveIntent(rs.projectPath, rs.run.ID, "superseded", nil)
	}
	clearPending(rs.run)
	w.AppendEvent("human", map[string]interface{}{"action": "superseded", "detail": resolution})
	w.AppendEvent("run_end", map[string]interface{}{"verdict": rs.run.Verdict})
	_ = w.WriteState()
}

// RunLand records that an operator manually landed the work represented by a
// completed run. A landing is accepted work, so it replaces an earlier reject
// verdict as well as recording the auditable landing resolution.
func (s *Service) RunLand(ctx context.Context, id, sha, actor, note string) error {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	rs, ok := s.runs[id]
	if !ok {
		return fmt.Errorf("run %q not found", id)
	}
	if rs.run.Status != "done" && (rs.run.Status != "paused" || rs.run.PendingKind != "gate") {
		return fmt.Errorf("run %q is %s; only done runs or paused gates may be landed", id, rs.run.Status)
	}
	if err := vcs.New(rs.projectPath).IsReachableFromDefault(sha); err != nil {
		return err
	}
	if actor == "" {
		actor = "human"
	}
	w, err := s.ensureWriter(rs)
	if err != nil {
		return err
	}
	rs.run.Accepted = true
	rs.run.Resolution = "landed"
	rs.run.Verdict = "PASSED"
	rs.run.CommitSHA = sha
	if rs.run.Status == "paused" {
		rs.run.Status = "done"
		rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
		clearPending(rs.run)
	}
	evidence := strings.TrimSpace(note)
	if evidence == "" {
		evidence = "manual attestation (no Ducklab-Run trailer)"
	} else if evidence == "trailer match" {
		evidence = fmt.Sprintf("trailer match: default branch commit %s carries Ducklab-Run: %s", sha, id)
	} else {
		evidence = "manual attestation: " + evidence
	}
	w.AppendEvent("human", map[string]interface{}{"action": "landed", "actor": actor, "reason": evidence, "commit_sha": sha, "evidence": evidence})
	w.AppendEvent("run_end", map[string]interface{}{"verdict": "PASSED", "resolution": "landed", "evidence": evidence})
	if rs.run.Stage == "build" {
		if bugID, bugErr := s.BugFixedByTask(ctx, rs.run.ProjectID, rs.run.TaskID); bugErr == nil && bugID != "" {
			w.AppendEvent("bug_fixed", map[string]interface{}{"bug": bugID, "task": rs.run.TaskID})
		}
	}
	err = w.WriteState()
	if rs.run.WorktreePath != "" {
		s.cleanupRunWorktree(rs, rs.projectPath)
	}
	// Landing resolves a gate just like acceptance or rejection, so queued work
	// held behind it must be reconsidered. It is also an acceptance decision:
	// guarded autopilot may continue with the next lawful task after the human
	// lands this one, just as it does after RunAccept.
	if err == nil {
		s.autopilotOnAccept(rs.run)
	}
	s.queue.poke(s)
	return err
}

// stageRun is the sole staging path for a run. LinkedDeps are runtime
// symlinks supplied to an isolated checkout and must never enter a commit.
func stageRun(git *vcs.Git, run *runlog.Run, projectRoot string) error {
	// LinkedDeps is runtime-only and deliberately omitted from persisted run
	// state. Reconstruct its configured paths too, so an accept after restart
	// has the same staging boundary as an in-memory chain.
	excluded := append([]string(nil), run.LinkedDeps...)
	excluded = append(excluded, linkedDependencyPaths(projectRoot)...)
	return git.AddAllExcluding(excluded...)
}

func (s *Service) RunReject(ctx context.Context, id, reason string) error {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok {
		return fmt.Errorf("run %q not found", id)
	}
	w, err := s.ensureWriter(rs)
	if err != nil {
		return err
	}
	// Restore through the run's execution root before removing an isolated
	// checkout. Its snapshot and recorded writes belong to that worktree, never
	// the registered human checkout.
	if err := restoreAfterUnaccepted(rs); err != nil {
		return err
	}
	// Rejecting the test revokes the pre-authorized build.
	rs.run.ChainBuild = nil
	rs.run.Status = "done"
	rs.run.Verdict = "FAILED"
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	if rs.run.Stage == "intake" {
		_ = artifact.ResolveIntent(rs.projectPath, rs.run.ID, "not accepted", nil)
	}
	clearPending(rs.run)
	w.AppendEvent("human", map[string]interface{}{"action": "reject", "reason": reason})
	w.AppendEvent("run_end", map[string]interface{}{"verdict": "FAILED"})
	err = w.WriteState()
	if rs.run.WorktreePath != "" {
		s.cleanupRunWorktree(rs, rs.projectPath)
	}
	// The rejection restored the tree; whatever queued behind its hold can go.
	s.queue.poke(s)
	return err
}

// RunAnswer records a person's answer and resumes the run.
//
// The run replays its turn with the answer available, so the ask_human call
// that paused it now resolves instead of pausing again.
func (s *Service) RunAnswer(ctx context.Context, id, questionID, answer string) error {
	return s.runAnswer(ctx, id, questionID, answer, "")
}

// runAnswer records an answer with its actual author when automation supplied
// it. An empty author deliberately remains an ordinary human answer.
func (s *Service) runAnswer(ctx context.Context, id, questionID, answer, author string) error {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok {
		return fmt.Errorf("run %q not found", id)
	}
	if rs.run.PendingKind != "question" {
		return fmt.Errorf("run %q is not waiting for an answer (pending: %q)", id, rs.run.PendingKind)
	}
	if questionID == "" {
		// Answering "the pending question" without naming it is the common
		// case from a CLI; take it from the checkpoint.
		if v, ok := rs.run.PendingData["question_id"].(string); ok {
			questionID = v
		}
	}
	if questionID == "" {
		return fmt.Errorf("run %q has no recorded question id", id)
	}
	// The question's text travels with the answer: the id survives only an
	// exact re-ask, and the replayed prompt needs the words.
	questionText, _ := rs.run.PendingData["question"].(string)
	rs.recordAnswer(questionID, questionText, answer)

	w, err := s.ensureWriter(rs)
	if err != nil {
		return err
	}
	event := map[string]interface{}{
		"action":      "answer",
		"question_id": questionID,
		"question":    questionText,
		"answer":      answer,
	}
	if author != "" {
		event["author"] = author
	}
	w.AppendEvent("human", event)
	if author != "" {
		// This is an attention event, not another human decision: unattended
		// runs continue, but the operator can inspect and correct the answer.
		w.AppendEvent("notification", map[string]interface{}{
			"kind": "advisor_auto_answer", "question_id": questionID,
			"question": questionText, "answer": answer, "author": author,
		})
	}

	_, err = s.RunResume(ctx, id)
	return err
}

// writeProjectTOML persists a project config. Delegates to config.SaveProject
// so serialization cannot drift from the loader.
func writeProjectTOML(path string, cfg *config.Project) error {
	return config.SaveProject(path, cfg)
}

func slugify(s string) string {
	// Simplified slugify
	result := ""
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result += string(r)
		} else if r >= 'A' && r <= 'Z' {
			result += string(r + 32)
		} else if r == ' ' || r == '_' {
			result += "-"
		}
	}
	if result == "" {
		result = "project"
	}
	if len(result) > 32 {
		result = result[:32]
	}
	return result
}

// publishSpend tells watching clients what the run has spent so far, and which
// duckling spent it.
//
// The totals were written to the run record only when the run ended, so the
// desktop's meter read zero for however long the work took and then jumped to
// the final number. In a mode with more than one model the question is usually
// "which of them is burning this", and the answer existed the whole time — the
// per-duckling attribution is computed on every call — it just never left the
// process.
//
// Display only, so it is published to the bus and never appended to
// events.jsonl: the totals are already in state.json and a replay reconstructs
// them from llm.jsonl.
func (s *Service) publishSpend(rs *runState, tracker *budget.Tracker) {
	if s.bus == nil || tracker == nil || tracker.Spend == nil {
		return
	}
	snap := tracker.Spend.Snapshot()

	ducklings := map[string]interface{}{}
	rs.wmu.Lock()
	for id, d := range rs.run.Spend {
		ducklings[id] = map[string]interface{}{
			"calls": d.Calls, "tokens": d.Tokens, "cost_usd": d.CostUSD,
			"estimated": d.Estimated,
		}
	}
	limit := rs.run.Budget.Limit
	rs.wmu.Unlock()

	s.bus.Publish(bus.Event{
		Type: "budget", RunID: rs.run.ID, ProjectID: rs.run.ProjectID,
		TS: time.Now(),
		Data: map[string]interface{}{
			"usd": snap.USD, "tokens": snap.Tokens, "turns": snap.Turns,
			"wallclock_s": snap.WallclockS,
			"limit": map[string]interface{}{
				"usd": limit.USD, "tokens": limit.Tokens,
				"turns": limit.Turns, "wallclock_s": limit.WallclockS,
			},
			// Keyed by duckling, because "the run has spent 300k" does not say
			// which model to change.
			"ducklings": ducklings,
		},
	})
}

// recoverRun turns a panic into a failed run instead of a dead engine.
//
// Only executeRun had this. The other five run goroutines — stage, review,
// release, triage, test-first — had nothing, so a panic in any of them took the
// whole process down and every other run in flight with it. The one that
// actually happened, a backwards line range handed to fs_read, could have come
// from a spec stage just as easily as from a build.
//
// The stack is kept. Without it a panic reported only its message — "slice
// bounds out of range [92:78]" — and finding which of a hundred slice
// expressions produced it meant reading the whole engine. A crash is the one
// place where that much detail is worth the noise.
func recoverRun(rs *runState) {
	r := recover()
	if r == nil {
		return
	}
	// What it spent before it died. Tokens were burned and money was charged
	// whatever happened next, and a report that omits them understates the cost
	// of exactly the runs worth being unhappy about.
	recordSpend(rs, rs.tracker)
	detail := fmt.Sprintf("panic: %v\n\n%s", r, debug.Stack())
	rs.run.Status = "failed"
	rs.run.Verdict = "ABORTED"
	rs.run.Failure = detail
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	if rs.writer != nil {
		rs.writer.AppendEvent("error", map[string]interface{}{"error": detail})
	}
	restoreAfterUnaccepted(rs)
	if rs.writer != nil {
		rs.writer.WriteState()
	}
}

// attachStreaming makes a run's turns visible while they happen.
//
// Every run kind sets Stream: true on its record, and exactly one of the six
// wired the callbacks that act on it — so a triage, a review, a release and a
// test-first all claimed to stream and emitted nothing, and their lanes sat on
// "thinking…" for the whole run with no way to tell work from a hang.
//
// Next to the cache rather than in each caller, for the same reason the budget
// ceilings are: six call sites is six chances to forget.
func (s *Service) attachStreaming(rs *runState, cache *loopCache) {
	// Tool calls are RECORD, not display: they land in events.jsonl as they
	// complete and reach every client through the writer's bus hook. Wired
	// before the stream guard below, which only gates the display-only
	// deltas — a no-stream run still owes its log the calls it made.
	cache.onToolCall = func(t *agent.Turn, duckling string, rec *agent.ToolCallRecord) {
		data := map[string]interface{}{
			"round": t.Round, "turn": t.Index,
			"role": string(t.Role), "duckling": duckling,
			"tool": rec.Name,
			"args": string(rec.Args),
		}
		if rec.Result != nil {
			data["ok"] = !rec.Result.IsError
			data["result"] = strategy.SummariseToolResult(rec.Result.Content)
		}
		if rec.Digest != "" {
			data["digest"] = rec.Digest
		}
		rs.writer.AppendEvent("tool_call", data)
	}
	// What just began running, before it either returns or eats its whole
	// ceiling in silence.
	cache.onToolStart = func(t *agent.Turn, duckling, name string, args json.RawMessage) {
		rs.writer.AppendEvent("tool_call_started", map[string]interface{}{
			"round": t.Round, "turn": t.Index,
			"role": string(t.Role), "duckling": duckling,
			"tool": name, "args": string(args),
		})
	}
	// Where the reply stands against its cap, as it moves. The card read
	// "default" while an architect sat at 19 calls of an invisible 24.
	cache.onCall = func(t *agent.Turn, n, max int) {
		rs.writer.AppendEvent("reply_call", map[string]interface{}{
			"round": t.Round, "turn": t.Index,
			"role": string(t.Role), "duckling": string(t.Duckling),
			"n": n, "max": max,
		})
	}
	// In time to act: the reply is about to spend its last allowed call,
	// and the lift that could save it sits one tick away in the budget card.
	cache.onCapNear = func(t *agent.Turn, used, max int) {
		rs.writer.AppendEvent("warning", map[string]interface{}{
			"round": t.Round, "turn": t.Index,
			"role": string(t.Role), "duckling": string(t.Duckling),
			"detail": fmt.Sprintf("%s is on the LAST of its %d calls for this reply — tick "+
				"\"no cap\" on calls/reply in the budget card to let it keep working, or it "+
				"will answer from what it has", t.Role, max),
		})
	}
	// Provider weather, on the record as it happens: the person watching an
	// idle run decides with "retrying (2): provider sent nothing for 2m0s"
	// where before they had silence.
	cache.onRetry = func(t *agent.Turn, attempt int, err error) {
		rs.writer.AppendEvent("provider_retry", map[string]interface{}{
			"round": t.Round, "turn": t.Index,
			"role": string(t.Role), "duckling": string(t.Duckling),
			"attempt": attempt, "error": err.Error(),
		})
	}
	cache.onRepetitionLoop = func(t *agent.Turn, repeated string) {
		rs.writer.AppendEvent("repetition_loop", map[string]interface{}{
			"round": t.Round, "turn": t.Index, "role": string(t.Role),
			"duckling": string(t.Duckling), "repeated": repeated,
		})
	}
	if !rs.run.Stream || s.bus == nil {
		return
	}
	runID, projectID := rs.run.ID, rs.run.ProjectID
	publish := func(kind string) func(*agent.Turn, string) {
		return func(t *agent.Turn, text string) {
			s.bus.Publish(bus.Event{
				Type: kind, RunID: runID, ProjectID: projectID,
				TS: time.Now(),
				Data: map[string]interface{}{
					"role": string(t.Role), "duckling": string(t.Duckling),
					// Which turn, not just which duckling: a council's second
					// architect turn must not append to the first one's text.
					"round": t.Round, "turn": t.Index,
					"text": text,
				},
			})
		}
	}
	// token_delta and reasoning_delta are never persisted (01 §5.3): they are
	// display state, and writing them would bloat events.jsonl with data no
	// resume needs.
	cache.onDelta = publish("token_delta")
	// Its own event type, not more token_delta: thinking appended to the answer
	// would make the transcript show a model's false starts as its reply, and
	// the contract parser reads that text.
	cache.onReasoning = publish("reasoning_delta")
}

// runHasUnsavedWork reports whether failing this run would destroy something:
// the tree has diverged from the run's own starting snapshot. Measured by
// re-hashing the tree, not by git-clean — a project's tree is often "dirty"
// from birth (uncommitted scaffolding), and pre-existing dirt is not this
// run's work. Same content hashes to the same tree object, so equality means
// the run changed nothing. Doubt counts as work — pausing over nothing costs
// a click; discarding hours cannot be undone.
func runHasUnsavedWork(rs *runState) bool {
	if rs == nil || rs.run == nil || rs.run.TreeSnapshot == "" || rs.projectPath == "" {
		return false
	}
	now, err := vcs.New(runRoot(rs.run, rs.projectPath)).SnapshotTree()
	if err != nil {
		return true
	}
	return now != rs.run.TreeSnapshot
}

// restoreAfterUnaccepted puts the tree back to the run's start when the run
// ended without its work being accepted. Quietly a no-op when there is nothing
// recorded — stage runs and pre-git projects take no snapshot.
// rosterSummary renders the resolved team for roster_read: seats per mode
// with provenance, each seated duckling's evidence in the one grammar the
// desktop uses, and the engine's suggestions.
func (s *Service) rosterSummary(ctx context.Context, projectID string) (string, error) {
	cards, err := s.Scorecards(ctx)
	if err != nil {
		return "", err
	}
	byID := map[string]Scorecard{}
	for _, c := range cards {
		byID[c.ID] = c
	}
	evidence := func(id, role string) string {
		c, ok := byID[id]
		if !ok {
			return ""
		}
		m := c.Measured
		if r, has := c.MeasuredByRole[role]; has && r.Runs > 0 {
			m = &r
		}
		out := ""
		if m != nil && m.Runs > 0 {
			out = fmt.Sprintf("%.0f%% · %d runs · $%.2f/run", m.PassRate, m.Runs, m.AvgCostPerRun)
		}
		if c.Index != nil && c.Index.CodingScore > 0 {
			if out != "" {
				out += " · "
			}
			out += fmt.Sprintf("coding %.1f", c.Index.CodingScore)
		}
		if out == "" {
			out = "no evidence yet"
		}
		return out
	}
	var b strings.Builder
	b.WriteString("The resolved team (the source the launchers use).\n")
	for _, mode := range []string{"solo", "pair", "council", "split", "tournament", "common"} {
		view, verr := s.RosterGet(ctx, projectID, mode)
		if verr != nil {
			// A project whose config cannot load still has the global team.
			if view, verr = s.GlobalRosterGet(ctx, mode); verr != nil {
				continue
			}
		}
		fmt.Fprintf(&b, "\n## %s\n", mode)
		if view.Warning != "" {
			fmt.Fprintf(&b, "(%s)\n", view.Warning)
		}
		for _, e := range view.Entries {
			ids := e.Ducklings
			if len(ids) == 0 && e.Duckling != "" {
				ids = []string{e.Duckling}
			}
			if len(ids) == 0 {
				fmt.Fprintf(&b, "- %s: (unseated)", e.Role)
			} else {
				fmt.Fprintf(&b, "- %s: %s [%s]", e.Role, strings.Join(ids, ", "), e.Source)
				for _, id := range ids {
					fmt.Fprintf(&b, " — %s", evidence(id, e.Role))
				}
			}
			if len(e.Candidates) > 0 {
				var names []string
				for _, c := range e.Candidates {
					names = append(names, c.ID)
				}
				fmt.Fprintf(&b, " · suggested: %s", strings.Join(names, ", "))
			}
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// runWrittenPaths reads the run's own record of what it wrote — every
// fs_write, fs_write_lines and successful fs_patch names its path in the
// event log, and the shell policy routes mutations through those tools — so
// a restore can undo the run's edits and nobody else's (B-077). nil when the
// log cannot be read: unknown is not the same claim as "wrote nothing".
func runWrittenPaths(runDir string) []string {
	f, err := os.Open(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	seen := map[string]bool{}
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var e struct {
			Type string `json:"type"`
			Data struct {
				Tool string          `json:"tool"`
				OK   *bool           `json:"ok"`
				Args json.RawMessage `json:"args"`
			} `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type != "tool_call" {
			continue
		}
		switch e.Data.Tool {
		case "fs_write", "fs_write_lines", "fs_patch":
		default:
			continue
		}
		if e.Data.OK != nil && !*e.Data.OK {
			continue // a refused or failed call changed nothing
		}
		var raw string
		var args struct {
			Path string `json:"path"`
		}
		// args arrives as a JSON string holding JSON.
		if json.Unmarshal(e.Data.Args, &raw) == nil {
			_ = json.Unmarshal([]byte(raw), &args)
		} else {
			_ = json.Unmarshal(e.Data.Args, &args)
		}
		if args.Path != "" && !seen[args.Path] {
			seen[args.Path] = true
			out = append(out, args.Path)
		}
	}
	return out
}

func restoreAfterUnaccepted(rs *runState) error {
	if rs == nil || rs.run == nil || rs.run.TreeSnapshot == "" || rs.run.Accepted {
		return nil
	}
	git := vcs.New(runRoot(rs.run, rs.projectPath))
	if err := git.RestoreTreeAtHeadScoped(rs.run.TreeSnapshot, rs.run.TreeSnapshotHead, runWrittenPaths(rs.runDir)); err != nil {
		// Said, not swallowed: a person who believes the tree is clean will
		// trust the next run's diff.
		if rs.writer != nil {
			rs.writer.AppendEvent("warning", map[string]interface{}{
				"detail": "the tree could not be restored to the run's start: " + err.Error(),
			})
		}
		return err
	}
	if rs.writer != nil {
		rs.writer.AppendEvent("tree_restored", map[string]interface{}{
			"snapshot": rs.run.TreeSnapshot,
		})
	}
	return nil
}

// llmWriter builds the adapter every run kind must use.
//
// The adapter carries two things beyond the log itself: the run record, so
// each call is attributed to its duckling, and the spend hook, so the budget
// meter moves while the money moves. They were wired at ONE of the six places
// an adapter is built — the same one-of-six disease as the streaming callbacks
// and the budget ceilings before it — so a council's intake showed a meter at
// zero for the whole run, and a triage's calls were attributed to nobody.
// activeWallclock measures execution time only. StartedAt is intentionally not
// consulted: it includes both queue waits and human pauses.
func activeWallclock(run *runlog.Run, now time.Time) time.Duration {
	if run == nil {
		return 0
	}
	elapsed := time.Duration(run.ActiveWallclockMs) * time.Millisecond
	if run.Status == "running" && run.ActiveSince != "" {
		if since, err := time.Parse(time.RFC3339Nano, run.ActiveSince); err == nil && now.After(since) {
			elapsed += now.Sub(since)
		}
	}
	return elapsed
}

func settleActiveWallclock(run *runlog.Run, now time.Time) {
	if run == nil {
		return
	}
	run.ActiveWallclockMs = activeWallclock(run, now).Milliseconds()
	run.ActiveSince = ""
}

func startActiveWallclock(run *runlog.Run, now time.Time) {
	if run != nil && run.ActiveSince == "" {
		run.ActiveSince = now.UTC().Format(time.RFC3339Nano)
	}
}

func (s *Service) monitorWallclockEscalation(ctx context.Context, rs *runState) {
	startActiveWallclock(rs.run, time.Now())
	defer settleActiveWallclock(rs.run, time.Now())
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkWallclockEscalation(rs)
		}
	}
}

// checkWallclockEscalation emits the existing escalation card once a live run
// exceeds the configured multiple of its mode/project history. Five completed
// runs are required so a single outlier never becomes misleading advice.
func (s *Service) checkWallclockEscalation(rs *runState) {
	if rs == nil || rs.run == nil || rs.writer == nil || rs.run.Status != "running" || rs.historyEscalated.Load() {
		return
	}
	s.cfgMu.RLock()
	multiplier := s.cfg.Defaults.Budget.WallclockEscalationMultiplier
	s.cfgMu.RUnlock()
	if multiplier <= 0 {
		multiplier = 2
	}
	var total float64
	var count int
	s.runsMu.RLock()
	for id, prior := range s.runs {
		if id == rs.run.ID || prior == nil || prior.run == nil {
			continue
		}
		r := prior.run
		if r.ProjectID == rs.run.ProjectID && r.Mode == rs.run.Mode && r.EndedAt != "" {
			ms := r.ActiveWallclockMs
			// Records written before active timing have no interval history.
			if ms == 0 {
				ms = r.WallclockMs
			}
			if ms > 0 {
				total += float64(ms) / 1000
				count++
			}
		}
	}
	s.runsMu.RUnlock()
	if count < 5 || total/float64(count) <= 0 {
		return
	}
	average := total / float64(count)
	elapsed := activeWallclock(rs.run, time.Now()).Seconds()

	if elapsed < multiplier*average || !rs.historyEscalated.CompareAndSwap(false, true) {
		return
	}
	stage := rs.run.Stage
	if rs.run.InterruptedTurn != nil {
		stage = fmt.Sprintf("%s, round %d", rs.run.InterruptedTurn.Role, rs.run.InterruptedTurn.Round)
	}
	data := map[string]interface{}{
		"point": "wallclock_history", "thresholds_fired": []string{"wallclock_over_history"},
		"current_stage": stage,
		"wallclock_s":   elapsed, "history_average_s": average, "history_runs": count,
		"detail":    fmt.Sprintf("%.0fm so far; runs of this shape average %.0fm", elapsed/60, average/60),
		"diagnoses": map[string]interface{}{"seat_at_capacity": "the current seat may be saturated", "task_brief_quality": "the brief may be too wide"},
		"actions":   []string{"relaunch_with_stronger_seat", "improve_task_body", "continue_as-is"},
	}
	rs.writer.AppendEvent("escalation_suggestion", data)
	// Pause at the next safe point, not now: the turn in flight finishes
	// and its work lands on the record before the run stops (I9). The
	// turn_end hook files the card and cancels.
	rs.wmu.Lock()
	rs.pausePending = data
	rs.wmu.Unlock()
	rs.pauseAfterTurn.Store(true)
	rs.writer.AppendEvent("pause_requested", map[string]interface{}{"kind": "history_duration", "detail": "pausing when the current turn ends"})
}

// pauseAtSafePoint lands a requested pause once the turn in flight has
// ended: the run is marked paused with its card, and only then cancelled.
func (s *Service) pauseAtSafePoint(rs *runState) {
	if rs == nil || rs.run == nil || rs.writer == nil || !rs.pauseAfterTurn.CompareAndSwap(true, false) {
		return
	}
	rs.wmu.Lock()
	data := rs.pausePending
	rs.pausePending = nil
	rs.wmu.Unlock()
	detail, _ := data["detail"].(string)
	settleActiveWallclock(rs.run, time.Now())
	rs.run.Status = "paused"
	rs.run.PendingKind = "history_duration"
	rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
	rs.run.PendingData = data
	rs.writer.AppendEvent("human_needed", map[string]interface{}{"kind": "history_duration", "detail": detail})
	rs.writer.WriteState()
	if rs.cancel != nil {
		rs.cancel()
	}
}

func (s *Service) llmWriter(rs *runState, tracker *budget.Tracker) *runLogAdapter {
	return &runLogAdapter{
		w: rs.writer, run: rs.run, mu: &rs.wmu,
		onSpend: func() {
			s.publishSpend(rs, tracker)
			s.checkWallclockEscalation(rs)
		},
	}
}

// applyStageLineup maps a mode's saved line-up onto a stage's roles: the first
// duckling drafts, the second critiques.
//
// Settings has let a person save a council line-up since mode line-ups
// existed, and nothing ever read it: ducklingsFor was wired into task runs,
// and council only ever runs as a stage. The person ticked k3 and luna, saved,
// launched intake — and watched one model draft AND critique itself, which is
// the exact decorrelation failure line-ups exist to prevent.
// It reports which seats the line-up filled, because provenance is "the
// line-up named this seat", not "the value changed" — a line-up that happens
// to agree with the alphabetical default still spoke.
// stageCritics returns the ducklings a council seats as critics: every
// duckling in the mode's line-up after the first, which drafts. Two ticked
// boxes give the old shape — one drafter, one critic — and a third box seats
// a third pair of eyes instead of silently doing nothing, which is what it
// did for as long as the council had exactly two chairs.
//
// A line-up entry that no longer names a real duckling is skipped rather than
// failing the run: the line-up is a preference, and a deleted model should
// degrade the council, not close it.
func (s *Service) stageCritics(mode string) []config.DucklingID {
	if mode == "" {
		mode = "council"
	}
	s.cfgMu.RLock()
	var ids []config.DucklingID
	seats := s.cfg.Defaults.ModeSeats[mode]
	for _, raw := range seats["reviewer"] {
		ids = append(ids, config.DucklingID(raw))
	}
	s.cfgMu.RUnlock()
	if len(ids) > 0 {
		var out []config.DucklingID
		seen := map[config.DucklingID]bool{}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			if _, err := s.ducklings.Get(id); err == nil {
				out = append(out, id)
				seen[id] = true
			}
		}
		return out
	}
	return s.criticsFrom(mode, s.stageLineupFor(mode))
}

// criticsFrom reads the critics out of a given line-up — the saved one, or a
// run's own override — so both paths seat critique turns identically.
func (s *Service) criticsFrom(mode string, lineup []string) []config.DucklingID {
	if len(lineup) < 2 {
		return nil
	}
	var critics []config.DucklingID
	for _, id := range lineup[1:] {
		if id == "" {
			continue
		}
		if _, err := s.ducklings.Get(config.DucklingID(id)); err != nil {
			continue
		}
		critics = append(critics, config.DucklingID(id))
	}
	return critics
}

// stageLineupFor is the seat source for DOCUMENT stages, whatever their
// mode: the documents group's own line-up (council), first seat only when
// the stage runs solo. Settings files the architect under documents —
// "architect · drafts" — and a solo amendment reading the TASKS' solo
// line-up meant saving a new architect there changed nothing the amendment
// used: the person edited the right seat and the engine looked at another.
func (s *Service) stageLineupFor(mode string) []string {
	s.cfgMu.RLock()
	seats := s.cfg.Defaults.ModeSeats[mode]
	orders := map[string][]string{
		"solo":       {"implementer", "advisor"},
		"pair":       {"implementer", "advisor", "reviewer"},
		"council":    {"architect", "reviewer"},
		"split":      {"architect", "implementer", "reviewer"},
		"tournament": {"implementer", "judge"},
	}
	var lineup []string
	for _, role := range orders[mode] {
		lineup = append(lineup, seats[role]...)
	}
	s.cfgMu.RUnlock()
	return lineup
}

func applyStageLineup(roster map[config.Role]config.DucklingID, lineup []string) []config.Role {
	var filled []config.Role
	for i, role := range []config.Role{config.RoleArchitect, config.RoleReviewer} {
		if i >= len(lineup) || lineup[i] == "" {
			break
		}
		roster[role] = config.DucklingID(lineup[i])
		filled = append(filled, role)
	}
	return filled
}

// RunReseat moves a paused run's seats from one duckling to its stand-in —
// the declared-fallback door for provider weather. Explicit and recorded,
// never a router's choice: the person (or their pre-authorized chain) names
// the swap, a seat_failover event lands on the record, and the run resumes
// with its ledger intact. Availability only — a run paused at a human gate
// has nothing to reseat.
func (s *Service) RunReseat(ctx context.Context, id, from, to string) (*runlog.Run, error) {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found", id)
	}
	if rs.run.Status != "paused" || (rs.run.PendingKind != "provider" && rs.run.PendingKind != "error") {
		return nil, fmt.Errorf("reseat answers provider weather; this run is %s/%s",
			rs.run.Status, orDefault(rs.run.PendingKind, "none"))
	}
	if _, err := s.ducklings.Get(config.DucklingID(to)); err != nil {
		return nil, fmt.Errorf("no duckling %q to reseat onto", to)
	}
	var roles []string
	for role, d := range rs.run.Roster {
		if d == from {
			rs.run.Roster[role] = to
			roles = append(roles, role)
		}
	}
	if len(roles) == 0 {
		return nil, fmt.Errorf("%s holds no seat on this run", from)
	}
	sort.Strings(roles)
	// A stage run re-resolves its line-up from config on resume, which would
	// quietly undo the swap: the override goes into the persisted request,
	// the same per-run seat door the chips use.
	if rs.run.Stage != "build" && rs.run.Stage != "test" {
		if sreq, ok := loadStageRequest(rs.runDir); ok {
			lineup := sreq.Ducklings
			if len(lineup) == 0 {
				lineup = s.stageLineupFor(rs.run.Mode)
			}
			for i := range lineup {
				if lineup[i] == from {
					lineup[i] = to
				}
			}
			sreq.Ducklings = lineup
			writeStageRequest(rs.runDir, sreq)
		}
	}
	if w, err := s.ensureWriter(rs); err == nil {
		w.AppendEvent("seat_failover", map[string]interface{}{
			"from": from, "to": to, "roles": roles,
			"reason": orDefault(rs.run.Failure, "provider unreachable"),
		})
	}
	return s.RunResume(ctx, id)
}
