// Package service is the capability layer. Every operation Ducklab can
// perform is a plain Go method on Service. Both the engine handlers and
// the in-process desktop fallback call only this. No HTTP here.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/registry"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/store"
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
	queue        *runQueue
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
	// projectPath is kept so a rehydrated run can open its writer without
	// a registry lookup that may have changed since the run started.
	projectPath string
	cancel      context.CancelFunc
	done        chan struct{}
	wmu         sync.Mutex
	// givenAnswers holds human answers keyed by question id, so a resumed run
	// replays its turn without asking the same question again.
	givenAnswers map[string]string
	// tracker is what the run has spent. Kept here so a panic can still record
	// it: recordSpend runs before the error branch on an ordinary failure, but a
	// panic skips straight to the deferred recover, and the run was written out
	// with zero tokens and zero cost while its per-duckling breakdown — updated
	// on every call — said otherwise. A run that contradicts itself is worse
	// than one that admits it does not know.
	tracker *budget.Tracker
}

// Options are service options.
type Options struct {
	Bus *bus.Bus
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

	s := &Service{
		cfg:        cfg,
		configPath: opts.ConfigPath,
		registry:   reg,
		ducklings:  duckling.NewRegistry(),
		bus:        opts.Bus,
		runs:       make(map[string]*runState),
		providers:  make(map[config.ProviderID]provider.Provider),
		projects:   make(map[string]*projectState),
		queue:      newRunQueue(cfg.Engine.MaxConcurrentRuns),
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
			// Dialect B: no tools in request, respond with text protocol
			if len(req.Tools) == 0 {
				if callCount == 1 {
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
			// Dialect A: native tool calls
			switch callCount {
			case 1, 2: // may be retried after repair
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
			default:
				return &provider.ChatResponse{
					Choices: []provider.Choice{{
						Message:      provider.Message{Role: "assistant", Content: "Fixed add.go: changed a - b to a + b."},
						FinishReason: provider.FinishStop,
					}},
					Usage: provider.Usage{PromptTokens: 100, CompletionTokens: 20},
				}
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
	ID       string          `json:"id"`
	Path     string          `json:"path"`
	Name     string          `json:"name"`
	Config   *config.Project `json:"config"`
	Gate     string          `json:"gate"`
	Autonomy string          `json:"autonomy"`
	// Missing is true when the path no longer exists. MarkMissing computed it
	// and ProjectList then dropped it on the floor, so every client saw a
	// deleted project as a perfectly healthy one.
	Missing bool `json:"missing,omitempty"`
}

// Status is the project status.
type Status struct {
	StageProgress map[string]string `json:"stage_progress"`
	TaskCounts    map[string]int    `json:"task_counts"`
	BudgetSpent   float64           `json:"budget_spent_today"`
	ActiveRuns    int               `json:"active_runs"`
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
	// The documents are the record and belong in the project's history; the
	// operational state must NEVER be tracked. A project that committed its
	// live SQLite database learned why: the engine branches and checks out on
	// every accept, and a checkout rewrites tracked files — including the
	// database's write-ahead log, under an open connection. The run log and
	// lock churn on every run and belong to the machine, not the history.
	gitignore := filepath.Join(ducklabDir, ".gitignore")
	if _, err := os.Stat(gitignore); os.IsNotExist(err) {
		ignore := "# ducklab operational state — never track: a git checkout rewriting\n" +
			"# a live SQLite WAL corrupts the database under the running engine.\n" +
			"ducklab.db\nducklab.db-wal\nducklab.db-shm\nlock\nruns/\nbench/\n"
		if err := os.WriteFile(gitignore, []byte(ignore), 0o644); err != nil {
			return nil, err
		}
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
	}
	if err := vcs.EnsureGitignore(absPath, gitignoreEntries); err != nil {
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
		projects = append(projects, p)
	}
	return projects, nil
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
	return s.ProjectOpen(ctx, entry.Path)
}

// ProjectUpdate applies dotted keys to a project's config and saves it.
//
// Keys are applied to a copy and written only if every one of them is valid,
// so a typo in the second key cannot leave the first half-applied.
func (s *Service) ProjectUpdate(ctx context.Context, id string, keys map[string]string) (*Project, error) {
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
	// The registry carries the display name too, so a rename that only reached
	// project.toml would leave `project list` showing the old one.
	if updated.Name != cfg.Name {
		if err := s.registry.Rename(id, updated.Name); err != nil {
			return nil, err
		}
	}
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
	s.projMu.Lock()
	delete(s.projects, id)
	s.projMu.Unlock()
	return s.registry.Unregister(id)
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
	// Load project to get task counts
	ducklabDir := filepath.Join(entry.Path, ".ducklab")
	db, err := store.Open(filepath.Join(ducklabDir, "ducklab.db"))
	if err != nil {
		return &Status{ActiveRuns: active}, nil
	}
	defer db.Close()
	tasks, _ := db.ListTasks()
	taskCounts := make(map[string]int)
	for _, t := range tasks {
		taskCounts[t.Status]++
	}
	return &Status{
		StageProgress: stageProgress(entry.Path),
		TaskCounts:    taskCounts,
		ActiveRuns:    active,
	}, nil
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
	TaskID    string         `json:"task_id"`
	Mode      string         `json:"mode"`
	Ducklings []string       `json:"ducklings"`
	Rounds    int            `json:"rounds"`
	Verify    string         `json:"verify"`
	Budget    *budget.Budget `json:"budget"`
	Autonomy  string         `json:"autonomy"`
	// NoStream turns off token streaming for this run. The default is to
	// stream, because the desktop exists to watch a run happen.
	NoStream     bool `json:"no_stream"`
	DryRun       bool `json:"dry_run"`
	Parallel     bool `json:"parallel"`
	UnsafeWrites bool `json:"unsafe_writes"`
}

// RunDetail is a run detail.
type RunDetail struct {
	Run    *runlog.Run     `json:"run"`
	Events []*runlog.Event `json:"events,omitempty"`
}

// AcceptResult is the result of accepting a run.
type AcceptResult struct {
	CommitSHA string `json:"commit_sha"`
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

// RunStart starts a run. Returns immediately with the run in running status.
func (s *Service) RunStart(ctx context.Context, projectID string, req RunRequest) (*runlog.Run, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
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
	if req.TaskID != "" && s.findTask(ctx, projectID, req.TaskID) == nil {
		return nil, fmt.Errorf("no task %s in this project — it may have been removed; "+
			"pick one from the board", req.TaskID)
	}

	// Create run
	runID := runlog.GenerateRunID()
	run := &runlog.Run{
		ID:        runID,
		ProjectID: projectID,
		Stage:     "build",
		Mode:      req.Mode,
		TaskID:    req.TaskID,
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
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
	}
	if run.Mode == "" {
		run.Mode = "solo"
	}
	if run.Autonomy == "" {
		run.Autonomy = "guarded"
	}

	// Create writer
	writer, err := runlog.NewWriter(entry.Path, run)
	if err != nil {
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
		"mode":    run.Mode,
		"task_id": run.TaskID,
	})

	// Dry-run is synchronous: render prompts, no model calls, exit immediately
	if req.DryRun {
		s.executeDryRun(rs, entry, req)
		return run, nil
	}

	// Submit to the queue: it starts the run now, or marks it queued and
	// starts it when a slot frees (AC-25).
	s.queue.submit(s, &queued{rs: rs, entry: entry, req: req, ctx: ctx})

	return run, nil
}

// executeDryRun renders prompts without calling any model. Synchronous.
func (s *Service) executeDryRun(rs *runState, entry *registry.ProjectEntry, req RunRequest) {
	defer close(rs.done)
	defer rs.writer.Close()

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
	ectx := &tools.ExecContext{
		ProjectRoot:  entry.Path,
		RunID:        rs.run.ID,
		Autonomy:     config.Autonomy(rs.run.Autonomy),
		UnsafeWrites: rs.run.UnsafeWrites,
		ShellPolicy:  projCfg.Shell,
		Verify:       projCfg.Verify,
		Answers:      rs.answers(),
		// A project skill shadows a global one of the same name (05 §7).
		GlobalSkillsDir: globalSkillsDir(),
	}

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
func (s *Service) executeRun(ctx context.Context, rs *runState, entry *registry.ProjectEntry, req RunRequest) {
	defer close(rs.done)
	defer rs.writer.Close()
	defer recoverRun(rs)

	// Load project config
	ducklabDir := filepath.Join(entry.Path, ".ducklab")
	projCfg, err := config.LoadProject(filepath.Join(ducklabDir, "project.toml"))
	if err != nil {
		s.failRun(rs, fmt.Errorf("load project config: %w", err))
		return
	}

	// The tree as it stands, before the run touches it. What "reject" and
	// "failed" restore; without it they were words on a record while the
	// half-made edits stayed in the tree for the next attempt to trip over.
	if git := vcs.New(entry.Path); git.HasGit() {
		if snap, serr := git.SnapshotTree(); serr == nil {
			rs.run.TreeSnapshot = snap
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
	b := mergeBudget(budget.Budget{
		MaxUSD:        projCfg.Budget.MaxUSD,
		MaxTokens:     int64(s.cfg.Defaults.Budget.MaxTokens),
		MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS,
		MaxTurns:      s.cfg.Defaults.Budget.MaxTurns,
	}, req.Budget)
	tracker := budget.NewTracker(&b)
	rs.setTracker(tracker)
	recordLimits(rs, &b)

	ectx := &tools.ExecContext{
		ProjectRoot:  entry.Path,
		RunID:        rs.run.ID,
		Autonomy:     config.Autonomy(rs.run.Autonomy),
		UnsafeWrites: rs.run.UnsafeWrites,
		ShellPolicy:  projCfg.Shell,
		Verify:       projCfg.Verify,
		Answers:      rs.answers(),
		// A project skill shadows a global one of the same name (05 §7).
		GlobalSkillsDir: globalSkillsDir(),
	}

	// The line-up this run will use: what it asked for, else the one configured
	// for its mode. Filled onto the request itself so every consumer sees it —
	// tournament and split read req.Ducklings directly, and a preference that
	// only reached solo and pair would be a preference that worked in two modes
	// out of four.
	req.Ducklings = s.ducklingsFor(rs.run.Mode, req.Ducklings)

	roster, rosterWarning := s.resolveRoster(projCfg)
	// Only tournament and split read req.Ducklings, so a person who picked a
	// duckling for a solo run got the project roster's implementer instead and
	// had no way to tell: the picker sits right there in the desktop offering a
	// choice that did nothing. Applied before the roster is recorded, so the
	// run says who actually ran.
	assignChosenDucklings(roster, rs.run.Mode, req.Ducklings)
	// Recomputed, not reused: a pick can put one duckling on both sides of a
	// pair, and the warning resolveRoster produced was decided against the
	// roster before the pick. It can also separate two that were the same, in
	// which case the warning must go.
	rosterWarning = bothSidesWarning(roster)
	rs.run.Roster = rosterStrings(roster)
	if rosterWarning != "" {
		// Recorded, not fatal: running both sides on one duckling is a
		// legitimate experiment, but reports must be able to segment it.
		rs.run.Warning = rosterWarning
		rs.writer.AppendEvent("warning", map[string]interface{}{"detail": rosterWarning})
	}
	cache := &loopCache{
		svc: s, tracker: tracker,
		writer: s.llmWriter(rs, tracker),
		loops: map[config.DucklingID]*agent.Loop{},
	}
	s.attachStreaming(rs, cache)

	dispatchErr := s.dispatchMode(ctx, &modeContext{
		entry: entry, projCfg: projCfg, rs: rs, ectx: ectx,
		cache: cache, roster: roster, req: req,
	})
	// Persist what the run actually spent. Without this every report shows
	// zero tokens and zero cost, and "measurable, or it didn't happen" (P9)
	// becomes a slogan.
	recordSpend(rs, tracker)
	if dispatchErr != nil {
		// A pause is not a failure: the run is waiting for a person, and
		// waiting indefinitely is correct behaviour (01 §7.1).
		var pending *pendingErr
		if errors.As(dispatchErr, &pending) {
			s.pauseForQuestion(rs, pending.q)
			return
		}
		s.failRun(rs, dispatchErr)
		return
	}

	// Run the gate
	gateResult, err := verify.Run(entry.Path, projCfg.Verify)
	if err != nil {
		s.failRun(rs, fmt.Errorf("verify: %w", err))
		return
	}
	rs.writer.WriteVerify(gateResult.Output)
	rs.writer.AppendEvent("gate", map[string]interface{}{
		"gate": string(gateResult.Gate),
		"cmd":  gateResult.Command,
		"exit": gateResult.ExitCode,
	})

	// A run with no gate ends UNVERIFIED, which is honest and easy to miss.
	// Said here, once, with the fix — rather than leaving someone to wonder on
	// the third run why nothing ever passes.
	if advice := gateAdvice(entry.Path, projCfg.Verify); advice != "" {
		rs.run.Warning = advice
		rs.writer.AppendEvent("warning", map[string]interface{}{"detail": advice})
	}

	// Compute verdict
	verdict := verify.Verdict(gateResult)
	rs.run.Verdict = verdict
	rs.writer.AppendEvent("verdict", map[string]interface{}{"verdict": verdict})

	// Get diff
	git := vcs.New(entry.Path)
	diff, _ := git.Diff()
	rs.writer.WriteDiff(diff)

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
			rs.writer.AppendEvent("human_needed", map[string]interface{}{
				"kind":    "gate",
				"verdict": verdict,
			})
			rs.writer.WriteState()
			s.bus.Publish(bus.Event{
				Type:      "human_needed",
				RunID:     rs.run.ID,
				ProjectID: rs.run.ProjectID,
				TS:        time.Now(),
				Data: map[string]interface{}{
					"kind":    "gate",
					"verdict": verdict,
				},
			})
			return
		}
	}

	// Auto-accept or finish
	if verdict == "PASSED" && (rs.run.Autonomy == "auto" || rs.run.Autonomy == "yolo") {
		s.acceptRun(ctx, rs, entry, "")
		return
	}
	// UNVERIFIED never auto-accepts; yolo still reaches human gate
	if verdict == "UNVERIFIED" && rs.run.Autonomy == "yolo" {
		rs.run.Status = "paused"
		rs.run.PendingKind = "gate"
		rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
		rs.writer.AppendEvent("human_needed", map[string]interface{}{
			"kind":    "gate",
			"verdict": verdict,
		})
		rs.writer.WriteState()
		return
	}

	rs.run.Status = "done"
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	rs.writer.AppendEvent("run_end", map[string]interface{}{"verdict": verdict})
	rs.writer.WriteState()
}

func (s *Service) failRun(rs *runState, err error) {
	// A cancellation during graceful shutdown is a pause, not a failure.
	// Without this check, stopping the engine marks every in-flight run
	// FAILED and the work is lost.
	if s.shuttingDown.Load() && errors.Is(err, context.Canceled) {
		rs.run.Status = "paused"
		rs.run.PendingKind = "engine_shutdown"
		rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
		rs.writer.AppendEvent("checkpoint", map[string]interface{}{
			"reason": "engine_shutdown",
			"status": "paused",
		})
		rs.writer.WriteState()
		return
	}
	rs.run.Status = "failed"
	rs.run.Verdict = "FAILED"
	rs.run.Failure = err.Error()
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	rs.writer.AppendEvent("error", map[string]interface{}{"error": err.Error()})
	rs.writer.AppendEvent("run_end", map[string]interface{}{"verdict": "FAILED"})
	restoreAfterUnaccepted(rs)
	rs.writer.WriteState()
}

// acceptRun accepts a run and commits.
func (s *Service) acceptRun(ctx context.Context, rs *runState, entry *registry.ProjectEntry, message string) error {
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
		clearPending(rs.run)
		rs.run.Accepted = true
		rs.run.Status = "done"
		rs.run.Resolution = "accepted by human"
		rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
		rs.writer.WriteState()
		return nil
	}

	if kind := artifactKindForStage(rs.run.Stage); kind != "" {
		if _, err := s.ArtifactPromote(ctx, rs.run.ProjectID, kind, "human"); err != nil {
			// Not fatal: a run whose proposal was already promoted by hand is
			// still a run worth accepting, and the promote says so itself.
			rs.writer.AppendEvent("warning", map[string]interface{}{
				"detail": fmt.Sprintf("promote %s: %v", kind, err),
			})
		}
	}

	git := vcs.New(entry.Path)
	if message == "" {
		message = fmt.Sprintf("ducklab: %s", rs.run.TaskID)
	}
	// Create branch if needed
	branch := fmt.Sprintf("ducklab/%s", rs.run.TaskID)
	git.CreateBranch(branch)
	// Stage all changes
	if err := git.AddAll(); err != nil {
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
		rs.run.Accepted = true
		rs.run.CommitSHA = head
		rs.run.Status = "done"
		rs.run.Resolution = "accepted; the tree already carried this change"
		rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
		clearPending(rs.run)
		// The report this task came from, if any: promoting a bug set its task
		// id and moved it to in_progress, and nothing ever moved it again. The
		// loop had an entrance and no exit.
		if id, berr := s.BugFixedByTask(ctx, rs.run.ProjectID, rs.run.TaskID); berr == nil && id != "" {
			rs.writer.AppendEvent("bug_fixed", map[string]interface{}{
				"bug": id, "task": rs.run.TaskID,
			})
		}

		if err := s.logResolution(rs, "accept"); err != nil {
			return err
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
	rs.run.Accepted = true
	rs.run.CommitSHA = sha
	rs.run.Status = "done"
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	clearPending(rs.run)
	// The report this task came from, if any: promoting a bug set its task
	// id and moved it to in_progress, and nothing ever moved it again. The
	// loop had an entrance and no exit.
	if id, berr := s.BugFixedByTask(ctx, rs.run.ProjectID, rs.run.TaskID); berr == nil && id != "" {
		rs.writer.AppendEvent("bug_fixed", map[string]interface{}{
			"bug": id, "task": rs.run.TaskID,
		})
	}

	// Logged, not assumed. These appends were ignored, and when the writer had
	// been closed they failed silently: state.json recorded the commit while
	// the log never recorded that a person accepted anything. Every client
	// derives "is this still waiting for me" from those events, so the desktop
	// went on offering Accept on a run it had already committed.
	if err := s.logResolution(rs, "accept"); err != nil {
		return err
	}
	return nil
}

// logResolution records a human decision and closes the run out.
func (s *Service) logResolution(rs *runState, action string) error {
	w, err := s.ensureWriter(rs)
	if err != nil {
		return fmt.Errorf("open run log: %w", err)
	}
	if err := w.AppendEvent("human", map[string]interface{}{"action": action}); err != nil {
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

	req := RunRequest{
		TaskID:   rs.run.TaskID,
		Mode:     rs.run.Mode,
		Autonomy: rs.run.Autonomy,
		// A resumed run keeps whatever it was started with.
		NoStream:     !rs.run.Stream,
		UnsafeWrites: rs.run.UnsafeWrites,
	}

	runCtx, cancel := context.WithCancel(context.Background())
	rs.cancel = cancel
	rs.done = make(chan struct{})
	rs.run.Status = "running"
	rs.run.PendingKind = ""
	rs.run.PendingSince = ""
	w.AppendEvent("checkpoint", map[string]interface{}{"reason": "resume", "status": "running"})
	w.WriteState()

	go s.executeRun(runCtx, rs, entry, req)
	return rs.run, nil
}

// RunAbort aborts a run.
func (s *Service) RunAbort(ctx context.Context, id string) error {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok {
		return fmt.Errorf("run %q not found", id)
	}
	if rs.cancel != nil {
		rs.cancel()
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
	// No restore here, deliberately: the goroutine is still dying and may have
	// a write in flight. The cancel lands, it unwinds through failRun, and
	// failRun restores — after the last writer has stopped.
	// The run stays in the map: it is still inspectable through RunGet and
	// still on disk. Deleting it made an aborted run vanish from run list.
	return w.WriteState()
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
	// Runs that failed before the reason was recorded on the run still have it
	// in their event stream, and the events are already in hand here.
	if run.Status == "failed" && run.Failure == "" {
		run.Failure = failureFromEvents(events)
	}
	// Always recomputed: the stored copy cannot be allowed to disagree with
	// the rules.
	run.Next = runNext(run)
	return &RunDetail{
		Run:    run,
		Events: events,
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

// RunAccept accepts a run.
func (s *Service) RunAccept(ctx context.Context, id string, msg string) (*AcceptResult, error) {
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
	if err := s.acceptRun(ctx, rs, entry, msg); err != nil {
		return nil, err
	}
	return &AcceptResult{CommitSHA: rs.run.CommitSHA}, nil
}

// RunReject rejects a run.
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
	rs.run.Status = "done"
	rs.run.Verdict = "FAILED"
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	clearPending(rs.run)
	w.AppendEvent("human", map[string]interface{}{"action": "reject", "reason": reason})
	w.AppendEvent("run_end", map[string]interface{}{"verdict": "FAILED"})
	// Reject means no. It used to mean "no, but keep everything anyway": the
	// record said FAILED while the half-made edits stayed in the tree, and the
	// next attempt of the task found them and reasoned about work nobody had
	// accepted as though it were the project's.
	restoreAfterUnaccepted(rs)
	return w.WriteState()
}

// RunAnswer records a human's answer and resumes the run.
//
// The run replays its turn with the answer available, so the ask_human call
// that paused it now resolves instead of pausing again.
func (s *Service) RunAnswer(ctx context.Context, id, questionID, answer string) error {
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
	rs.recordAnswer(questionID, answer)

	w, err := s.ensureWriter(rs)
	if err != nil {
		return err
	}
	w.AppendEvent("human", map[string]interface{}{
		"action":      "answer",
		"question_id": questionID,
	})

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

// restoreAfterUnaccepted puts the tree back to the run's start when the run
// ended without its work being accepted. Quietly a no-op when there is nothing
// recorded — stage runs and pre-git projects take no snapshot.
func restoreAfterUnaccepted(rs *runState) {
	if rs == nil || rs.run == nil || rs.run.TreeSnapshot == "" || rs.run.Accepted {
		return
	}
	git := vcs.New(rs.projectPath)
	if err := git.RestoreTree(rs.run.TreeSnapshot); err != nil {
		// Said, not swallowed: a person who believes the tree is clean will
		// trust the next run's diff.
		if rs.writer != nil {
			rs.writer.AppendEvent("warning", map[string]interface{}{
				"detail": "the tree could not be restored to the run's start: " + err.Error(),
			})
		}
		return
	}
	if rs.writer != nil {
		rs.writer.AppendEvent("tree_restored", map[string]interface{}{
			"snapshot": rs.run.TreeSnapshot,
		})
	}
}

// llmWriter builds the adapter every run kind must use.
//
// The adapter carries two things beyond the log itself: the run record, so
// each call is attributed to its duckling, and the spend hook, so the budget
// meter moves while the money moves. They were wired at ONE of the six places
// an adapter is built — the same one-of-six disease as the streaming callbacks
// and the budget ceilings before it — so a council's intake showed a meter at
// zero for the whole run, and a triage's calls were attributed to nobody.
func (s *Service) llmWriter(rs *runState, tracker *budget.Tracker) *runLogAdapter {
	return &runLogAdapter{
		w: rs.writer, run: rs.run, mu: &rs.wmu,
		onSpend: func() { s.publishSpend(rs, tracker) },
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
	lineup := s.ducklingsFor(mode, nil)
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
