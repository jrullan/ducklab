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
	From string `json:"from"`
	// Rounds overrides the script's own limit. Zero means the script decides,
	// which is two for a council.
	Rounds int `json:"rounds"`
	// Revise is what to change about the draft already on the table. Set only
	// when revising: it turns the run from "write this document" into "edit
	// this one", which is the answer to a proposal that is almost right.
	Revise   string `json:"revise"`
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

	// Recorded as what will actually run, not as a constant. A report that
	// says every stage was a council when half were solo is a report that
	// cannot answer the question it exists for.
	mode := req.Mode
	if mode == "" {
		mode = "council"
	}
	run := &runlog.Run{
		ID:        runlog.GenerateRunID(),
		ProjectID: projectID,
		Stage:     req.Stage,
		Mode:      mode,
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

	writer.AppendEvent("run_start", map[string]interface{}{"stage": req.Stage, "mode": mode})

	go s.executeStage(runCtx, rs, entry.Path, req)
	return run, nil
}

func (s *Service) executeStage(ctx context.Context, rs *runState, projectRoot string, req StageRequest) {
	defer recoverRun(rs)
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
		// Kept as its own file. Comparing requirements against what was asked
		// for is the first thing anyone does with them, and the brief was
		// reachable only by digging it out of a prompt in llm.jsonl.
		rs.writer.WriteBrief(seed)
		// A --from that is not a readable path is treated as the brief text
		// itself: a user pasting a sentence should not have to make a file.
	}

	roster, warning := s.resolveRoster(projCfg)
	rs.run.Roster = rosterStrings(roster)
	if warning != "" {
		rs.run.Warning = warning
		rs.writer.AppendEvent("warning", map[string]interface{}{"detail": warning})
	}

	limits := &budget.Budget{
		MaxUSD:        projCfg.Budget.MaxUSD,
		MaxTokens:     int64(s.cfg.Defaults.Budget.MaxTokens),
		MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS,
		MaxTurns:      s.cfg.Defaults.Budget.MaxTurns,
	}
	tracker := budget.NewTracker(limits)
	recordLimits(rs, limits)
	rs.tracker = tracker
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
	s.attachStreaming(rs, cache)

	result, err := stage.Run(ctx, stage.Params{
		ProjectRoot: projectRoot,
		Stage:       stage.Name(req.Stage),
		RunID:       rs.run.ID,
		Seed:        seed,
		Mode:        req.Mode,
		Rounds:      s.roundsFor(rs.run.Mode, req.Rounds),
		Revision:    req.Revise,
		Ducklings:   ducklingList(roster),
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			res, err := strategy.ExecuteScript(ctx, s.applyRoleTurns(script), &strategy.ExecuteParams{
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
		// Which run produced this. Carried so a client can go and fetch what
		// that run was asked for; the proposal already said it and the
		// accepted document did not, so the brief vanished on acceptance.
		"run_id": current.Front.RunID,
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
	// Read the proposal before promoting: Promote removes the file, and its
	// frontmatter is where the producing run's id lives.
	runID := ""
	if proposed, _ := artifact.LoadProposed(entry.Path, artifact.Kind(kind)); proposed != nil {
		runID = proposed.Front.RunID
	}
	if _, err := artifact.Promote(entry.Path, artifact.Kind(kind), approvedBy); err != nil {
		return nil, err
	}
	// Close the run that produced it. Promoting answered its gate, and a run
	// left paused on a question already decided sits in the inbox forever:
	// three of them had accumulated on the timesheet project, each still
	// claiming to be waiting for an answer that had been given hours before.
	s.resolveStageRun(runID, approvedBy)
	// The trace check runs on promotion, not on demand: an artifact accepted
	// into a broken spine should say so immediately, while the person who
	// accepted it is still looking.
	res, err := s.TraceCheck(ctx, projectID)
	if err != nil {
		return nil, err
	}
	// The proposal has just been consumed, so this reads approved artifacts —
	// which is what a report about what was accepted should read.
	return map[string]interface{}{"promoted": kind, "trace_errors": res.Errors}, nil
}

// resolveStageRun marks the run behind an accepted artifact as finished.
//
// It is deliberately not RunAccept: that path commits a diff, and a document
// stage produced no diff to commit. What it owes is the gate — clear the
// pending block, record who decided, and let the run leave the inbox.
func (s *Service) resolveStageRun(runID, approvedBy string) {
	if runID == "" {
		return
	}
	s.runsMu.RLock()
	rs, ok := s.runs[runID]
	s.runsMu.RUnlock()
	if !ok || rs.run.Status != "paused" {
		return
	}
	rs.run.Status = "done"
	rs.run.Accepted = true
	rs.run.Resolution = "accepted by " + approvedBy
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	clearPending(rs.run)
	// ensureWriter attaches the bus hook, so appending here is what tells a
	// connected desktop the run has left the inbox — no refresh needed.
	if w, err := s.ensureWriter(rs); err == nil {
		w.AppendEvent("gate_resolved", map[string]interface{}{
			"resolution": "accepted", "by": approvedBy,
		})
		w.WriteState()
	}
}

// TraceResult is a spine check and what it was run against.
type TraceResult struct {
	Errors []artifact.TraceError `json:"errors"`
	// Proposed names the stages whose pending proposal was checked instead of
	// the approved artifact. Empty means every stage was the approved one.
	Proposed []string `json:"proposed,omitempty"`
}

// TraceCheck walks the spine. Deterministic and model-free.
//
// A pending proposal stands in for the artifact it would replace, so the check
// describes the document you are about to accept rather than the one you
// accepted last week. That is the only moment it can change a decision.
func (s *Service) TraceCheck(ctx context.Context, projectID string) (*TraceResult, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	spine, proposed, err := artifact.LoadSpinePending(entry.Path)
	if err != nil {
		return nil, err
	}
	errs := spine.Check()
	if errs == nil {
		errs = []artifact.TraceError{}
	}
	out := &TraceResult{Errors: errs}
	for _, k := range proposed {
		out.Proposed = append(out.Proposed, string(k))
	}
	return out, nil
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
	// Blocked says why, in one sentence, when Status is "blocked". A column
	// that shows work stopped without saying what stopped it is a column that
	// sends you reading run logs.
	Blocked string `json:"blocked,omitempty"`
	// Next are the actions a person may legally start from this task, in the
	// order a client should offer them (docs/ux-evaluation.md §5.4).
	Next []string `json:"next,omitempty"`
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
	// The gate mode decides whether test-first is even offerable here.
	gateMode := ""
	if projCfg, cfgErr := config.LoadProject(entry.Path + "/.ducklab/project.toml"); cfgErr == nil {
		gateMode = projCfg.Verify.Mode
	}

	plan, err := artifact.Load(entry.Path, artifact.KindPlan)
	if err != nil {
		return nil, err
	}
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}

	// A task's status is its MOST RECENT run, and RunList answers newest
	// first, so the first run seen for a task wins.
	//
	// This used to assign unconditionally on every branch, letting an older
	// run overwrite a newer one: a task accepted this morning went back to
	// "in progress" because a run from last week appeared later in the list.
	// While RunList ranged over a map it was worse than wrong, it was
	// unstable — the same board could show two different columns on two
	// consecutive loads.
	status := map[string]string{}
	blocked := map[string]string{}
	// Whether TaskRemove would refuse: an accepted run pins its task for good,
	// an open one until it is decided. Tracked here so the offered action and
	// the refusal can never disagree.
	pinned := map[string]bool{}
	for _, r := range runs {
		if r.TaskID == "" {
			continue
		}
		if r.Accepted || r.Status == "running" || r.Status == "queued" || r.Status == "paused" {
			pinned[r.TaskID] = true
		}
		if status[r.TaskID] != "" {
			continue
		}
		switch {
		case r.Accepted:
			status[r.TaskID] = "accepted"
		case r.Status == "running" || r.Status == "queued":
			status[r.TaskID] = "in_progress"
		case r.Status == "paused":
			status[r.TaskID] = "review"
		default:
			// Failed and aborted used to land back in "todo", where a task that
			// had been tried and broken looked exactly like one nobody had
			// touched. The board could not tell you the difference.
			status[r.TaskID] = "blocked"
			blocked[r.TaskID] = "the last run " + r.Status + " — read it, then retry or change the task"
		}
	}

	var out []TaskView
	for _, m := range plan.Sections {
		for _, t := range m.Children {
			st := status[t.ID]
			if st == "" {
				st = "todo"
			}
			deps := splitList(t.Field("depends on"))
			// A task whose dependencies are not accepted cannot be started, and
			// the plan has said so all along — TaskNext has been reading this
			// since it was written. The board just never showed it, so the only
			// way to learn a task was not ready was to run it and watch a model
			// invent the thing it depended on.
			if st == "todo" {
				if waiting := unmetDeps(deps, status); len(waiting) > 0 {
					st = "blocked"
					blocked[t.ID] = "waiting on " + strings.Join(waiting, ", ")
				}
			}
			out = append(out, TaskView{
				ID: t.ID, Title: t.Title, Milestone: m.ID,
				Implements: t.Implements,
				Complexity: t.Field("complexity"),
				DependsOn:  deps,
				Status:     st,
				Body:       t.Body,
				Blocked:    blocked[t.ID],
				Next:       taskNextActions(st, gateMode, !pinned[t.ID]),
			})
		}
	}
	return out, nil
}

// unmetDeps returns the dependencies that are not accepted yet, in the order
// the task listed them. A dependency naming a task that does not exist counts
// as unmet: a typo in the plan should stop the work, not quietly permit it.
func unmetDeps(deps []string, status map[string]string) []string {
	var waiting []string
	for _, dep := range deps {
		if status[dep] != "accepted" {
			waiting = append(waiting, dep)
		}
	}
	return waiting
}

// TaskNext returns the first task ready to be started.
//
// The dependency check used to live here, duplicated from nothing — it was the
// only place in the product that knew a task could be waiting on another. Now
// TaskList marks those blocked, so "todo" already means ready, and a task whose
// last run failed is blocked rather than silently offered again as if it were
// fresh work.
func (s *Service) TaskNext(ctx context.Context, projectID string) (*TaskView, error) {
	tasks, err := s.TaskList(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		if tasks[i].Status == "todo" {
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
			// Who else delivers these sections, and therefore what is not this
			// task's to write.
			//
			// This used to say "This task delivers SPEC-003" and then hand over
			// the whole section. Every section in a real plan is delivered by
			// two to five tasks — one project had five on SPEC-002 — so the
			// sentence was false and the model reasonably read the section as
			// its scope. Twice in one session a task implemented its sibling's
			// work as well, the gate went green because the code was correct,
			// and the next run found nothing left to do.
			b.WriteString(scopeNote(task.Implements, s.siblingTasks(ctx, projectID, taskID, task.Implements)))
			if spec := s.specSections(projectRoot, task.Implements); spec != "" {
				// "contributes to", not "delivers": the heading was half the
				// instruction the model was following.
				b.WriteString("\n## The specification this task contributes to\n\n" + spec)
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

// scopeNote tells a task what is its part of a spec section, and what is not.
//
// The wording is the whole mechanism. It used to say "This task delivers
// SPEC-003" and then hand over the whole section, and every section in a real
// plan is delivered by several tasks — one project had five on SPEC-002 — so
// the sentence was false and the model reasonably read the section as its
// scope. Twice in one session a task implemented its sibling's work as well;
// the gate went green because the code was correct, and the next run found
// nothing left to do.
func scopeNote(implements []string, siblings []TaskView) string {
	if len(siblings) == 0 {
		return fmt.Sprintf("\nThis task delivers %s.\n", strings.Join(implements, ", "))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nThis task delivers **part of** %s. "+
		"The rest of those sections belongs to other tasks:\n\n", strings.Join(implements, ", "))
	for _, sib := range siblings {
		fmt.Fprintf(&b, "- %s — %s\n", sib.ID, sib.Title)
	}
	// A real dependency needs an answer other than "do all of it", or the
	// model will do all of it — which is exactly what happened.
	b.WriteString("\nDo not implement those. Work another task has been given is work that will " +
		"be done twice or not at all, and either way the plan stops meaning anything. If your " +
		"part genuinely cannot stand without something listed above, write the smallest thing " +
		"that lets yours work and say so in your summary — do not deliver the whole of it.\n")
	return b.String()
}

// siblingTasks are the other tasks that deliver any of the same spec sections.
//
// They are what this task must not implement. Named rather than merely
// excluded: "do not do more than your task" is advice a model cannot check
// itself against, and "T-004 does the geometry calculations" is a fact it can.
func (s *Service) siblingTasks(ctx context.Context, projectID, taskID string, implements []string) []TaskView {
	tasks, err := s.TaskList(ctx, projectID)
	if err != nil {
		return nil
	}
	mine := map[string]bool{}
	for _, id := range implements {
		mine[strings.ToUpper(strings.TrimSpace(id))] = true
	}
	var out []TaskView
	for _, t := range tasks {
		if strings.EqualFold(t.ID, taskID) {
			continue
		}
		// Work already accepted is not a warning, it is the tree the task is
		// being written against — and the model can read it there.
		if t.Status == "accepted" {
			continue
		}
		for _, id := range t.Implements {
			if mine[strings.ToUpper(strings.TrimSpace(id))] {
				out = append(out, t)
				break
			}
		}
	}
	return out
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

// artifactKindForStage maps a stage to the document its run proposes.
//
// Empty for stages that write no artifact: a build run's human gate is about
// code, and promoting something there would be inventing a decision nobody
// made.
func artifactKindForStage(stage string) string {
	switch stage {
	case "intake":
		return "requirements"
	case "spec":
		return "spec"
	case "plan":
		return "plan"
	}
	return ""
}
