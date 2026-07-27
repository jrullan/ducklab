package service

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"errors"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/stage"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/tools"
)

// StageRequest starts an artifact stage.
type StageRequest struct {
	Stage string `json:"stage"`
	Mode  string `json:"mode"`
	// From seeds intake with an existing document instead of interviewing.
	From     string `json:"from"`
	Autonomy string `json:"autonomy"`
	Stream   bool   `json:"stream"`
}

// StageStart runs intake, spec or plan and leaves a proposal for the human.
//
// Returns immediately with a running run, like every other long operation: the
// desktop watches the council converse rather than blocking on a request.
func (s *Service) StageStart(ctx context.Context, projectID string, req StageRequest) (*runlog.Run, error) {
	if !stage.Valid(req.Stage) {
		return nil, fmt.Errorf("unknown stage %q (available: intake, spec, plan)", req.Stage)
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}

	run := &runlog.Run{
		ID:        runlog.GenerateRunID(),
		ProjectID: projectID,
		Stage:     req.Stage,
		Mode:      "council",
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Autonomy:  orDefault(req.Autonomy, "guarded"),
		Stream:    req.Stream,
		// An artifact stage has no executable gate: the verdict is UNVERIFIED
		// until a person approves it, and saying so is the honest label (P3).
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

	writer.AppendEvent("run_start", map[string]interface{}{"stage": req.Stage, "mode": "council"})

	go s.executeStage(runCtx, rs, entry.Path, req)
	return run, nil
}

func (s *Service) executeStage(ctx context.Context, rs *runState, projectRoot string, req StageRequest) {
	defer close(rs.done)
	defer rs.writer.Close()

	projCfg, err := config.LoadProject(projectRoot + "/.ducklab/project.toml")
	if err != nil {
		s.failRun(rs, fmt.Errorf("load project config: %w", err))
		return
	}

	seed := req.From
	if seed != "" {
		if data, err := os.ReadFile(seed); err == nil {
			seed = string(data)
		}
		// A --from that is not a readable path is treated as the brief text
		// itself: a user pasting a sentence should not have to make a file.
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
	ectx := &tools.ExecContext{
		ProjectRoot: projectRoot,
		RunID:       rs.run.ID,
		Autonomy:    config.Autonomy(rs.run.Autonomy),
		ShellPolicy: projCfg.Shell,
		Answers:     rs.answers(),
	}
	cache := &loopCache{
		svc: s, tracker: tracker,
		writer: &runLogAdapter{w: rs.writer},
		loops:  map[config.DucklingID]*agent.Loop{},
	}

	result, err := stage.Run(ctx, stage.Params{
		ProjectRoot: projectRoot,
		Stage:       stage.Name(req.Stage),
		RunID:       rs.run.ID,
		Seed:        seed,
		Ducklings:   ducklingList(roster),
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			res, err := strategy.ExecuteScript(ctx, script, &strategy.ExecuteParams{
				ProjectRoot: projectRoot,
				Prompt:      prompt,
				ExecContext: ectx,
				Runner:      s.runnerFor(cache, roster, ectx),
				Roster:      roster,
				OnEvent: func(kind string, data map[string]interface{}) {
					rs.writer.AppendEvent(kind, data)
				},
			})
			if err != nil {
				return "", err
			}
			return res.Text, nil
		},
	})

	recordSpend(rs, tracker)

	if err != nil {
		var pending *pendingErr
		if errors.As(err, &pending) {
			s.pauseForQuestion(rs, pending.q)
			return
		}
		s.failRun(rs, err)
		return
	}

	rs.writer.AppendEvent("proposal", map[string]interface{}{
		"kind":     string(result.Kind),
		"sections": len(result.Proposed.Sections),
		"remapped": len(result.Remapped),
	})
	// No executable gate exists for a document, so the verdict is UNVERIFIED
	// and the human gate is the only gate (P3).
	rs.run.Verdict = "UNVERIFIED"
	rs.writer.AppendEvent("verdict", map[string]interface{}{"verdict": "UNVERIFIED"})

	rs.run.Status = "paused"
	rs.run.PendingKind = "gate"
	rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
	rs.run.PendingData = map[string]interface{}{
		"artifact": string(result.Kind),
		"sections": len(result.Proposed.Sections),
	}
	rs.writer.AppendEvent("human_needed", map[string]interface{}{
		"kind": "gate", "verdict": "UNVERIFIED", "artifact": string(result.Kind),
	})
	rs.writer.WriteState()
}

// ArtifactGet returns a committed artifact and any pending proposal.
func (s *Service) ArtifactGet(ctx context.Context, projectID, kind string) (map[string]interface{}, error) {
	if !artifact.ValidKind(kind) {
		return nil, fmt.Errorf("unknown artifact %q", kind)
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	k := artifact.Kind(kind)

	current, err := artifact.Load(entry.Path, k)
	if err != nil {
		return nil, err
	}
	proposed, err := artifact.LoadProposed(entry.Path, k)
	if err != nil {
		return nil, err
	}
	diff, err := artifact.Diff(entry.Path, k)
	if err != nil {
		return nil, err
	}

	out := map[string]interface{}{
		"kind":     kind,
		"markdown": current.Raw,
		"sections": sectionViews(current.Sections),
		"version":  current.Front.Version,
		"approved": current.Front.Approved(),
	}
	if proposed != nil {
		out["proposal"] = map[string]interface{}{
			"markdown": proposed.Raw,
			"sections": sectionViews(proposed.Sections),
			"run_id":   proposed.Front.RunID,
			"diff":     diff,
		}
	}
	return out, nil
}

// ArtifactPromote accepts a pending proposal.
func (s *Service) ArtifactPromote(ctx context.Context, projectID, kind, approvedBy string) (map[string]interface{}, error) {
	if !artifact.ValidKind(kind) {
		return nil, fmt.Errorf("unknown artifact %q", kind)
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	if _, err := artifact.Promote(entry.Path, artifact.Kind(kind), approvedBy); err != nil {
		return nil, err
	}
	// The trace check runs on promotion, not on demand: an artifact accepted
	// into a broken spine should say so immediately, while the person who
	// accepted it is still looking.
	errs, err := s.TraceCheck(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"promoted": kind, "trace_errors": errs}, nil
}

// TraceCheck walks the spine. Deterministic and model-free.
func (s *Service) TraceCheck(ctx context.Context, projectID string) ([]artifact.TraceError, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	spine, err := artifact.LoadSpine(entry.Path)
	if err != nil {
		return nil, err
	}
	errs := spine.Check()
	if errs == nil {
		errs = []artifact.TraceError{}
	}
	return errs, nil
}

// TraceShow walks the spine from one id.
func (s *Service) TraceShow(ctx context.Context, projectID, id string) (*artifact.Node, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	spine, err := artifact.LoadSpine(entry.Path)
	if err != nil {
		return nil, err
	}
	return spine.Walk(id)
}

// TaskView is a task as a client sees it.
type TaskView struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Milestone  string   `json:"milestone"`
	Implements []string `json:"implements,omitempty"`
	Complexity string   `json:"complexity,omitempty"`
	DependsOn  []string `json:"depends_on,omitempty"`
	Status     string   `json:"status"`
	Body       string   `json:"body,omitempty"`
}

// TaskList reads tasks from the plan and folds in what runs have done to them.
//
// The plan is the source of truth for what the tasks ARE; run records are the
// source for their status. Keeping the status in the document would mean a
// model rewriting the plan could mark its own work accepted.
func (s *Service) TaskList(ctx context.Context, projectID string) ([]TaskView, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	plan, err := artifact.Load(entry.Path, artifact.KindPlan)
	if err != nil {
		return nil, err
	}
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}

	status := map[string]string{}
	for _, r := range runs {
		if r.TaskID == "" {
			continue
		}
		switch {
		case r.Accepted:
			status[r.TaskID] = "accepted"
		case r.Status == "running" || r.Status == "queued":
			status[r.TaskID] = "in_progress"
		case r.Status == "paused":
			status[r.TaskID] = "review"
		case status[r.TaskID] == "":
			status[r.TaskID] = "todo"
		}
	}

	var out []TaskView
	for _, m := range plan.Sections {
		for _, t := range m.Children {
			st := status[t.ID]
			if st == "" {
				st = "todo"
			}
			out = append(out, TaskView{
				ID: t.ID, Title: t.Title, Milestone: m.ID,
				Implements: t.Implements,
				Complexity: t.Field("complexity"),
				DependsOn:  splitList(t.Field("depends on")),
				Status:     st,
				Body:       t.Body,
			})
		}
	}
	return out, nil
}

// TaskNext returns the first todo task whose dependencies are all accepted.
func (s *Service) TaskNext(ctx context.Context, projectID string) (*TaskView, error) {
	tasks, err := s.TaskList(ctx, projectID)
	if err != nil {
		return nil, err
	}
	done := map[string]bool{}
	for _, t := range tasks {
		if t.Status == "accepted" {
			done[t.ID] = true
		}
	}
	for i := range tasks {
		if tasks[i].Status != "todo" {
			continue
		}
		ready := true
		for _, dep := range tasks[i].DependsOn {
			if !done[dep] {
				ready = false
				break
			}
		}
		if ready {
			return &tasks[i], nil
		}
	}
	return nil, nil
}

// --- helpers -------------------------------------------------------------------

func sectionViews(sections []artifact.Section) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(sections))
	for _, s := range sections {
		v := map[string]interface{}{
			"id": s.ID, "title": s.Title, "body": s.Body,
		}
		if len(s.Implements) > 0 {
			v["implements"] = s.Implements
		}
		if len(s.Fields) > 0 {
			v["fields"] = s.Fields
		}
		if len(s.Children) > 0 {
			v["children"] = sectionViews(s.Children)
		}
		out = append(out, v)
	}
	return out
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ducklingList(roster map[config.Role]config.DucklingID) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range roster {
		if id != "" && !seen[string(id)] {
			seen[string(id)] = true
			out = append(out, string(id))
		}
	}
	return out
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// buildTaskPrompt assembles what an implementer is asked for a build run.
//
// Two pieces of context that a fresh prompt would lose: the project note,
// injected into every turn, and what previous runs on THIS task already tried
// and failed. A small model re-run on the same task repeats the same dead end,
// so naming the dead ends is the cheapest correction available (04 §1.5).
func (s *Service) buildTaskPrompt(ctx context.Context, projectID, projectRoot, taskID string) string {
	var b strings.Builder

	if m, err := artifact.LoadMemory(projectRoot); err == nil {
		if mc := m.PromptContext(); mc != "" {
			b.WriteString(mc)
			b.WriteString("\n\n")
		}
	}

	b.WriteString("## Your task\n\n")
	if task := s.findTask(ctx, projectID, taskID); task != nil {
		fmt.Fprintf(&b, "%s — %s\n", task.ID, task.Title)
		if strings.TrimSpace(task.Body) != "" {
			b.WriteString("\n" + strings.TrimSpace(task.Body) + "\n")
		}
		if len(task.Implements) > 0 {
			fmt.Fprintf(&b, "\nThis task delivers %s.\n", strings.Join(task.Implements, ", "))
			if spec := s.specSections(projectRoot, task.Implements); spec != "" {
				b.WriteString("\n## The specification it delivers\n\n" + spec)
			}
		}
	} else {
		fmt.Fprintf(&b, "Implement task %s\n", taskID)
	}

	if prior := s.failedAttempts(ctx, projectID, taskID); len(prior) > 0 {
		b.WriteString("\n")
		b.WriteString(artifact.RenderFailedAttempts(prior))
	}
	return b.String()
}

func (s *Service) findTask(ctx context.Context, projectID, taskID string) *TaskView {
	tasks, err := s.TaskList(ctx, projectID)
	if err != nil {
		return nil
	}
	for i := range tasks {
		if strings.EqualFold(tasks[i].ID, taskID) {
			return &tasks[i]
		}
	}
	return nil
}

func (s *Service) specSections(projectRoot string, ids []string) string {
	spec, err := artifact.Load(projectRoot, artifact.KindSpec)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, id := range ids {
		if sec := spec.Section(id); sec != nil {
			fmt.Fprintf(&b, "### %s — %s\n%s\n\n", sec.ID, sec.Title, strings.TrimSpace(sec.Body))
		}
	}
	return b.String()
}

// failedAttempts summarises prior failed runs on a task, newest first, capped
// at three: the point is to name the dead ends, not to replay the history.
func (s *Service) failedAttempts(ctx context.Context, projectID, taskID string) []artifact.FailedAttempt {
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return nil
	}
	var failed []*runlog.Run
	for _, r := range runs {
		if r.TaskID == taskID && r.Verdict == "FAILED" {
			failed = append(failed, r)
		}
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].StartedAt > failed[j].StartedAt })
	if len(failed) > 3 {
		failed = failed[:3]
	}

	out := make([]artifact.FailedAttempt, 0, len(failed))
	for _, r := range failed {
		out = append(out, artifact.FailedAttempt{
			RunID: r.ID, Mode: r.Mode,
			Summary: s.runSummary(r),
			Gate:    s.gateSummary(r),
		})
	}
	return out
}

// runSummary is derived from the run's own record, never by asking a model to
// recall what it did.
func (s *Service) runSummary(r *runlog.Run) string {
	events, err := runlog.ReadEvents(s.RunDir(r.ID))
	if err != nil {
		return "no summary recorded"
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "error" {
			if msg, ok := events[i].Data["error"].(string); ok {
				return msg
			}
		}
	}
	return "the gate stayed red"
}

func (s *Service) gateSummary(r *runlog.Run) string {
	out, err := s.RunVerify(context.Background(), r.ID, 6)
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	return strings.TrimSpace(out)
}
