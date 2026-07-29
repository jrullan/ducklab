package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/review"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/tools"
	"github.com/jrullan/ducklab/internal/vcs"
)

// ReviewRequest asks for one task's accepted work to be reviewed.
type ReviewRequest struct {
	TaskID string `json:"task_id"`
	// Mode is solo or council (05 §1). Solo is one reviewer; council adds a
	// second opinion and a human turn.
	Mode string `json:"mode"`
}

// ReviewStart reviews the diff a task's accepted run committed.
//
// The subject is the accepted commit, not the working tree. A review of
// whatever happens to be uncommitted right now would be a review of a moment,
// and the record would not say which one.
func (s *Service) ReviewStart(ctx context.Context, projectID string, req ReviewRequest) (*runlog.Run, error) {
	if strings.TrimSpace(req.TaskID) == "" {
		return nil, fmt.Errorf("review: no task given")
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	if err := checkRunnable(entry.Path); err != nil {
		return nil, err
	}

	sha, runID, err := s.acceptedCommitFor(ctx, projectID, req.TaskID)
	if err != nil {
		return nil, err
	}
	diff, err := vcs.New(entry.Path).ShowCommit(sha)
	if err != nil {
		return nil, fmt.Errorf("review %s: %w", req.TaskID, err)
	}
	if strings.TrimSpace(diff) == "" {
		// An empty diff is not a review that approves; it is nothing to review.
		return nil, fmt.Errorf("review %s: commit %s changed nothing", req.TaskID, short(sha))
	}

	mode := req.Mode
	if mode == "" {
		mode = "solo"
	}
	run := &runlog.Run{
		ID:        runlog.GenerateRunID(),
		ProjectID: projectID,
		Stage:     "review",
		Mode:      mode,
		TaskID:    req.TaskID,
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		CommitSHA: sha,
		Stream:    true,
		// A review runs nothing, so it has no executable gate. The verdict is
		// the reviewer's, and the human decides what to do about it (P3).
		Gate: "none",
	}
	writer, err := runlog.NewWriter(entry.Path, run)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	rs := &runState{
		run: run, writer: writer, runDir: writer.RunDir(),
		projectPath: entry.Path, cancel: cancel, done: make(chan struct{}),
	}
	s.attachWriter(rs, writer)
	s.runsMu.Lock()
	s.runs[run.ID] = rs
	s.runsMu.Unlock()

	writer.AppendEvent("run_start", map[string]interface{}{
		"stage": "review", "mode": mode, "task_id": req.TaskID, "commit": short(sha),
	})

	go s.executeReview(runCtx, rs, entry.Path, req, diff, runID)
	return run, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// executeReview runs the reviewer and files what it said.
func (s *Service) executeReview(ctx context.Context, rs *runState, projectRoot string, req ReviewRequest, diff, sourceRun string) {
	defer close(rs.done)
	defer rs.writer.Close()

	projCfg, err := config.LoadProject(filepath.Join(projectRoot, ".ducklab", "project.toml"))
	if err != nil {
		s.failRun(rs, fmt.Errorf("load project config: %w", err))
		return
	}
	roster, warning := s.resolveRoster(projCfg)
	rs.run.Roster = rosterStrings(roster)
	if warning != "" {
		rs.run.Warning = warning
		rs.writer.AppendEvent("warning", map[string]interface{}{"detail": warning})
	}
	tracker := budget.NewTracker(&budget.Budget{
		MaxUSD:        projCfg.Budget.MaxUSD,
		MaxTokens:     int64(s.cfg.Defaults.Budget.MaxTokens),
		MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS,
		MaxTurns:      s.cfg.Defaults.Budget.MaxTurns,
	})
	ectx := &tools.ExecContext{ProjectRoot: projectRoot, RunID: rs.run.ID}
	cache := &loopCache{
		svc: s, tracker: tracker,
		writer: &runLogAdapter{w: rs.writer},
		loops:  map[config.DucklingID]*agent.Loop{},
	}

	params := &strategy.ExecuteParams{
		ProjectRoot: projectRoot,
		TaskID:      req.TaskID,
		Prompt:      reviewPrompt(req.TaskID, diff),
		Runner:      s.runnerFor(cache, roster, ectx),
		Roster:      roster,
		Diff:        func() (string, error) { return diff, nil },
		OnEvent: func(kind string, data map[string]interface{}) {
			rs.writer.AppendEvent(kind, data)
		},
	}

	res, err := strategy.ExecuteScript(ctx, strategy.ReviewScript(rs.run.Mode == "council"), params)
	recordSpend(rs, tracker)
	if err != nil {
		s.failRun(rs, pendingOrErr(res, err))
		return
	}

	var verdict *agent.Verdict
	if res != nil && res.Outcome != nil {
		verdict, _ = res.Outcome.Parsed.(*agent.Verdict)
	}
	path, werr := writeReview(projectRoot, review.Record{
		TaskID:    req.TaskID,
		Verdict:   verdict,
		RunID:     sourceRun,
		CommitSHA: rs.run.CommitSHA,
		Mode:      rs.run.Mode,
		Ducklings: []string{string(roster[config.RoleReviewer])},
		At:        time.Now(),
	})
	if werr != nil {
		s.failRun(rs, werr)
		return
	}

	rs.run.Verdict = "UNVERIFIED"
	rs.writer.AppendEvent("review_written", map[string]interface{}{
		"path": path, "verdict": verdictWord(verdict), "findings": findingCount(verdict),
	})

	// The reviewer's verdict is not the decision. A person reads the review and
	// decides what happens to the task (05 §1).
	rs.run.Status = "paused"
	rs.run.PendingKind = "gate"
	rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
	rs.run.PendingData = map[string]interface{}{
		"review": path, "verdict": verdictWord(verdict),
	}
	rs.writer.AppendEvent("human_needed", map[string]interface{}{
		"kind": "gate", "verdict": verdictWord(verdict), "review": path,
	})
	rs.writer.WriteState()
}

func verdictWord(v *agent.Verdict) string {
	if v == nil || v.Verdict == "" {
		return "unreviewed"
	}
	return v.Verdict
}

func findingCount(v *agent.Verdict) int {
	if v == nil {
		return 0
	}
	return len(v.Findings)
}

// reviewPrompt gives the reviewer the diff and nothing else.
//
// No implementer transcript, by design (I7): a reviewer that reads the author's
// reasoning adopts it, and stops being a second reading.
func reviewPrompt(taskID, diff string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Review %s\n\n", taskID)
	b.WriteString("This change has already been accepted and committed. Read it and report what you find.\n\n")
	b.WriteString("## The diff\n\n```diff\n")
	b.WriteString(strings.TrimRight(diff, "\n"))
	b.WriteString("\n```\n")
	return b.String()
}

// acceptedCommitFor finds the commit a task's work landed in.
//
// Newest accepted run wins. A task re-run after a review is reviewed on what it
// became, not on what it was the first time.
func (s *Service) acceptedCommitFor(ctx context.Context, projectID, taskID string) (sha, runID string, err error) {
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return "", "", err
	}
	for _, r := range runs { // RunList answers newest first
		if r.TaskID == taskID && r.Accepted && r.CommitSHA != "" {
			return r.CommitSHA, r.ID, nil
		}
	}
	return "", "", fmt.Errorf("review %s: no accepted run to review; run and accept the task first", taskID)
}

// writeReview records what a reviewer said about a task.
func writeReview(projectRoot string, rec review.Record) (string, error) {
	if err := os.MkdirAll(review.Dir(projectRoot), 0o755); err != nil {
		return "", err
	}
	path := review.Path(projectRoot, rec.TaskID)
	if err := os.WriteFile(path, []byte(review.Render(rec)), 0o644); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return path, nil
	}
	return rel, nil
}

// ReviewSummary is one filed review, enough to list without reading the body.
type ReviewSummary struct {
	TaskID     string `json:"task_id"`
	Verdict    string `json:"verdict"`
	Findings   int    `json:"findings"`
	CommitSHA  string `json:"commit,omitempty"`
	Mode       string `json:"mode,omitempty"`
	ReviewedAt string `json:"reviewed_at,omitempty"`
}

// ReviewList returns the reviews on file, newest first.
func (s *Service) ReviewList(ctx context.Context, projectID string) ([]ReviewSummary, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	names, err := os.ReadDir(review.Dir(entry.Path))
	if os.IsNotExist(err) {
		// A project nobody has reviewed has no directory, and that is not an
		// error — it is the answer.
		return []ReviewSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]ReviewSummary, 0, len(names))
	for _, n := range names {
		if n.IsDir() || !strings.HasSuffix(n.Name(), ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(review.Dir(entry.Path), n.Name()))
		if err != nil {
			continue
		}
		out = append(out, summariseReview(strings.TrimSuffix(n.Name(), ".md"), string(body)))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ReviewedAt != out[j].ReviewedAt {
			return out[i].ReviewedAt > out[j].ReviewedAt
		}
		return out[i].TaskID < out[j].TaskID
	})
	return out, nil
}

// ReviewGet returns one review's markdown.
func (s *Service) ReviewGet(ctx context.Context, projectID, taskID string) (string, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(review.Path(entry.Path, taskID))
	if os.IsNotExist(err) {
		return "", fmt.Errorf("no review of %s; run `ducklab review %s`", taskID, taskID)
	}
	return string(body), err
}

// summariseReview reads the frontmatter a review wrote about itself.
//
// Parsed from the document rather than tracked in a second index: the file is
// the record, and an index beside it is one more thing that can disagree with
// what is on disk.
func summariseReview(taskID, body string) ReviewSummary {
	sum := ReviewSummary{TaskID: taskID}
	for _, line := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			if strings.HasPrefix(line, "## ") {
				sum.Findings++
			}
			continue
		}
		switch strings.TrimSpace(k) {
		case "verdict":
			sum.Verdict = strings.TrimSpace(v)
		case "commit":
			sum.CommitSHA = strings.TrimSpace(v)
		case "mode":
			sum.Mode = strings.TrimSpace(v)
		case "reviewed_at":
			sum.ReviewedAt = strings.TrimSpace(v)
		}
	}
	return sum
}
