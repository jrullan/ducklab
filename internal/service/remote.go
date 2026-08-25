package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/tools"
	"github.com/jrullan/ducklab/internal/vcs"
)

// RemoteRequest is deliberately separate from run requests: remote operations
// only happen on an explicit person/operator call, never as run follow-up.
type RemoteRequest struct {
	Actor  string `json:"actor"`
	Origin string `json:"origin,omitempty"`
	Branch string `json:"branch,omitempty"`
	Title  string `json:"title,omitempty"`
}

type RemoteResult struct {
	Action     string `json:"action"`
	Actor      string `json:"actor"`
	Branch     string `json:"branch"`
	Status     string `json:"status"`
	Prompt     string `json:"prompt,omitempty"`
	CompareURL string `json:"compare_url,omitempty"`
	PRURL      string `json:"pr_url,omitempty"`
	Body       string `json:"body,omitempty"`
}

type remoteReceipt struct {
	RemoteResult
	At string `json:"at"`
}

func (s *Service) remoteProject(id string) (*projectState, error) {
	s.projMu.RLock()
	p := s.projects[id]
	s.projMu.RUnlock()
	if p != nil {
		return p, nil
	}
	entry, err := s.registry.Get(id)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return nil, err
	}
	loaded := &projectState{cfg: cfg, git: vcs.New(entry.Path)}
	s.projMu.Lock()
	if p = s.projects[id]; p == nil {
		s.projects[id] = loaded
		p = loaded
	}
	s.projMu.Unlock()
	return p, nil
}

func remoteAllowed(req RemoteRequest) error {
	if strings.TrimSpace(req.Actor) == "" {
		return fmt.Errorf("remote action requires an explicit actor")
	}
	if req.Origin == "autopilot" || req.Origin == "yolo" {
		return fmt.Errorf("remote actions require an explicit person; %s cannot perform them", req.Origin)
	}
	return nil
}

func configuredRemote(p *projectState) (string, error) {
	name := strings.TrimSpace(p.cfg.Remote.Name)
	if name == "" {
		return "", fmt.Errorf("remote action refused: no [remote] is configured")
	}
	if _, err := p.git.RemoteURL(name); err != nil {
		return "", fmt.Errorf("remote action refused: remote %q is unavailable", name)
	}
	return name, nil
}

func (s *Service) writeRemoteReceipt(p *projectState, result RemoteResult) {
	data, _ := json.Marshal(remoteReceipt{RemoteResult: result, At: s.now().UTC().Format(time.RFC3339)})
	f, err := os.OpenFile(filepath.Join(p.git.Root, ".ducklab", "remote-actions.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.Write(append(data, '\n'))
		_ = f.Close()
	}
}

func (s *Service) Pull(ctx context.Context, projectID string, req RemoteRequest) (*RemoteResult, error) {
	if err := remoteAllowed(req); err != nil {
		return nil, err
	}
	p, err := s.remoteProject(projectID)
	if err != nil {
		return nil, err
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	remote, err := configuredRemote(p)
	if err != nil {
		return nil, err
	}
	branch, err := p.git.CurrentBranch()
	if err != nil {
		return nil, err
	}
	if err := p.git.Fetch(remote); err != nil {
		return nil, err
	}
	ahead, behind, err := p.git.AheadBehind(remote, branch)
	if err != nil {
		return nil, err
	}
	out := &RemoteResult{Action: "pull", Actor: req.Actor, Branch: branch, Status: "up_to_date"}
	if ahead > 0 && behind > 0 {
		out.Status = "decision_required"
		out.Prompt = fmt.Sprintf("Your branch and the shared branch both have new commits (%d yours, %d shared). Nothing was merged. Choose whether to rebase your work, merge it yourself, or keep working locally.", ahead, behind)
		s.writeRemoteReceipt(p, *out)
		return out, nil
	}
	if behind > 0 {
		if err := p.git.FastForwardOnly(remote + "/" + branch); err != nil {
			return nil, err
		}
		out.Status = "fast_forwarded"
	}
	s.writeRemoteReceipt(p, *out)
	return out, nil
}

func (s *Service) Push(ctx context.Context, projectID string, req RemoteRequest) (*RemoteResult, error) {
	if err := remoteAllowed(req); err != nil {
		return nil, err
	}
	p, err := s.remoteProject(projectID)
	if err != nil {
		return nil, err
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	remote, err := configuredRemote(p)
	if err != nil {
		return nil, err
	}
	branch := req.Branch
	if branch == "" {
		branch, err = p.git.CurrentBranch()
		if err != nil {
			return nil, err
		}
	}
	if !p.git.BranchExists(branch) {
		return nil, fmt.Errorf("branch %q does not exist", branch)
	}
	if err := p.git.Push(remote, branch); err != nil {
		return nil, err
	}
	out := &RemoteResult{Action: "push", Actor: req.Actor, Branch: branch, Status: "pushed"}
	s.writeRemoteReceipt(p, *out)
	return out, nil
}

func (s *Service) PR(ctx context.Context, projectID string, req RemoteRequest) (*RemoteResult, error) {
	if err := remoteAllowed(req); err != nil {
		return nil, err
	}
	p, err := s.remoteProject(projectID)
	if err != nil {
		return nil, err
	}
	p.lock.Lock()
	cfg := *p.cfg
	branch := req.Branch
	if branch == "" {
		branch, err = p.git.CurrentBranch()
	}
	p.lock.Unlock()
	if err != nil {
		return nil, err
	}
	body := s.prBody(ctx, projectID, p, cfg, req.Title)
	push, err := s.Push(ctx, projectID, req)
	if err != nil {
		return nil, err
	}
	// Push records its own receipt. Record the final PR outcome separately too,
	// so the actor's request and its created/fallback result are both auditable.
	push.Action = "pr"
	push.Body = body
	base := cfg.GitHub.PRBase
	if base == "" {
		base = cfg.Git.BaseBranch
	}
	if gh, e := exec.LookPath("gh"); e == nil && cfg.GitHub.PRTool != "none" {
		if exec.CommandContext(ctx, gh, "auth", "status").Run() == nil {
			title := req.Title
			if title == "" {
				title = branch
			}
			args := []string{"pr", "create", "--base", base, "--head", branch, "--title", title, "--body", body}
			if cfg.GitHub.PRDraft {
				args = append(args, "--draft")
			}
			cmd := exec.CommandContext(ctx, gh, args...)
			cmd.Dir = p.git.Root
			raw, e := cmd.Output()
			if e == nil {
				push.Status = "created"
				push.PRURL = strings.TrimSpace(string(raw))
				s.writeRemoteReceipt(p, *push)
				return push, nil
			}
		}
	}
	url, _ := p.git.RemoteURL(cfg.Remote.Name)
	if cfg.GitHub.Repo != "" && !strings.HasPrefix(url, "https://github.com/") && !strings.HasPrefix(url, "git@github.com:") {
		url = "https://github.com/" + cfg.GitHub.Repo
	}
	push.Status = "compare_url"
	push.CompareURL = compareURL(url, base, branch)
	s.writeRemoteReceipt(p, *push)
	return push, nil
}

// prBody asks the seated scribe to turn the accepted run record into PR prose.
// It deliberately gives the model a closed record, rather than repository access:
// PR copy must not claim work absent from accepted receipts.
func (s *Service) prBody(ctx context.Context, projectID string, p *projectState, cfg config.Project, title string) string {
	if !cfg.GitHub.PRBodyByScribe {
		return ""
	}
	record := s.prRunRecord(ctx, projectID)
	fallback := "## Summary\n\nDrafted from Ducklab's run record (no scribe seat was available).\n\n" + record
	roster, _ := s.resolveRoster(&cfg, "release")
	if roster[config.RoleScribe] == "" {
		return fallback
	}

	run := &runlog.Run{ID: runlog.GenerateRunID(), ProjectID: projectID, Stage: "pr_body", Mode: "solo", Status: "running", StartedAt: s.now().UTC().Format(time.RFC3339), Stream: true, Gate: "none"}
	writer, err := runlog.NewWriter(p.git.Root, run)
	if err != nil {
		return fallback
	}
	defer writer.Close()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	rs := &runState{run: run, writer: writer, runDir: writer.RunDir(), projectPath: p.git.Root, cancel: cancel, done: make(chan struct{})}
	s.attachWriter(rs, writer)
	defer close(rs.done)
	limitsValue := projectBudget(budget.Budget{MaxUSD: s.cfg.Defaults.Budget.MaxUSD, MaxTokens: int64(s.cfg.Defaults.Budget.MaxTokens), MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS, MaxTurns: s.cfg.Defaults.Budget.MaxTurns}, cfg.Budget)
	tracker := budget.NewTracker(&limitsValue)
	recordLimits(rs, &limitsValue)
	rs.setTracker(tracker)
	ectx := &tools.ExecContext{ProjectRoot: p.git.Root, RunID: run.ID}
	cache := &loopCache{svc: s, tracker: tracker, writer: s.llmWriter(rs, tracker), capLift: rs.capLifted.Load, loops: map[config.DucklingID]*agent.Loop{}}
	s.attachStreaming(rs, cache)
	writer.AppendEvent("pr_body_draft", map[string]interface{}{"source": "run_record", "title": title})
	res, err := strategy.ExecuteScript(runCtx, strategy.ReleaseScript(), &strategy.ExecuteParams{LiveToolEvents: true, ProjectRoot: p.git.Root, Prompt: prScribePrompt(title, record), Runner: s.runnerFor(cache, roster, ectx), Roster: roster, OnEvent: func(kind string, data map[string]interface{}) { writer.AppendEvent(kind, data) }})
	recordSpend(rs, tracker)
	if err != nil || res == nil || res.Outcome == nil || strings.TrimSpace(res.Outcome.Text) == "" {
		run.Status = "failed"
		writer.AppendEvent("pr_body_fallback", map[string]interface{}{"reason": "scribe did not return a draft"})
		writer.WriteState()
		return fallback
	}
	run.Status = "done"
	writer.AppendEvent("pr_body_drafted", map[string]interface{}{"source": "scribe"})
	writer.WriteState()
	return strings.TrimSpace(res.Outcome.Text)
}

func (s *Service) prRunRecord(ctx context.Context, projectID string) string {
	titles := s.taskTitles(ctx, projectID)
	runs, _ := s.RunList(ctx, RunFilter{ProjectID: projectID})
	var lines []string
	for _, r := range runs {
		if !r.Accepted || r.TaskID == "" || r.CommitSHA == "" {
			continue
		}
		t := titles[r.TaskID]
		title := t.title
		if title == "" {
			title = r.TaskID
		}
		lines = append(lines, fmt.Sprintf("- %s — %s\n  Deliverable: %s\n  Receipt SHA: %s", r.TaskID, title, t.summary, r.CommitSHA))
	}
	if len(lines) == 0 {
		return "## Run record\n\nNo accepted task receipts were recorded."
	}
	return "## Run record\n\n" + strings.Join(lines, "\n")
}

func prScribePrompt(title, record string) string {
	if strings.TrimSpace(title) == "" {
		title = "the proposed change"
	}
	return "Draft a pull request body for " + title + ". It must be understandable to someone new to the repository. Use only this accepted run record; do not invent claims. Mention the task titles, deliverables, and receipt SHAs where useful. Return only the Markdown PR body.\n\n" + record
}

func compareURL(remote, base, branch string) string {
	remote = strings.TrimSuffix(remote, ".git")
	if strings.HasPrefix(remote, "git@github.com:") {
		remote = "https://github.com/" + strings.TrimPrefix(remote, "git@github.com:")
	}
	if strings.HasPrefix(remote, "https://github.com/") {
		return remote + "/compare/" + base + "..." + branch
	}
	return ""
}

var _ = config.Project{}
