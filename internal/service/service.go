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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/registry"
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
	bus       *bus.Bus
	runs      map[string]*runState
	runsMu    sync.RWMutex
	providers map[config.ProviderID]provider.Provider
	projects  map[string]*projectState
	projMu    sync.RWMutex
	// shuttingDown makes an in-flight run's cancellation read as a deliberate
	// pause rather than a failure, so a graceful stop never marks work FAILED.
	shuttingDown atomic.Bool
}

type projectState struct {
	cfg   *config.Project
	db    *store.DB
	git   *vcs.Git
	lock  sync.Mutex
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
	if req.Describe != "" {
		// TODO: set description
	}
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
			ID:   e.ID,
			Path: e.Path,
			Name: e.Name,
		}
		projects = append(projects, p)
	}
	return projects, nil
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
		TaskCounts: taskCounts,
		ActiveRuns: active,
	}, nil
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
	TaskID       string            `json:"task_id"`
	Mode         string            `json:"mode"`
	Ducklings    []string          `json:"ducklings"`
	Rounds       int               `json:"rounds"`
	Verify       string            `json:"verify"`
	Budget       *budget.Budget    `json:"budget"`
	Autonomy     string            `json:"autonomy"`
	Stream       bool              `json:"stream"`
	DryRun       bool              `json:"dry_run"`
	Parallel     bool              `json:"parallel"`
	UnsafeWrites bool              `json:"unsafe_writes"`
}

// RunDetail is a run detail.
type RunDetail struct {
	Run    *runlog.Run `json:"run"`
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

// RunStart starts a run. Returns immediately with the run in running status.
func (s *Service) RunStart(ctx context.Context, projectID string, req RunRequest) (*runlog.Run, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}

	// Create run
	runID := runlog.GenerateRunID()
	run := &runlog.Run{
		ID:           runID,
		ProjectID:    projectID,
		Stage:        "build",
		Mode:         req.Mode,
		TaskID:       req.TaskID,
		Status:       "running",
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
		Stream:       req.Stream,
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
		"mode":   run.Mode,
		"task_id": run.TaskID,
	})

	// Dry-run is synchronous: render prompts, no model calls, exit immediately
	if req.DryRun {
		s.executeDryRun(rs, entry, req)
		return run, nil
	}

	// Execute asynchronously
	go s.executeRun(ctx, rs, entry, req)

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
		Duckling:    call.Duckling,
		Provider:    call.Provider,
		Model:       call.Model,
		Role:        call.Role,
		Request:     call.Request,
		Response:    call.Response,
		Usage:       call.Usage,
		CostUSD:     call.CostUSD,
		LatencyMs:   call.LatencyMs,
		Attempt:     call.Attempt,
		Estimated:   call.Estimated,
		CostSource:  call.CostSource,
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

	// Resolve duckling
	ducklingID := projCfg.Roster[config.RoleImplementer]
	if ducklingID == "" {
		// Find first available duckling
		for id := range s.cfg.Ducklings {
			ducklingID = id
			break
		}
	}
	d, err := s.ducklings.Get(ducklingID)
	if err != nil {
		s.failRun(rs, fmt.Errorf("get duckling: %w", err))
		return
	}
	p, err := s.ducklings.Provider(ducklingID)
	if err != nil {
		s.failRun(rs, fmt.Errorf("get provider: %w", err))
		return
	}

	// Setup budget
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

	// Setup exec context
	ectx := &tools.ExecContext{
		ProjectRoot:  entry.Path,
		RunID:        rs.run.ID,
		Autonomy:     config.Autonomy(rs.run.Autonomy),
		UnsafeWrites: rs.run.UnsafeWrites,
		ShellPolicy:  projCfg.Shell,
	}

	// Setup agent loop
	// Use caps from config if present; only probe if absent
	var caps *duckling.Capabilities
	cfgDuckling, cfgOk := s.cfg.Ducklings[ducklingID]
	if cfgOk && cfgDuckling.Caps.NativeTools != nil {
		caps = &duckling.Capabilities{
			NativeTools:   *cfgDuckling.Caps.NativeTools,
			ContextTokens: 32768,
		}
		if cfgDuckling.Caps.ContextTokens != nil {
			caps.ContextTokens = *cfgDuckling.Caps.ContextTokens
		}
	} else {
		probed, err := s.ducklings.Probe(ctx, ducklingID)
		if err != nil {
			probed = &duckling.Capabilities{NativeTools: false, ContextTokens: 32768}
		}
		caps = probed
	}
	agentLoop := &agent.Loop{
		Provider: p,
		Duckling: &agent.DucklingConfig{
			ID:       ducklingID,
			Provider: d.Provider,
			Model:    d.Model,
			Params:   d.Params,
			Caps:     provider.Capabilities(*caps),
			Cost:     d.Cost,
		},
		Registry:       tools.NewRegistry(),
		Budget:         tracker,
		MaxTurns:       s.cfg.Defaults.AgentMaxTurns,
		RepairAttempts: s.cfg.Defaults.RepairAttempts,
		RunWriter:      &runLogAdapter{w: rs.writer},
	}

	// Execute strategy
	params := &strategy.ExecuteParams{
		ProjectRoot: entry.Path,
		TaskID:      req.TaskID,
		Prompt:      fmt.Sprintf("Implement task %s", req.TaskID),
		AgentLoop:   agentLoop,
		ExecContext: ectx,
		Rounds:      req.Rounds,
	}
	fmt.Fprintf(os.Stderr, "[engine] executing strategy for run %s with duckling %s\n", rs.run.ID, ducklingID)

	_, err = strategy.ExecuteSolo(ctx, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[engine] strategy error for run %s: %v\n", rs.run.ID, err)
		s.failRun(rs, err)
		return
	}
	fmt.Fprintf(os.Stderr, "[engine] strategy done for run %s\n", rs.run.ID)

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
	rs.writer.AppendEvent("human", map[string]interface{}{"action": "accept"})
	rs.writer.AppendEvent("run_end", map[string]interface{}{"verdict": rs.run.Verdict})
	rs.writer.WriteState()
	return nil
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

	// A human gate is not a resume point — it is answered, not continued.
	if rs.run.PendingKind == "gate" {
		return rs.run, nil
	}

	entry, err := s.entryFor(rs)
	if err != nil {
		return nil, err
	}

	req := RunRequest{
		TaskID:       rs.run.TaskID,
		Mode:         rs.run.Mode,
		Autonomy:     rs.run.Autonomy,
		Stream:       rs.run.Stream,
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
	w.AppendEvent("human", map[string]interface{}{"action": "reject", "reason": reason})
	w.AppendEvent("run_end", map[string]interface{}{"verdict": "FAILED"})
	return w.WriteState()
}

// RunAnswer answers a pending question.
func (s *Service) RunAnswer(ctx context.Context, id, questionID, answer string) error {
	// For v0.1, this is a no-op placeholder
	return nil
}

func writeProjectTOML(path string, cfg *config.Project) error {
	// Simplified TOML writer
	content := fmt.Sprintf(`schema = 1
id = %q
name = %q
created = %q
autonomy = %q

[verify]
mode = %q
tests = %q
build = %q
lint = %q
custom = %q
timeout_s = %d

[git]
branch_prefix = %q
base_branch = %q
commit_trailer = %v
`,
		cfg.ID, cfg.Name, cfg.Created, cfg.Autonomy,
		cfg.Verify.Mode, cfg.Verify.Tests, cfg.Verify.Build, cfg.Verify.Lint, cfg.Verify.Custom, cfg.Verify.TimeoutS,
		cfg.Git.BranchPrefix, cfg.Git.BaseBranch, cfg.Git.CommitTrailer,
	)
	return os.WriteFile(path, []byte(content), 0o644)
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
