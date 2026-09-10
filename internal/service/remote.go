package service

import (
	"context"
	"encoding/json"
	"errors"
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
	Remote     string `json:"remote,omitempty"`
	Branch     string `json:"branch"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	CompareURL string `json:"compare_url,omitempty"`
	PRURL      string `json:"pr_url,omitempty"`
	Body       string `json:"body,omitempty"`
}

// missingRemoteError is different from a failed remote operation: there was
// nowhere to attempt one. Automatic publication treats it as a local-only
// outcome, while explicit Push/Pull/PR requests still return the precise
// configuration error to their caller.
type missingRemoteError struct{ name string }

func (e *missingRemoteError) Error() string {
	if e.name == "" {
		return "remote action refused: no [remote] is configured"
	}
	return fmt.Sprintf("remote action refused: no remote '%s' in this repository", e.name)
}

func localOnlyDetail(name string) string {
	if name == "" {
		return "committed locally; no remote configured"
	}
	return fmt.Sprintf("committed locally; no remote '%s' in this repository", name)
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
		return "", &missingRemoteError{}
	}
	if _, err := p.git.RemoteURL(name); err != nil {
		return "", &missingRemoteError{name: name}
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

// publishAccept publishes an accepted commit under the project's on_accept
// policy (B-266). It runs AFTER the landing and checkout sync have made the
// commit durable, and NEVER contaminates the acceptance: on failure the accept
// stands and the run carries a worded warning naming the commit and the push
// failure, with the existing push door as the retry. The durable audit line is
// written by the Push service itself; the run additionally carries its receipt
// so a client can show live-or-live-soon without re-reading the audit file.
func (s *Service) publishAccept(ctx context.Context, rs *runState) {
	switch s.onAcceptPolicy(rs) {
	case "push":
		s.publishAcceptPush(ctx, rs)
	case "pr":
		s.publishAcceptPR(ctx, rs)
	}
}

func (s *Service) publishAcceptPush(ctx context.Context, rs *runState) {
	p, err := s.remoteProject(rs.run.ProjectID)
	if err != nil {
		s.recordPublishFailure(rs, "push", "", err)
		s.persistPublishResult(rs)
		return
	}
	// Push the configured default branch explicitly, never the current checkout
	// branch: an accept lands on the default branch, so that is what publication
	// must publish — otherwise a checkout parked on ducklab/<task> would push the
	// feature branch instead of the line of record (B-266).
	branch := s.baseBranchForPush(p)
	out, pushErr := s.Push(ctx, rs.run.ProjectID, RemoteRequest{Actor: rs.run.Resolution, Branch: branch})
	if pushErr != nil {
		var missing *missingRemoteError
		if errors.As(pushErr, &missing) {
			s.recordLocalOnly(rs, p, "push", branch, missing.name)
			s.persistPublishResult(rs)
			return
		}
		s.recordPublishFailure(rs, "push", branch, pushErr)
		s.persistPublishResult(rs)
		return
	}
	if out != nil {
		s.recordPublishReceipt(rs, *out)
	}
	s.persistPublishResult(rs)
}

// publishAcceptPR is the pr policy (B-289): the default branch stays local; the
// run's own branch, pointed at the landed commit, is pushed and a pull request
// into the configured base is opened — or, when one is already open for that
// branch, updated by the push. The card's consequence line promises exactly
// this, and until now the policy returned without doing anything.
func (s *Service) publishAcceptPR(ctx context.Context, rs *runState) {
	p, err := s.remoteProject(rs.run.ProjectID)
	if err != nil {
		s.recordPublishFailure(rs, "pr", "", err)
		s.persistPublishResult(rs)
		return
	}
	branch := rs.run.Branch
	if branch == "" {
		if rs.run.TaskID != "" {
			branch = "ducklab/" + rs.run.TaskID
		} else {
			branch = "ducklab/" + rs.run.ID
		}
	}
	if !p.git.BranchExists(branch) || !p.git.BranchContains(branch, rs.run.CommitSHA) {
		if err := p.git.SetBranch(branch, rs.run.CommitSHA); err != nil {
			s.recordPublishFailure(rs, "pr", branch, fmt.Errorf("point %s at %s: %w", branch, short(rs.run.CommitSHA), err))
			s.persistPublishResult(rs)
			return
		}
	}
	out, prErr := s.PR(ctx, rs.run.ProjectID, RemoteRequest{Actor: rs.run.Resolution, Branch: branch, Title: acceptCommitSubject(rs.run)})
	if prErr != nil {
		var missing *missingRemoteError
		if errors.As(prErr, &missing) {
			s.recordLocalOnly(rs, p, "pr", branch, missing.name)
			s.persistPublishResult(rs)
			return
		}
		s.recordPublishFailure(rs, "pr", branch, prErr)
		s.persistPublishResult(rs)
		return
	}
	if out != nil {
		s.recordPublishReceipt(rs, *out)
	}
	s.persistPublishResult(rs)
}

// recordLocalOnly records that an on_accept policy had no configured git
// destination. This is information, not a failed attempt: no warning and no
// failed push receipt means next() cannot offer a retry that can never work.
func (s *Service) recordLocalOnly(rs *runState, p *projectState, action, branch, remote string) {
	out := RemoteResult{
		Action: action, Actor: rs.run.Resolution, Remote: remote, Branch: branch,
		Status: "local_only", Detail: localOnlyDetail(remote),
	}
	s.recordPublishReceipt(rs, out)
	s.writeRemoteReceipt(p, out)
}

// recordPublishReceipt stores a successful publication receipt onto the run so
// clients can show the accepted commit is live on the remote.
func (s *Service) recordPublishReceipt(rs *runState, out RemoteResult) {
	raw, _ := json.Marshal(out)
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) == nil {
		rs.run.RemoteReceipts = append(rs.run.RemoteReceipts, m)
	}
}

// recordPublishFailure keeps the acceptance and records the exact state with
// the push door as the retry: the accepted commit is durable, it just did not
// reach the remote, and the person may push it by hand.
func (s *Service) recordPublishFailure(rs *runState, action, branch string, err error) {
	rs.run.Warning = fmt.Sprintf("committed as %s; %s failed: %v", rs.run.CommitSHA, action, err)
	receipt := map[string]interface{}{
		"action": action, "branch": branch, "status": "failed", "error": err.Error(),
	}
	rs.run.RemoteReceipts = append(rs.run.RemoteReceipts, receipt)
}

func publicationInfo(r *runlog.Run) string {
	for i := len(r.RemoteReceipts) - 1; i >= 0; i-- {
		receipt := r.RemoteReceipts[i]
		if receipt["status"] == "local_only" {
			if detail, ok := receipt["detail"].(string); ok {
				return detail
			}
		}
	}
	return ""
}

// persistPublishResult makes a publication's receipt or failure warning durable
// on the run, so it survives an engine restart. It never reverses the accept: a
// persistence error is reported on the run stream but must not fail acceptance
// (B-266).
func (s *Service) persistPublishResult(rs *runState) {
	if rs.writer == nil {
		if _, err := s.ensureWriter(rs); err != nil {
			rs.run.Warning = fmt.Sprintf("committed as %s; push result could not be recorded", rs.run.CommitSHA)
			return
		}
	}
	if err := rs.writer.WriteState(); err != nil {
		rs.writer.AppendEvent("warning", map[string]interface{}{
			"detail": fmt.Sprintf("publication result could not be persisted: %v", err),
		})
	}
}

// onAcceptPolicy resolves the publication policy across scopes: the project's
// own on_accept wins, then the global default, then nothing (B-266).
func (s *Service) onAcceptPolicy(rs *runState) string {
	s.cfgMu.RLock()
	g := s.cfg.Remote.OnAccept
	s.cfgMu.RUnlock()
	if p, err := s.remoteProject(rs.run.ProjectID); err == nil {
		return config.OnAcceptPolicy(g, p.cfg.Remote.OnAccept)
	}
	return config.OnAcceptPolicy(g, "")
}

// publishReleaseTag pushes a certified release tag under the same on_accept
// policy a push-configured accept uses (B-266). Called after the tag is cut; a
// completed release's tag push is best-effort and never fails the cut, BUT a
// failure is recorded in the durable audit line and surfaced as a worded error
// so the operator can see the tag did not reach the remote.
func (s *Service) publishReleaseTag(ctx context.Context, projectID, projectPath, actor, tag string) error {
	p, err := s.remoteProject(projectID)
	if err != nil {
		return nil // no readable policy — a release's tag push is best-effort
	}
	if s.onAcceptPolicyFrom(p) != "push" {
		return nil
	}
	remote := strings.TrimSpace(p.cfg.Remote.Name)
	if remote == "" {
		// No remote to push — nothing to record, the local tag stands.
		return nil
	}
	// The line of record goes first. A cut commits the notes, manifests and
	// bundle on the default branch and then tags that commit; publishing the
	// tag alone left origin/main without the release commit while the tag and
	// the GitHub release named it (v0.9.3, 2026-08-29). A tag on a commit the
	// branch does not carry is exactly the incoherent state, so when the
	// branch push fails the tag stays local and the failure is worded.
	branch := s.baseBranchForPush(p)
	if _, err := s.Push(ctx, projectID, RemoteRequest{Actor: actor, Branch: branch}); err != nil {
		var missing *missingRemoteError
		if errors.As(err, &missing) {
			return nil
		}
		return fmt.Errorf("release %s cut; %s push failed, tag kept local: %w", tag, branch, err)
	}
	receipt := RemoteResult{Action: "push", Actor: actor, Branch: tag, Status: "pushed"}
	if err := p.git.PushTag(remote, tag); err != nil {
		receipt.Status = "failed"
		s.writeRemoteReceipt(p, receipt)
		return fmt.Errorf("release %s cut; tag push failed: %w", tag, err)
	}
	s.writeRemoteReceipt(p, receipt)
	return nil
}

// onAcceptPolicyFrom resolves the policy for an already-loaded project state.
func (s *Service) onAcceptPolicyFrom(p *projectState) string {
	s.cfgMu.RLock()
	g := s.cfg.Remote.OnAccept
	s.cfgMu.RUnlock()
	return config.OnAcceptPolicy(g, p.cfg.Remote.OnAccept)
}

// baseBranchForPush resolves the branch publication should push — the exact
// line of record acceptance itself advances (origin/HEAD, else main/master,
// else the current branch), never the configured Git.BaseBranch (which
// acceptance ignores) and never an unrelated feature or worktree checkout the
// person may be parked on. Resolving through the same default-branch mechanism
// acceptance uses is what guarantees a configured BaseBranch differing from
// origin/HEAD cannot publish a different line (B-266).
func (s *Service) baseBranchForPush(p *projectState) string {
	if def, err := p.git.DefaultBranchName(); err == nil && strings.TrimSpace(def) != "" {
		return def
	}
	return "main"
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
		return nil, classifyPushError(remote, err)
	}
	out := &RemoteResult{Action: "push", Actor: req.Actor, Branch: branch, Status: "pushed"}
	s.writeRemoteReceipt(p, *out)
	return out, nil
}

// classifyPushError turns git's backend-specific prose into the few recovery
// classes an operator can act on. The original error stays attached for the
// audit trail, but the first sentence no longer calls every failure
// "unavailable".
func classifyPushError(remote string, err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "non-fast-forward") ||
		strings.Contains(message, "fetch first") ||
		(strings.Contains(message, "[rejected]") && strings.Contains(message, "failed to push")) {
		return fmt.Errorf("remote %q has new commits; pull/rebase before retrying: %w", remote, err)
	}
	if strings.Contains(message, "authentication failed") ||
		strings.Contains(message, "permission denied") ||
		strings.Contains(message, "could not read username") ||
		strings.Contains(message, "could not resolve host") ||
		strings.Contains(message, "connection refused") ||
		strings.Contains(message, "connection timed out") ||
		strings.Contains(message, "network is unreachable") ||
		strings.Contains(message, "unable to access") ||
		strings.Contains(message, "does not appear to be a git repository") ||
		strings.Contains(message, "repository not found") {
		return fmt.Errorf("remote %q is unreachable; check its URL, network, and authentication: %w", remote, err)
	}
	return fmt.Errorf("push to remote %q failed: %w", remote, err)
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
			// Opens OR updates: a pull request already open for this branch was
			// updated by the push above; creating a second one would fail and
			// fall through to a compare URL that misdescribes the state.
			if url, ok := existingPullRequest(ctx, gh, p.git.Root, branch); ok {
				push.Status = "updated"
				push.PRURL = url
				s.writeRemoteReceipt(p, *push)
				return push, nil
			}
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
	turnCaps := s.resolveTurnCaps("release", 0)
	res, err := strategy.ExecuteScript(runCtx, strategy.ReleaseScript(), &strategy.ExecuteParams{LiveToolEvents: true, ProjectRoot: p.git.Root, Prompt: prScribePrompt(title, record), Runner: s.runnerFor(cache, roster, ectx), Roster: roster, TurnCaps: turnCaps.Caps, TurnCapSources: turnCaps.Sources, OnEvent: func(kind string, data map[string]interface{}) { writer.AppendEvent(kind, data) }})
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

// existingPullRequest asks gh for an open pull request whose head is branch.
func existingPullRequest(ctx context.Context, gh, dir, branch string) (string, bool) {
	cmd := exec.CommandContext(ctx, gh, "pr", "list", "--head", branch, "--state", "open", "--json", "url", "--limit", "1")
	cmd.Dir = dir
	raw, err := cmd.Output()
	if err != nil {
		return "", false
	}
	var prs []struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &prs) != nil || len(prs) == 0 || prs[0].URL == "" {
		return "", false
	}
	return prs[0].URL, true
}
