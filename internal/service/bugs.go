package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/stage"
	"github.com/jrullan/ducklab/internal/store"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/tools"
)

// BugRequest reports something that is broken.
type BugRequest struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Severity string `json:"severity"`
	Reporter string `json:"reporter"`
	Source   string `json:"source"`
}

// BugAdd records a report (05 §6).
//
// Severity is taken as given rather than guessed at. A reporter saying
// "critical" may be wrong, but a tool that quietly downgrades what it was told
// is a tool nobody reports to twice; triage is where that judgement belongs.
func (s *Service) BugAdd(ctx context.Context, projectID string, req BugRequest) (*bug.Bug, error) {
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("a bug needs a title")
	}
	sev := strings.ToLower(strings.TrimSpace(req.Severity))
	if sev == "" {
		sev = string(bug.Normal)
	}
	if !bug.ValidSeverity(sev) {
		return nil, fmt.Errorf("unknown severity %q, want critical, high, normal or low", req.Severity)
	}

	db, err := s.openProjectDB(projectID)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	n, err := db.NextSequence("bug", "B")
	if err != nil {
		return nil, err
	}
	rec := &store.Bug{
		ID:       fmt.Sprintf("B-%03d", n),
		Title:    strings.TrimSpace(req.Title),
		Body:     req.Body,
		Severity: sev,
		Status:   string(bug.Open),
		Source:   orDefault(req.Source, "manual"),
		Reporter: req.Reporter,
	}
	if err := db.CreateBug(rec); err != nil {
		return nil, err
	}
	return toBug(rec), nil
}

// BugList returns the project's bugs, worst first.
func (s *Service) BugList(ctx context.Context, projectID string, openOnly bool) ([]bug.Bug, error) {
	db, err := s.openProjectDB(projectID)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.ListBugs()
	if err != nil {
		return nil, err
	}
	out := make([]bug.Bug, 0, len(rows))
	for _, r := range rows {
		b := *toBug(r)
		if openOnly && !b.IsOpen() {
			continue
		}
		out = append(out, b)
	}
	bug.SortByUrgency(out)
	return out, nil
}

// BugMove changes a bug's status, refusing moves the loop does not allow.
func (s *Service) BugMove(ctx context.Context, projectID, id, to string) (*bug.Bug, error) {
	db, err := s.openProjectDB(projectID)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rec, err := db.GetBug(id)
	if err != nil {
		return nil, fmt.Errorf("no bug %s", id)
	}
	next, err := bug.Move(bug.Status(rec.Status), bug.Status(to))
	if err != nil {
		return nil, err
	}
	rec.Status = string(next)
	if err := db.UpdateBug(rec); err != nil {
		return nil, err
	}
	return toBug(rec), nil
}

func (s *Service) openProjectDB(projectID string) (*store.DB, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(filepath.Join(entry.Path, ".ducklab", "ducklab.db"))
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func toBug(r *store.Bug) *bug.Bug {
	return &bug.Bug{
		ID: r.ID, Title: r.Title, Body: r.Body,
		Severity: bug.Severity(r.Severity), Status: bug.Status(r.Status),
		DuplicateOf: r.DuplicateOf, TaskID: r.TaskID,
		Source: r.Source, Reporter: r.Reporter,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// MaxTriageBatch bounds one triage run (05 §6).
//
// Ten bugs, each its own turn. One prompt holding ten reports lets confusion
// about the third contaminate the seventh, and a batch is not a conversation.
const MaxTriageBatch = 10

// BugTriage classifies the open bugs, one turn each (05 §6).
//
// The classifications are proposals. Under manual and guarded autonomy nothing
// is applied until a person says so — especially duplicates, where being wrong
// closes a real report.
func (s *Service) BugTriage(ctx context.Context, projectID string) (*runlog.Run, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	open, err := s.BugList(ctx, projectID, true)
	if err != nil {
		return nil, err
	}
	var todo []bug.Bug
	for _, b := range open {
		if b.Status == bug.Open {
			todo = append(todo, b)
		}
	}
	if len(todo) == 0 {
		return nil, fmt.Errorf("no untriaged bugs")
	}
	if len(todo) > MaxTriageBatch {
		todo = todo[:MaxTriageBatch]
	}

	run := &runlog.Run{
		ID:        runlog.GenerateRunID(),
		ProjectID: projectID,
		Stage:     "operate",
		Mode:      "solo",
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Stream:    true,
		Gate:      "none",
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
		"stage": "operate", "mode": "solo", "bugs": len(todo),
	})

	go s.executeTriage(runCtx, rs, entry.Path, todo, open)
	return run, nil
}

func (s *Service) executeTriage(ctx context.Context, rs *runState, projectRoot string, todo, all []bug.Bug) {
	defer close(rs.done)
	defer rs.writer.Close()

	projCfg, err := config.LoadProject(filepath.Join(projectRoot, ".ducklab", "project.toml"))
	if err != nil {
		s.failRun(rs, fmt.Errorf("load project config: %w", err))
		return
	}
	roster, _ := s.resolveRoster(projCfg)
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
	runner := s.runnerFor(cache, roster, ectx)
	duckling := roster[config.RoleTriager]

	proposals := make([]map[string]interface{}, 0, len(todo))
	for i, b := range todo {
		turn := &strategy.Turn{
			Role:     config.RoleTriager,
			Toolbelt: "full", // narrowed to the triager's ceiling
			Contract: "json:triage",
			MaxTurns: 6,
		}
		belt, err := turn.ResolveToolbelt(tools.NewRegistry())
		if err != nil {
			s.failRun(rs, err)
			return
		}
		rs.writer.AppendEvent("turn_start", map[string]interface{}{
			"round": 1, "turn": i, "role": string(config.RoleTriager),
			"duckling": string(duckling), "bug": b.ID,
		})
		out, err := runner(ctx, turn, duckling, triagePrompt(b, all), belt,
			strategy.TurnContext{Round: 1, Index: i})
		if err != nil {
			// One bad bug does not poison the others: the rest of the batch
			// still runs, and this one stays open for a person to look at.
			rs.writer.AppendEvent("triage_failed", map[string]interface{}{
				"bug": b.ID, "error": err.Error(),
			})
			continue
		}
		rs.writer.AppendEvent("turn_end", map[string]interface{}{
			"round": 1, "turn": i, "role": string(config.RoleTriager),
		})
		t, ok := out.Parsed.(*agent.Triage)
		if !ok || t == nil {
			continue
		}
		p := map[string]interface{}{
			"bug": b.ID, "severity": t.Severity, "reason": t.Reason,
			"component": t.Component, "task_title": t.TaskTitle,
			"suspected_files": t.SuspectedFiles,
		}
		if t.DuplicateOf != "" {
			p["duplicate_of"] = t.DuplicateOf
		}
		if t.Reproducible != nil {
			p["reproducible"] = *t.Reproducible
		}
		proposals = append(proposals, p)
		rs.writer.AppendEvent("triage", p)
	}
	recordSpend(rs, tracker)

	rs.run.Verdict = "UNVERIFIED"
	rs.run.Status = "paused"
	rs.run.PendingKind = "gate"
	rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
	rs.run.PendingData = map[string]interface{}{"triaged": len(proposals)}
	rs.writer.AppendEvent("human_needed", map[string]interface{}{
		"kind": "gate", "triaged": len(proposals),
	})
	rs.writer.WriteState()
}

// triagePrompt states one bug and the open bugs it might duplicate.
//
// Only the open ones: proposing a duplicate of something already closed would
// reopen a decision that was made, and the prompt says to base the answer only
// on what it was given.
func triagePrompt(b bug.Bug, all []bug.Bug) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## The bug\n\n**%s — %s**\n\nReported severity: %s\n\n", b.ID, b.Title, b.Severity)
	if strings.TrimSpace(b.Body) != "" {
		sb.WriteString(strings.TrimSpace(b.Body))
		sb.WriteString("\n\n")
	}
	sb.WriteString("## Other open bugs\n\n")
	others := 0
	for _, o := range all {
		if o.ID == b.ID || !o.IsOpen() {
			continue
		}
		fmt.Fprintf(&sb, "- %s — %s\n", o.ID, o.Title)
		others++
	}
	if others == 0 {
		sb.WriteString("There are none, so this cannot be a duplicate.\n")
	}
	return sb.String()
}

// BugPromote turns a bug into a task and links them (05 §6).
//
// The task's body carries the report verbatim. A fix written from a summary of
// a bug is a fix for the summary, and the reproduction steps are the part most
// easily lost in paraphrase.
func (s *Service) BugPromote(ctx context.Context, projectID, bugID string) (map[string]interface{}, error) {
	db, err := s.openProjectDB(projectID)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rec, err := db.GetBug(bugID)
	if err != nil {
		return nil, fmt.Errorf("no bug %s", bugID)
	}
	if rec.TaskID != "" {
		// Already promoted. Reported rather than done twice: a second task for
		// one report splits the work and leaves both halves looking unfinished.
		return nil, fmt.Errorf("%s is already task %s", bugID, rec.TaskID)
	}
	if bug.Status(rec.Status) == bug.Duplicate || bug.Status(rec.Status) == bug.WontFix ||
		bug.Status(rec.Status) == bug.Closed {
		return nil, fmt.Errorf("%s is %s; there is nothing to build", bugID, rec.Status)
	}
	// Checked before anything is written.
	//
	// This used to run after the task and the edge existed, so promoting an
	// untriaged bug created both and then failed on the status move, leaving a
	// task nobody asked for wired to a bug that had not moved.
	//
	// An untriaged bug is refused on purpose: promote follows triage in the
	// loop (05 §6), and a bug nobody has classified is one nobody has decided
	// is worth building.
	next, err := bug.Move(bug.Status(rec.Status), bug.InProgress)
	if err != nil {
		if bug.Status(rec.Status) == bug.Open {
			return nil, fmt.Errorf("%s has not been triaged yet; run `ducklab bug triage` "+
				"or set it with `ducklab bug status %s triaged`", bugID, bugID)
		}
		return nil, err
	}

	// The id comes from the plan, not from a database sequence.
	//
	// Tasks live in docs/plan.md — it is what `task list`, the board and
	// `ducklab run` all read. A sequence that knew only the bug table handed
	// out T-001 in a project whose plan already had T-001 through T-010: the
	// promoted task was invisible to every command, and the one the CLI told
	// you to run was a different task with the same name.
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	taskID, err := appendPlanTask(entry.Path, rec)
	if err != nil {
		return nil, err
	}
	// Recorded in the database too, so a bug's task can be found without
	// parsing a document, but the document is what allocated the id.
	if err := db.CreateTask(&store.Task{
		ID:     taskID,
		Title:  rec.Title,
		Body:   promotedTaskBody(rec),
		Status: "todo",
	}); err != nil {
		return nil, err
	}
	// The edge is what makes the bug part of the same graph as everything
	// else: it answers "why does this task exist" with the report that caused
	// it, the way a task answers it with the spec section it implements.
	if err := db.AddTrace("bug", bugID, "task", taskID); err != nil {
		return nil, err
	}

	rec.TaskID = taskID
	rec.Status = string(next)
	if err := db.UpdateBug(rec); err != nil {
		return nil, err
	}
	return map[string]interface{}{"bug": bugID, "task": taskID, "status": rec.Status}, nil
}

func promotedTaskBody(b *store.Bug) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Fixes %s.\n\n", b.ID)
	if strings.TrimSpace(b.Body) != "" {
		sb.WriteString("## Reported\n\n")
		sb.WriteString(strings.TrimSpace(b.Body))
		sb.WriteString("\n")
	}
	return sb.String()
}

// bugsMilestoneTitle names the milestone promoted bugs land under.
//
// Their own milestone rather than the last one: a fix is not part of the
// feature that happened to be planned most recently, and burying it there
// would misreport what that milestone contained.
//
// It is found by title, not by a memorable id. The first version used
// "M-BUGS", which is not an id this project's own parser accepts — ids are
// PREFIX-<digits> — so the heading was silently unrecognised and the task it
// contained was read as a child of whatever milestone came before it.
const bugsMilestoneTitle = "Reported bugs"

// appendPlanTask adds a task to the plan document and returns its id.
func appendPlanTask(projectRoot string, rec *store.Bug) (string, error) {
	plan, err := artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil {
		return "", err
	}
	if plan == nil {
		plan = &artifact.Document{Front: artifact.Frontmatter{Kind: artifact.KindPlan}}
	}

	var existing []artifact.Section
	for _, m := range plan.Sections {
		existing = append(existing, m.Children...)
	}
	taskID := fmt.Sprintf("T-%03d", stage.NextFree(existing, "T"))

	task := artifact.Section{
		ID:    taskID,
		Title: rec.Title,
		Body:  promotedTaskBody(rec),
	}

	placed := false
	for i := range plan.Sections {
		if plan.Sections[i].Title == bugsMilestoneTitle {
			plan.Sections[i].Children = append(plan.Sections[i].Children, task)
			placed = true
			break
		}
	}
	if !placed {
		plan.Sections = append(plan.Sections, artifact.Section{
			ID:       fmt.Sprintf("M-%03d", stage.NextFree(plan.Sections, "M")),
			Title:    bugsMilestoneTitle,
			Children: []artifact.Section{task},
		})
	}
	plan.Front.Kind = artifact.KindPlan
	// A project may legitimately have bugs and no plan: the spec allows a
	// build on a hand-written task with no requirements at all (05 §1).
	// Promoting into one creates the plan rather than refusing.
	if err := os.MkdirAll(artifact.DocsDir(projectRoot), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(artifact.Path(projectRoot, artifact.KindPlan),
		[]byte(artifact.Render(plan)), 0o644); err != nil {
		return "", err
	}
	return taskID, nil
}
