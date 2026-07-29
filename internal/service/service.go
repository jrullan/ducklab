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
}

// Options are service options.
type Options struct {
	Bus *bus.Bus
}

// New creates a new service.
func New(cfg *config.Global, opts Options) (*Service, error) {
	reg, err := registry.Load()
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}

	s := &Service{
		cfg:       cfg,
		registry:  reg,
		ducklings: duckling.NewRegistry(),
		bus:       opts.Bus,
		runs:      make(map[string]*runState),
		providers: make(map[config.ProviderID]provider.Provider),
		projects:  make(map[string]*projectState),
		queue:     newRunQueue(cfg.Engine.MaxConcurrentRuns),
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

// InitRequest is a project init request.
type InitRequest struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Describe string `json:"describe"`
	GitInit  bool   `json:"git_init"`
}

// ProjectOpen opens a project.
func (s *Service) ProjectOpen(ctx context.Context, path string) (*Project, error) {
	absPath, err := filepath.Abs(path)
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
	absPath, err := filepath.Abs(req.Path)
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
}

func (a *runLogAdapter) AppendLLM(call *agent.LLMCallRecord) error {
	return a.w.AppendLLM(&runlog.LLMCall{
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
}

// executeRun executes a run in the background.
func (s *Service) executeRun(ctx context.Context, rs *runState, entry *registry.ProjectEntry, req RunRequest) {
	defer close(rs.done)
	defer rs.writer.Close()
	defer func() {
		if r := recover(); r != nil {
			rs.run.Status = "failed"
			rs.run.Verdict = "ABORTED"
			rs.writer.AppendEvent("error", map[string]interface{}{"error": fmt.Sprintf("panic: %v", r)})
			rs.writer.WriteState()
		}
	}()

	// Load project config
	ducklabDir := filepath.Join(entry.Path, ".ducklab")
	projCfg, err := config.LoadProject(filepath.Join(ducklabDir, "project.toml"))
	if err != nil {
		s.failRun(rs, fmt.Errorf("load project config: %w", err))
		return
	}

	// Budget
	b := req.Budget
	if b == nil {
		b = &budget.Budget{
			MaxUSD:        projCfg.Budget.MaxUSD,
			MaxTokens:     int64(s.cfg.Defaults.Budget.MaxTokens),
			MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS,
			MaxTurns:      s.cfg.Defaults.Budget.MaxTurns,
		}
	}
	tracker := budget.NewTracker(b)

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

	roster, rosterWarning := s.resolveRoster(projCfg)
	rs.run.Roster = rosterStrings(roster)
	if rosterWarning != "" {
		// Recorded, not fatal: running both sides on one duckling is a
		// legitimate experiment, but reports must be able to segment it.
		rs.run.Warning = rosterWarning
		rs.writer.AppendEvent("warning", map[string]interface{}{"detail": rosterWarning})
	}
	cache := &loopCache{
		svc: s, tracker: tracker,
		writer: &runLogAdapter{w: rs.writer},
		loops:  map[config.DucklingID]*agent.Loop{},
	}
	if rs.run.Stream && s.bus != nil {
		// token_delta is never persisted (01 §5.3): it is display state, and
		// writing it would bloat events.jsonl with data no resume needs.
		runID, projectID := rs.run.ID, rs.run.ProjectID
		cache.onDelta = func(t *agent.Turn, text string) {
			s.bus.Publish(bus.Event{
				Type: "token_delta", RunID: runID, ProjectID: projectID,
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

	// Compute verdict
	verdict := verify.Verdict(gateResult)
	rs.run.Verdict = verdict
	rs.writer.AppendEvent("verdict", map[string]interface{}{"verdict": verdict})

	// Get diff
	git := vcs.New(entry.Path)
	diff, _ := git.Diff()
	rs.writer.WriteDiff(diff)

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
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	rs.writer.AppendEvent("error", map[string]interface{}{"error": err.Error()})
	rs.writer.AppendEvent("run_end", map[string]interface{}{"verdict": "FAILED"})
	rs.writer.WriteState()
}

// acceptRun accepts a run and commits.
func (s *Service) acceptRun(ctx context.Context, rs *runState, entry *registry.ProjectEntry, message string) error {
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
func (s *Service) RunGet(ctx context.Context, id string) (*RunDetail, error) {
	s.runsMu.RLock()
	rs, ok := s.runs[id]
	s.runsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found", id)
	}
	events, _ := runlog.ReadEvents(s.RunDir(id))
	return &RunDetail{
		Run:    rs.run,
		Events: events,
	}, nil
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
		runs = append(runs, rs.run)
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
