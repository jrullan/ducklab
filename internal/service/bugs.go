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
	entry, entryErr := s.registry.Get(projectID)
	var audit map[string][]bug.AuditEntry
	if entryErr == nil {
		audit = readBugAudit(entry.Path)
	}
	out := make([]bug.Bug, 0, len(rows))
	for _, r := range rows {
		b := *toBug(r)
		if openOnly && !b.IsOpen() {
			continue
		}
		if entryErr == nil {
			b.Attachments = listAttachments(entry.Path, b.ID)
			b.History = audit[b.ID]
		}
		out = append(out, b)
	}
	bug.SortByUrgency(out)
	return out, nil
}

// BugMove changes a bug's status, refusing moves the loop does not allow.
//
// Signed. B-041 went from fixed back to in_progress and no record said who —
// the agent operating overnight, asked directly, could neither confirm nor
// deny. An unattributed move is indistinguishable from a malfunction.
func (s *Service) BugMove(ctx context.Context, projectID, id, to, actor string) (*bug.Bug, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
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
	from := rec.Status
	rec.Status = string(next)
	if err := db.UpdateBug(rec); err != nil {
		return nil, err
	}
	if actor == "" {
		actor = "human"
	}
	appendBugAudit(entry.Path, bug.AuditEntry{
		Bug: rec.ID, From: from, To: rec.Status, Actor: actor, Via: "move",
	})
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
		Next: bug.NextFrom(bug.Status(r.Status)),
	}
}

// MaxTriageBatch bounds one triage run (05 §6).
//
// Ten bugs, each its own turn. One prompt holding ten reports lets confusion
// about the third contaminate the seventh, and a batch is not a conversation.
const MaxTriageBatch = 10

// BugTriage classifies open bugs, one turn each (05 §6). An empty bugID
// takes the whole inbox (bounded by MaxTriageBatch); naming one triages
// exactly that bug — the button inside ONE bug's panel used to fire the
// batch, which read as the panel acting far beyond its own context.
//
// The classifications are proposals. Under manual and guarded autonomy nothing
// is applied until a person says so — especially duplicates, where being wrong
// closes a real report.
func (s *Service) BugTriage(ctx context.Context, projectID, bugID string) (*runlog.Run, error) {
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
		if b.Status == bug.Open && (bugID == "" || b.ID == bugID) {
			todo = append(todo, b)
		}
	}
	if len(todo) == 0 {
		if bugID != "" {
			return nil, fmt.Errorf("bug %s is not open for triage", bugID)
		}
		return nil, fmt.Errorf("no untriaged bugs")
	}
	if len(todo) > MaxTriageBatch {
		todo = todo[:MaxTriageBatch]
	}

	run := &runlog.Run{
		ID:        runlog.GenerateRunID(),
		ProjectID: projectID,
		// What it did, not the loop it belongs to. Every other run records the
		// former — build, review, release — and this one recorded "operate",
		// which is the name of the whole loop. Runs are labelled by task id and
		// fall back to the stage, so a triage run appeared in the list as
		// "operate" with nothing to say what had actually run.
		Stage:     "triage",
		Mode:      "solo",
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Stream:    true,
		Gate:      "none",
		Autonomy:  s.triageAutonomy(entry.Path),
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
		"stage": "triage", "mode": "solo", "bugs": len(todo),
	})

	go s.executeTriage(runCtx, rs, entry.Path, todo, open)
	return run, nil
}

func (s *Service) executeTriage(ctx context.Context, rs *runState, projectRoot string, todo, all []bug.Bug) {
	defer recoverRun(rs)
	defer close(rs.done)
	defer rs.writer.Close()

	projCfg, err := config.LoadProject(filepath.Join(projectRoot, ".ducklab", "project.toml"))
	if err != nil {
		s.failRun(rs, fmt.Errorf("load project config: %w", err))
		return
	}
	roster, _ := s.resolveRoster(projCfg)
	limits := &budget.Budget{
		MaxUSD:        projCfg.Budget.MaxUSD,
		MaxTokens:     int64(s.cfg.Defaults.Budget.MaxTokens),
		MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS,
		MaxTurns:      s.cfg.Defaults.Budget.MaxTurns,
	}
	tracker := budget.NewTracker(limits)
	recordLimits(rs, limits)
	rs.setTracker(tracker)
	ectx := &tools.ExecContext{ProjectRoot: projectRoot, RunID: rs.run.ID}
	cache := &loopCache{
		svc: s, tracker: tracker,
		writer:  s.llmWriter(rs, tracker),
		capLift: rs.capLifted.Load,
		loops:   map[config.DucklingID]*agent.Loop{},
	}
	s.attachStreaming(rs, cache)
	runner := s.runnerFor(cache, roster, ectx)
	duckling := roster[config.RoleTriager]

	proposals := make([]map[string]interface{}, 0, len(todo))
	for i, b := range todo {
		turn := &strategy.Turn{
			Role:     config.RoleTriager,
			Toolbelt: "full", // narrowed to the triager's ceiling
			Contract: "json:triage",
			// The cap that told its own failure message to raise a number
			// nobody could reach. Configurable now, with six as the default.
			MaxTurns: s.turnsFor(string(config.RoleTriager), ScriptRoleTurns["triager"]),
		}
		// The report's screenshots, shown to a triager that can see. Gated on
		// the declared vision cap: a text-only model sent an image array gets
		// a 400 from most endpoints, and a triage that dies on evidence it
		// cannot read helps nobody.
		if cfg, ok := s.cfg.Ducklings[duckling]; ok && cfg.Caps.Vision != nil && *cfg.Caps.Vision {
			turn.Images = attachmentDataURLs(rs.projectPath, b.ID, 6<<20)
			if len(turn.Images) > 0 {
				rs.writer.AppendEvent("warning", map[string]interface{}{
					"detail": fmt.Sprintf("%s: %d screenshot(s) shown to the triager", b.ID, len(turn.Images)),
				})
			}
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
		// What the triager said and did. Without this the run recorded a
		// turn_start and a turn_end around nothing: the lane showed a
		// participant with an empty bubble, and the reasoning — which IS the
		// content of a triage — never left the process.
		// true: the triager's loop wires OnToolCall like every other, so its
		// calls are already on the record as they happened.
		strategy.EmitTurnRecord(func(kind string, data map[string]interface{}) {
			rs.writer.AppendEvent(kind, data)
		}, 1, i, config.RoleTriager, duckling, out, true)
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
			"test_strategy": t.TestStrategy, "test_reason": t.TestReason,
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
	// The proposals themselves, not just how many. Accepting the gate has to
	// apply them, and the count alone left the run with nothing to act on: the
	// classifications existed only in the event stream, so Accept ran the
	// ordinary code path — stage nothing, find the tree clean — and the whole
	// triage was discarded with a green tick.
	rs.run.PendingData = map[string]interface{}{
		"triaged": len(proposals), "proposals": proposals,
	}

	// Auto-apply under auto and yolo — with one law standing: a duplicate
	// proposal always waits for a person, because a wrong duplicate CLOSES a
	// real report, and that is the one triage outcome that is not just
	// metadata. Severity, component and title are reversible edits.
	if rs.run.Autonomy == "auto" || rs.run.Autonomy == "yolo" {
		hasDuplicate := false
		for _, p := range proposals {
			if _, ok := p["duplicate_of"]; ok {
				hasDuplicate = true
			}
		}
		if !hasDuplicate && len(proposals) > 0 {
			rs.writer.WriteState()
			if entry, err := s.entryFor(rs); err == nil {
				if err := s.acceptRun(ctx, rs, entry, "", "auto-triage"); err == nil {
					return
				}
			}
			// A failed auto-apply degrades to the ordinary human gate below.
		} else if hasDuplicate {
			rs.run.PendingData["detail"] = "auto-apply declined: a proposal closes a report as duplicate — that decision is yours"
		}
	}

	rs.writer.AppendEvent("human_needed", map[string]interface{}{
		"kind": "gate", "triaged": len(proposals),
	})
	rs.writer.WriteState()
}

// triageAutonomy resolves what a triage run may decide alone: the project's
// declared autonomy, else the global default, else guarded.
func (s *Service) triageAutonomy(projectPath string) string {
	if projCfg, err := config.LoadProject(filepath.Join(projectPath, ".ducklab", "project.toml")); err == nil && projCfg.Autonomy != "" {
		return string(projCfg.Autonomy)
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if s.cfg.Defaults.Autonomy != "" {
		return string(s.cfg.Defaults.Autonomy)
	}
	return "guarded"
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
func (s *Service) BugPromote(ctx context.Context, projectID, bugID, actor string) (map[string]interface{}, error) {
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
		Title:  promotedTaskTitle(rec),
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

	from := rec.Status
	rec.TaskID = taskID
	rec.Status = string(next)
	if err := db.UpdateBug(rec); err != nil {
		return nil, err
	}
	if actor == "" {
		actor = "human"
	}
	appendBugAudit(entry.Path, bug.AuditEntry{
		Bug: rec.ID, From: from, To: rec.Status, Actor: actor, Via: "promote", Note: taskID,
	})
	// A promote changes what the guide says without any run settling — the
	// exact blind spot of the settle hooks. Poke the loop so an autopilot
	// idling at "promote it" picks the new task up instead of waiting for an
	// accept that will never come.
	go s.autopilotAdvance(projectID)
	return map[string]interface{}{"bug": bugID, "task": taskID, "status": rec.Status}, nil
}

// promotedTaskBody is what an implementer is given when a report becomes work.
//
// The reporter's words AND what the triage worked out. It used to be the words
// alone: the component, the suspected files and the reasoning were computed,
// shown once at a gate, and then discarded — so the model that had to fix the
// bug went looking for a location somebody had already found.
func promotedTaskBody(b *store.Bug) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Fixes %s.\n\n", b.ID)
	if strings.TrimSpace(b.Body) != "" {
		sb.WriteString("## Reported\n\n")
		sb.WriteString(strings.TrimSpace(b.Body))
		sb.WriteString("\n")
	}
	if b.Component != "" || b.SuspectedFiles != "" || b.TriageReason != "" {
		sb.WriteString("\n## Triage\n\n")
		if b.Component != "" {
			fmt.Fprintf(&sb, "**Component:** %s\n", b.Component)
		}
		if b.SuspectedFiles != "" {
			fmt.Fprintf(&sb, "**Suspected files:** %s\n",
				strings.Join(strings.Split(b.SuspectedFiles, "\n"), ", "))
		}
		if b.TriageReason != "" {
			sb.WriteString("\n" + strings.TrimSpace(b.TriageReason) + "\n")
		}
		if b.TestStrategy != "" {
			fmt.Fprintf(&sb, "\n**Verification (triage recommends):** %s", b.TestStrategy)
			if b.TestReason != "" {
				fmt.Fprintf(&sb, " — %s", strings.TrimSpace(b.TestReason))
			}
			sb.WriteString("\n")
		}
		// Said out loud, because it is a model's opinion and the reporter's
		// words are not. An implementer that finds the cause elsewhere should
		// not doubt itself.
		sb.WriteString("\nThis section is the triager's reading, not the reporter's. " +
			"Check it rather than assume it.\n")
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

	// The title the triager proposed, when it gave one: it names the change to
	// make, where the report names the symptom. "when dragging a vertex one edge
	// value does not change" is what a person saw; "recompute the edge label for
	// the dragged vertex" is what somebody has to do.
	task := artifact.Section{
		ID:    taskID,
		Title: promotedTaskTitle(rec),
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

// ApplyTriage writes an accepted triage onto the bugs it classified.
//
// A triage run is neither an artifact stage nor a change to the tree, so
// acceptRun had nothing to do with one: it promoted no document, committed no
// diff, and returned success. Accept and Reject were the same button.
//
// Promotion to a task stays a separate act. Agreeing with a classification is
// not the same decision as committing to fix it, and a triage that silently
// filled the board would take that decision away.
func (s *Service) ApplyTriage(ctx context.Context, projectID string, raw interface{}) (int, error) {
	// Two shapes for one thing. A run still in memory carries the slice the
	// triage built; a run rehydrated from state.json carries what JSON made of
	// it. Handling only the second meant the fix worked after an engine restart
	// and not before, which is the worst of both.
	var proposals []map[string]interface{}
	switch v := raw.(type) {
	case []map[string]interface{}:
		proposals = v
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				proposals = append(proposals, m)
			}
		}
	default:
		return 0, nil
	}

	db, err := s.openProjectDB(projectID)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	applied := 0
	for _, p := range proposals {
		id, _ := p["bug"].(string)
		if id == "" {
			continue
		}
		rec, err := db.GetBug(id)
		if err != nil {
			// A bug deleted between the triage and the accept is not a reason
			// to lose the rest of the batch.
			continue
		}
		if sev, _ := p["severity"].(string); sev != "" {
			rec.Severity = sev
		}
		if dup, _ := p["duplicate_of"].(string); dup != "" {
			rec.DuplicateOf = dup
		}
		// The half of the answer that says WHERE. It lived only in the run's
		// event stream, so promoting the bug days later carried the reporter's
		// prose and nothing the triage had worked out — and an implementer
		// started from "the left edge label does not update" and a 1361-line
		// file with the location already computed and thrown away.
		if v, _ := p["component"].(string); v != "" {
			rec.Component = v
		}
		if v, _ := p["task_title"].(string); v != "" {
			rec.TaskTitle = v
		}
		if v, _ := p["test_strategy"].(string); v != "" {
			rec.TestStrategy = v
		}
		if v, _ := p["test_reason"].(string); v != "" {
			rec.TestReason = v
		}
		if v, _ := p["reason"].(string); v != "" {
			rec.TriageReason = v
		}
		if files, ok := p["suspected_files"].([]interface{}); ok {
			var names []string
			for _, f := range files {
				if name, _ := f.(string); name != "" {
					names = append(names, name)
				}
			}
			rec.SuspectedFiles = strings.Join(names, "\n")
		}
		if files, ok := p["suspected_files"].([]string); ok {
			rec.SuspectedFiles = strings.Join(files, "\n")
		}
		// A classification must never undo a promotion. Move(InProgress,
		// Triaged) is a LEGAL transition — it exists so a person can send
		// half-started work back — so relying on Move to refuse was wrong:
		// accepting a stale triage run after the bug had become a task quietly
		// regressed it, and the accept of that task then found nothing in
		// in_progress to close. Measured: a bug double-triaged, promoted, and
		// knocked back by the second gate twelve minutes later, to the second.
		// Its words still update; its place in the loop is not triage's to take.
		if rec.TaskID == "" && bug.Status(rec.Status) == bug.Open {
			to := bug.Triaged
			// A duplicate is closed by being one; it does not need its own fix.
			if rec.DuplicateOf != "" {
				to = bug.Duplicate
			}
			if next, err := bug.Move(bug.Status(rec.Status), to); err == nil {
				from := rec.Status
				rec.Status = string(next)
				if entry, eerr := s.registry.Get(projectID); eerr == nil {
					appendBugAudit(entry.Path, bug.AuditEntry{
						Bug: rec.ID, From: from, To: rec.Status, Actor: "engine", Via: "triage",
					})
				}
			}
		}
		if err := db.UpdateBug(rec); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// BugFixedByTask moves the bug a task came from to "fixed", and reports which.
//
// Promoting a bug set its task id and moved it to in_progress, and nothing ever
// moved it again. The work landed, the task was accepted, and the report sat on
// the board as in_progress for good — the loop had an entrance and no exit.
//
// "fixed", not "verified". The gate that passed may be a syntax check: this
// project accepted twenty-one tasks against one, and the feature the bug is
// about never worked. Verified is a person saying the report is actually
// answered, and that is the one judgement a run cannot make for them (I2).
func (s *Service) BugFixedByTask(ctx context.Context, projectID, taskID string) (string, error) {
	if taskID == "" {
		return "", nil
	}
	db, err := s.openProjectDB(projectID)
	if err != nil {
		return "", err
	}
	defer db.Close()

	recs, err := db.ListBugs()
	if err != nil {
		return "", err
	}
	for _, rec := range recs {
		if rec.TaskID != taskID {
			continue
		}
		// Walk the legal chain to fixed from wherever the report stands. It
		// used to demand in_progress exactly and skip in silence — so a bug a
		// stale triage had knocked back to triaged watched its own task get
		// accepted and moved nowhere, with no event saying why.
		st := bug.Status(rec.Status)
		switch st {
		case bug.Fixed, bug.Verified, bug.Closed:
			// Already at or past the point this would move it to.
			return "", nil
		}
		for _, step := range []bug.Status{bug.InProgress, bug.Fixed} {
			if next, err := bug.Move(st, step); err == nil {
				st = next
			}
		}
		if st != bug.Fixed {
			return "", fmt.Errorf("%s is %s and cannot reach fixed from there", rec.ID, rec.Status)
		}
		from := rec.Status
		rec.Status = string(st)
		if err := db.UpdateBug(rec); err != nil {
			return "", err
		}
		if entry, eerr := s.registry.Get(projectID); eerr == nil {
			appendBugAudit(entry.Path, bug.AuditEntry{
				Bug: rec.ID, From: from, To: rec.Status, Actor: "engine",
				Via: "task-accepted", Note: taskID,
			})
		}
		return rec.ID, nil
	}
	return "", nil
}

// promotedTaskTitle prefers what the triager proposed.
//
// A report names the symptom; a task names the change. "one edge value does not
// change" is what a person saw, and it is a poor name for the work.
func promotedTaskTitle(b *store.Bug) string {
	if t := strings.TrimSpace(b.TaskTitle); t != "" {
		return t
	}
	return b.Title
}

// BugEdit changes what a report says.
//
// A report is written by a person in a hurry, from memory, often before they
// have looked. Correcting it was impossible: a bug could be moved, triaged and
// promoted but never edited, so a typo or a missing detail lived as long as the
// bug did — and the triager, and then the implementer, worked from it.
//
// Only the words. Status, task and duplicate belong to the loop and have their
// own transitions; letting a form set them would put the loop's rules in two
// places.
func (s *Service) BugEdit(ctx context.Context, projectID, bugID string, req BugRequest) (*bug.Bug, error) {
	db, err := s.openProjectDB(projectID)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rec, err := db.GetBug(bugID)
	if err != nil {
		return nil, fmt.Errorf("no bug %s", bugID)
	}
	if t := strings.TrimSpace(req.Title); t != "" {
		rec.Title = t
	}
	// An empty body is a legitimate edit — someone clearing a wrong paragraph —
	// so it is only left alone when the field was absent altogether. The title
	// is not: a report with no title cannot be listed.
	if req.Body != "" || req.Title != "" {
		rec.Body = strings.TrimSpace(req.Body)
	}
	if sev := strings.ToLower(strings.TrimSpace(req.Severity)); sev != "" {
		if !bug.ValidSeverity(sev) {
			return nil, fmt.Errorf("unknown severity %q, want critical, high, normal or low", req.Severity)
		}
		rec.Severity = sev
	}
	if err := db.UpdateBug(rec); err != nil {
		return nil, err
	}
	return toBug(rec), nil
}

// TaskRemove deletes a task from the plan, and unlinks the report it came from.
//
// Refused once a run has touched it. A run record names its task, reports
// average by it and the traceability spine walks it: deleting one out from under
// its runs leaves rows pointing at something that no longer exists, and a report
// that says a task passed when the task is gone is worse than no report.
//
// What it exists for is undoing a promotion — a bug turned into work before its
// triage had run, say — so the bug goes back to triaged and can be promoted
// again with everything that was worked out since.
func (s *Service) TaskRemove(ctx context.Context, projectID, taskID string) (map[string]interface{}, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	// Only committed or in-flight work pins a task. The first version refused
	// ANY run at all — which made the one workflow removal exists for, undoing
	// a bad promotion, impossible: you learn a task was promoted badly BY
	// running it and watching it fail. Measured on the task this was built for.
	//
	// A failed or rejected run committed nothing, and a report that says a
	// removed task FAILED is history, not a lie. An accepted run's work is in
	// the tree and traced here; a run still going would be orphaned mid-flight.
	for _, r := range runs {
		if r.TaskID != taskID {
			continue
		}
		if r.Accepted {
			return nil, fmt.Errorf("%s was accepted in %s; its work is committed and traced "+
				"to this task, so the task must stay", taskID, r.ID)
		}
		if r.Status == "running" || r.Status == "queued" || r.Status == "paused" {
			return nil, fmt.Errorf("%s has a run still open (%s, %s); abort or decide it first",
				taskID, r.ID, r.Status)
		}
	}

	// The database opens BEFORE the plan is touched. This removal edits two
	// records that must move together — the plan section and the task row with
	// its bug pointer — and the database half used to be best-effort: an open
	// failure returned success after editing only the plan. T-048 lived that
	// exact split for a morning: gone from the plan, alive in the database,
	// its bug stuck in_progress pointing at it — unpromotable, unrunnable, and
	// still offered by every view that reads the database.
	db, err := s.openProjectDB(projectID)
	if err != nil {
		return nil, fmt.Errorf("cannot remove %s: its database record is unreachable (%v); "+
			"removing only the plan entry would strand the task half-deleted", taskID, err)
	}
	defer db.Close()

	removed, unreferenced, err := removePlanTask(entry.Path, taskID)
	if err != nil {
		return nil, err
	}
	if !removed {
		return nil, fmt.Errorf("no task %s in the plan", taskID)
	}

	out := map[string]interface{}{"removed": taskID}
	if len(unreferenced) > 0 {
		// Said, so the person knows which tasks just changed under them.
		out["dependencies_cleaned"] = unreferenced
	}
	if err := db.DeleteTask(taskID); err != nil {
		// The plan edit is already on disk; said out loud rather than
		// swallowed, so the person knows the halves disagree and which one.
		out["warning"] = fmt.Sprintf("plan entry removed, but the database row remains: %v", err)
	}

	// The report goes back to where it was, or it would sit in in_progress
	// forever pointing at a task nobody can find.
	bugs, err := db.ListBugs()
	if err != nil {
		out["warning"] = fmt.Sprintf("plan entry removed, but its bug could not be reset: %v", err)
		return out, nil
	}
	for _, rec := range bugs {
		if rec.TaskID != taskID {
			continue
		}
		rec.TaskID = ""
		from := rec.Status
		if next, mErr := bug.Move(bug.Status(rec.Status), bug.Triaged); mErr == nil {
			rec.Status = string(next)
		}
		if err := db.UpdateBug(rec); err == nil {
			out["bug"] = rec.ID
			out["bug_status"] = rec.Status
			appendBugAudit(entry.Path, bug.AuditEntry{
				Bug: rec.ID, From: from, To: rec.Status, Actor: "engine",
				Via: "task-removed", Note: taskID,
			})
		} else {
			out["warning"] = fmt.Sprintf("plan entry removed, but %s could not be reset: %v", rec.ID, err)
		}
		break
	}
	return out, nil
}

// removePlanTask takes a task out of plan.md, and the milestone with it if that
// leaves it empty.
//
// The document is what allocates ids and what every reader parses, so a removal
// that only touched the database would leave the task visible everywhere anyone
// actually looks.
func removePlanTask(projectRoot, taskID string) (removed bool, unreferenced []string, err error) {
	plan, err := artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil {
		return false, nil, err
	}
	if plan == nil {
		return false, nil, nil
	}
	found := false
	var milestones []artifact.Section
	for _, m := range plan.Sections {
		var kept []artifact.Section
		for _, t := range m.Children {
			if strings.EqualFold(t.ID, taskID) {
				found = true
				continue
			}
			kept = append(kept, t)
		}
		m.Children = kept
		// An empty milestone left behind reads as work that was planned and
		// then silently dropped.
		if len(kept) == 0 && strings.TrimSpace(m.Body) == "" {
			continue
		}
		milestones = append(milestones, m)
	}
	if !found {
		return false, nil, nil
	}
	// The removed task's id must not survive in anyone's Depends line. It
	// did once: T-022 was removed cleanly and T-023 kept depending on it —
	// "depends on a task that does not exist, so it can never start" — a
	// dead end no button could fix, because tasks have no dependency editor.
	// The removal made the reference dangling; the removal cleans it up.
	for mi := range milestones {
		for ci := range milestones[mi].Children {
			c := &milestones[mi].Children[ci]
			body, changed := stripDependency(c.Body, taskID)
			if changed {
				c.Body = body
				unreferenced = append(unreferenced, c.ID)
			}
		}
	}
	plan.Sections = milestones
	plan.Front.Kind = artifact.KindPlan
	if err := os.WriteFile(artifact.Path(projectRoot, artifact.KindPlan),
		[]byte(artifact.Render(plan)), 0o644); err != nil {
		return false, nil, err
	}
	return true, unreferenced, nil
}

// stripDependency removes one id from a body's **Depends on:** line, dropping
// the line entirely when it was the only dependency.
func stripDependency(body, taskID string) (string, bool) {
	lines := strings.Split(body, "\n")
	changed := false
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if !strings.HasPrefix(lower, "**depends on:**") {
			out = append(out, line)
			continue
		}
		rest := trimmed[len("**Depends on:**"):]
		var kept []string
		for _, dep := range strings.Split(rest, ",") {
			if d := strings.TrimSpace(dep); d != "" && !strings.EqualFold(d, taskID) {
				kept = append(kept, d)
			} else if strings.EqualFold(strings.TrimSpace(dep), taskID) {
				changed = true
			}
		}
		if len(kept) > 0 {
			out = append(out, "**Depends on:** "+strings.Join(kept, ", "))
		}
		// A depends line with nobody left is dropped, not kept empty.
	}
	return strings.Join(out, "\n"), changed
}

// RunFileFindings turns the run's final reviewer findings into bug reports.
//
// A reviewer that approves "with two minor findings" has found real work; the
// approval means "not worth blocking THIS run", not "not worth remembering".
// Those findings used to live only in the transcript, waiting for a future
// testing phase to re-discover them at full price. Filed as bugs they enter
// the existing loop — triage, promote, fix — with their provenance attached.
//
// Idempotent by record: a findings_filed event on the run refuses a second
// filing, because two clicks must not mean duplicate reports.
func (s *Service) RunFileFindings(ctx context.Context, runID string) ([]bug.Bug, error) {
	s.runsMu.RLock()
	rs, ok := s.runs[runID]
	s.runsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	events, err := runlog.ReadEvents(s.RunDir(runID))
	if err != nil {
		return nil, fmt.Errorf("read run events: %w", err)
	}

	var verdict, duckling string
	var findings []map[string]interface{}
	for _, e := range events {
		if e.Type == "findings_filed" {
			return nil, fmt.Errorf("not filed — this run's findings were already filed as bugs (%v)", e.Data["bugs"])
		}
		if e.Type != "message" || e.Data["verdict"] == nil {
			continue
		}
		verdict = fmt.Sprintf("%v", e.Data["verdict"])
		duckling = fmt.Sprintf("%v", e.Data["duckling"])
		findings = nil
		if raw, ok := e.Data["findings"].([]interface{}); ok {
			for _, f := range raw {
				if m, ok := f.(map[string]interface{}); ok {
					findings = append(findings, m)
				}
			}
		}
	}
	if len(findings) == 0 {
		return nil, fmt.Errorf("not filed — the last reviewer verdict (%s) carries no findings", orDefault(verdict, "none"))
	}

	var out []bug.Bug
	var ids []string
	for _, f := range findings {
		issue := strings.TrimSpace(fmt.Sprintf("%v", orDefault(str(f["issue"]), "unspecified finding")))
		title := issue
		if len(title) > 100 {
			title = title[:97] + "…"
		}
		var body strings.Builder
		body.WriteString(issue + "\n")
		if file := str(f["file"]); file != "" {
			body.WriteString("\nWhere: " + file)
			if line, ok := f["line"].(float64); ok && line > 0 {
				fmt.Fprintf(&body, ":%d", int(line))
			}
			body.WriteString("\n")
		}
		if fix := str(f["fix"]); fix != "" {
			body.WriteString("\nSuggested fix: " + fix + "\n")
		}
		fmt.Fprintf(&body, "\nFound by %s reviewing %s in run %s (verdict: %s).\n",
			orDefault(duckling, "the reviewer"), orDefault(rs.run.TaskID, "the work"), runID, verdict)
		b, err := s.BugAdd(ctx, rs.run.ProjectID, BugRequest{
			Title:    title,
			Body:     body.String(),
			Severity: findingSeverity(str(f["severity"])),
			Reporter: duckling,
			Source:   "review",
		})
		if err != nil {
			return out, fmt.Errorf("filed %d of %d, then: %w", len(out), len(findings), err)
		}
		out = append(out, *b)
		ids = append(ids, b.ID)
	}

	if w, werr := s.ensureWriter(rs); werr == nil {
		w.AppendEvent("findings_filed", map[string]interface{}{
			"bugs": ids, "count": len(ids), "by": "human",
		})
		_ = w.WriteState()
	}
	return out, nil
}

// findingSeverity maps a reviewer's scale onto the bug tracker's.
func findingSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return "critical"
	case "major":
		return "high"
	case "minor":
		return "low"
	}
	return "normal"
}

// str is fmt-free map plucking: absent and nil both read as empty.
func str(v interface{}) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}
