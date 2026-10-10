package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/stage"
	"github.com/jrullan/ducklab/internal/store"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/tools"
	"github.com/jrullan/ducklab/internal/verify"
)

// BugRequest reports something that is broken.
type BugRequest struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Severity string `json:"severity"`
	Reporter string `json:"reporter"`
	Source   string `json:"source"`
	// Proposal, on an edit, is the split the person wants promote to make.
	// A pointer so three requests stay distinct: absent leaves the stored
	// proposal alone, an empty list discards it, a list replaces it.
	Proposal *[]bug.Portion `json:"proposal,omitempty"`
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
	var taskStatus map[string]string
	for _, r := range rows {
		b := *toBug(r)
		if openOnly && !b.IsOpen() {
			continue
		}
		if entryErr == nil {
			b.Attachments = listAttachments(entry.Path, b.ID)
			b.History = audit[b.ID]
			b.NeedsTriage = bugNeedsRetriage(b.History)
			b.Reopened = reopenedSinceFix(b.History)
		}
		if traces, terr := db.TracesFrom("bug", r.ID); terr == nil && len(traces) > 0 {
			if taskStatus == nil {
				taskStatus = s.boardTaskStatus(ctx, projectID)
			}
			b.Tasks = bugTasks(db, r.TaskID, traces, taskStatus)
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
	// Reopening a claimed fix starts a new piece of work. Keeping the consumed
	// task bound here makes promote refuse forever ("already task T-nnn") and
	// falsely presents the accepted task as the current fix. Preserve that task
	// in the audit trail, clear the live binding, and return to triaged so the
	// person can confirm the new evidence before minting another task.
	if bug.Status(rec.Status) == bug.Fixed && bug.Status(to) == bug.InProgress {
		if actor == "" {
			actor = "human"
		}
		from, previousTask := rec.Status, rec.TaskID
		rec.Status = string(bug.Triaged)
		rec.TaskID = ""
		rec.DuplicateOf = ""
		// The old contract described the fix that just failed verification. It
		// must not become the checklist for the next attempt merely because its
		// words are still literally true of the current tree.
		rec.Component = ""
		rec.SuspectedFiles = ""
		rec.TaskTitle = ""
		rec.TriageReason = ""
		rec.TestStrategy = ""
		rec.TestReason = ""
		rec.Deliverables = ""
		rec.Proposal = ""
		if err := db.UpdateBug(rec); err != nil {
			return nil, err
		}
		note := "reopened after the previous fix did not answer the report"
		if previousTask != "" {
			note = fmt.Sprintf("reopened after %s was accepted; the previous fix did not answer the report", previousTask)
		}
		audit := bug.AuditEntry{
			Bug: rec.ID, From: from, To: rec.Status, Actor: actor, Via: "reopen", Note: note,
		}
		appendBugAudit(entry.Path, audit)
		out := toBug(rec)
		out.History = []bug.AuditEntry{audit}
		out.NeedsTriage = true
		out.Reopened = true
		return out, nil
	}
	next, err := bug.Move(bug.Status(rec.Status), bug.Status(to))
	if err != nil {
		return nil, err
	}
	if next == bug.Fixed && rec.Proposal != "" {
		all, pending, err := bugProposalTasksAccepted(db, rec.ID, rec.TaskID)
		if err != nil {
			return nil, err
		}
		if !all {
			return nil, fmt.Errorf("bug %s cannot become fixed until proposed task(s) %s are accepted", rec.ID, strings.Join(pending, ", "))
		}
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
		Next:     bug.NextFrom(bug.Status(r.Status)),
		Proposal: storedPortions(r),
	}
}

// storedPortions reads the split on the table. The store keeps the triager's
// JSON verbatim; a person's edit is stored in the same shape, so promote reads
// both through one path. Unreadable JSON is shown as no proposal here and
// refused by promote, where the failure can be acted on.
func storedPortions(r *store.Bug) []bug.Portion {
	if strings.TrimSpace(r.Proposal) == "" {
		return nil
	}
	var portions []bug.Portion
	if err := json.Unmarshal([]byte(r.Proposal), &portions); err != nil {
		return nil
	}
	return portions
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
	all, err := s.BugList(ctx, projectID, false)
	if err != nil {
		return nil, err
	}
	var todo []bug.Bug
	for _, b := range open {
		eligible := b.Status == bug.Open || (b.Status == bug.Triaged && b.NeedsTriage)
		if eligible && (bugID == "" || b.ID == bugID) {
			todo = append(todo, b)
		}
	}
	if len(todo) == 0 {
		if bugID != "" {
			return nil, fmt.Errorf("bug %s does not need triage", bugID)
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
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Stream:    true,
		Gate:      "none",
		Autonomy:  s.triageAutonomy(entry.Path),
		Subject:   triageSubject(todo),
	}
	// Born running outside the queue: its active clock opens here or never.
	setRunStatus(run, "running", time.Now())
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

	go s.executeTriage(runCtx, rs, entry.Path, todo, all)
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
	roster, _ := s.resolveRoster(projCfg, "triage")
	limitsValue := projectBudget(budget.Budget{
		MaxUSD: s.cfg.Defaults.Budget.MaxUSD, MaxTokens: int64(s.cfg.Defaults.Budget.MaxTokens),
		MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS, MaxTurns: s.cfg.Defaults.Budget.MaxTurns,
	}, projCfg.Budget)
	limits := &limitsValue
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
	turnCaps := s.resolveTurnCaps("triage", 0)

	proposals := make([]map[string]interface{}, 0, len(todo))
	for i, b := range todo {
		turn := &strategy.Turn{
			Role:     config.RoleTriager,
			Toolbelt: "full", // narrowed to the triager's ceiling
			Contract: "json:triage",
			// The cap that told its own failure message to raise a number
			// nobody could reach. It now follows the shared precedence rule.
			MaxTurns:          strategy.CapFor(turnCaps.Caps, config.RoleTriager, ScriptRoleTurns["triager"]),
			MaxTurnsRequested: strategy.CapFor(turnCaps.Caps, config.RoleTriager, ScriptRoleTurns["triager"]),
			MaxTurnsSource:    strategy.CapSourceFor(turnCaps.Sources, config.RoleTriager, "script default"),
		}
		// The report's screenshots, shown to a triager that can see. Gated on
		// the declared vision cap: a text-only model sent an image array gets
		// a 400 from most endpoints, and a triage that dies on evidence it
		// cannot read helps nobody. A declared seat whose endpoint already
		// rejected an image is not sent one again (B-515).
		if s.seatCanSee(duckling) {
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
		previousContract := ""
		if previousTaskID := previousFixTask(b.History); previousTaskID != "" {
			if task := s.findTask(ctx, rs.run.ProjectID, previousTaskID); task != nil {
				previousContract = task.Title + "\n\n" + task.Body
			}
		}
		out, err := runner(ctx, turn, duckling, triagePrompt(b, all, previousContract), belt,
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
			"test_strategy":   t.TestStrategy, "test_reason": t.TestReason,
		}
		if len(t.Proposal) > 0 {
			p["proposal"] = t.Proposal
		}
		if len(t.Deliverables) > 0 {
			p["deliverables"] = t.Deliverables
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
	setRunStatus(rs.run, "paused", time.Now())
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
func triagePrompt(b bug.Bug, all []bug.Bug, previousContract string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## The bug\n\n**%s — %s**\n\nReported severity: %s\n\n", b.ID, b.Title, b.Severity)
	if strings.TrimSpace(b.Body) != "" {
		sb.WriteString(strings.TrimSpace(b.Body))
		sb.WriteString("\n\n")
	}
	if b.NeedsTriage {
		sb.WriteString("## Reopen evidence (authoritative)\n\n")
		if reopened, ok := latestReopen(b.History); ok {
			fmt.Fprintf(&sb, "%s\n", reopened.Note)
		}
		for _, related := range all {
			if related.DuplicateOf != b.ID {
				continue
			}
			fmt.Fprintf(&sb, "\n- %s — %s\n  %s\n", related.ID, related.Title, strings.TrimSpace(related.Body))
		}
		if strings.TrimSpace(previousContract) != "" {
			sb.WriteString("\n### Previous consumed task contract\n\n")
			sb.WriteString(strings.TrimSpace(previousContract))
			sb.WriteString("\n")
		}
		sb.WriteString("\nThe previous contract described a fix that failed verification. Write new acceptance slices against this evidence; do not reuse a slice merely because the current tree already satisfies it.\n\n")
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
	return s.BugPromoteWithNote(ctx, projectID, bugID, actor, "")
}

func (s *Service) BugPromoteWithNote(ctx context.Context, projectID, bugID, actor, note string) (map[string]interface{}, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
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
	history := readBugAudit(entry.Path)[rec.ID]
	if bugNeedsRetriage(history) {
		return nil, fmt.Errorf("%s was reopened because its previous fix did not hold; triage it again (or edit and save a fresh contract) before promoting new work", bugID)
	}
	if _, reopened := latestReopen(history); reopened && strings.TrimSpace(note) == "" {
		return nil, fmt.Errorf("%s was reopened; promotion requires a note saying what this attempt must address that the previous fix missed", bugID)
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
	var portions []agent.SplitProposal
	if rec.Proposal != "" {
		if err := json.Unmarshal([]byte(rec.Proposal), &portions); err != nil {
			return nil, fmt.Errorf("read stored split proposal: %w", err)
		}
	}
	promoted, err := preparePromotionPortions(entry.Path, rec, portions)
	if err != nil {
		return nil, err
	}
	reopenContext := promotionReopenEvidence(db, rec, history, note)
	if actor == "" {
		actor = "human"
	}
	plan, taskIDs, err := planWithPromotedTasks(entry.Path, rec, promoted, reopenContext)
	if err != nil {
		return nil, err
	}
	// The promotion is a new revision of the accepted plan, and its front
	// matter says so. B-489's plan gained a milestone and still read version 2,
	// approved_by human, as if nothing had happened since the plan run.
	plan.Front.Version++
	plan.Front.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	plan.Front.ApprovedBy = actor

	// Database first, plan last. The plan write and its commit are the steps
	// that can be refused (a hook, a held index lock), and the database rows
	// are the ones that can be taken back exactly: a refused commit deletes the
	// rows and puts the bug back where it was, so a failed promotion leaves the
	// bug promotable, the plan as HEAD knows it, and no task row nobody can
	// see. The other order left committed tasks whose rows and bug link a
	// database error could fail to write, with no clean way back.
	original := *rec
	var created []string
	bugMoved := false
	rollback := func(cause error) error {
		var undo []string
		for _, id := range created {
			if err := db.DeleteTask(id); err != nil {
				undo = append(undo, fmt.Sprintf("task %s row: %v", id, err))
			}
		}
		// Only a bug that was moved is put back, and verbatim: UpdateBug stamps
		// updated_at with now, so restoring through it (or "restoring" a bug
		// that never changed) left a refused promotion looking like a fresh
		// edit of the report (review of #153).
		if bugMoved {
			restored := original
			if err := db.RestoreBug(&restored); err != nil {
				undo = append(undo, fmt.Sprintf("bug %s: %v", original.ID, err))
			}
		}
		if len(undo) > 0 {
			return fmt.Errorf("%w (and could not undo: %s)", cause, strings.Join(undo, "; "))
		}
		return cause
	}
	for i, taskID := range taskIDs {
		title, body := promotedTaskTitle(rec), promotedTaskBody(rec, reopenContext)
		if len(promoted) > 0 {
			title, body = promoted[i].Title, promotedPortionBody(rec, promoted[i], reopenContext)
		}
		if err := db.CreateTask(&store.Task{ID: taskID, Title: title, Body: body, Status: "todo"}); err != nil {
			return nil, rollback(err)
		}
		created = append(created, taskID)
		if err := db.AddTrace("bug", bugID, "task", taskID); err != nil {
			return nil, rollback(err)
		}
	}

	from := rec.Status
	rec.TaskID = taskIDs[0]
	rec.Status = string(next)
	if err := db.UpdateBug(rec); err != nil {
		return nil, rollback(err)
	}
	bugMoved = true

	planPath := artifact.Path(entry.Path, artifact.KindPlan)
	snap := snapshotDocs(entry.Path, planPath)
	if err := writePlan(entry.Path, plan); err != nil {
		if restoreErr := snap.restore(); restoreErr != nil {
			err = fmt.Errorf("%v (also failed to restore the plan: %v)", err, restoreErr)
		}
		return nil, rollback(err)
	}
	// Only the plan is committed; anything else dirty in the checkout stays
	// the person's (B-489: the TI-36X checkout had unrelated edits pending).
	sha, skipped, err := commitOrRestoreDocs(snap, "promoted plan",
		fmt.Sprintf("ducklab: promote %s to %s", rec.ID, strings.Join(taskIDs, ", ")),
		map[string]string{"Ducklab-Bug": rec.ID, "Ducklab-Action": "bug_promoted"})
	if err != nil {
		return nil, rollback(err)
	}
	appendBugAudit(entry.Path, bug.AuditEntry{
		Bug: rec.ID, From: from, To: rec.Status, Actor: actor, Via: "promote", Note: taskIDs[0],
	})
	// A promote changes what the guide says without any run settling — the
	// exact blind spot of the settle hooks. Poke the loop so an autopilot
	// idling at "promote it" picks the new task up instead of waiting for an
	// accept that will never come.
	go s.autopilotAdvance(projectID)
	out := map[string]interface{}{"bug": bugID, "task": taskIDs[0], "status": rec.Status}
	if len(taskIDs) > 1 {
		out["tasks"] = taskIDs
	}
	if sha != "" {
		out["commit"] = sha
	}
	if skipped != "" {
		out["warning"] = fmt.Sprintf("the plan was updated but not committed: %s", skipped)
	}
	return out, nil
}

// preparePromotionPortions turns a proposed split into executable lanes before
// it reaches the plan. A single portion can safely inherit every suspected
// file. A real split must assign each suspected file explicitly: guessing
// between sibling portions would recreate overlapping lanes under a different
// name. Stack providers contribute shared test roots and registration files.
func preparePromotionPortions(projectRoot string, rec *store.Bug, portions []agent.SplitProposal) ([]promotionPortion, error) {
	if len(portions) == 0 {
		return nil, nil
	}
	out := make([]promotionPortion, len(portions))
	for i, portion := range portions {
		resolved, additions, err := resolvePromotionLanePaths(projectRoot, portion.Owns)
		if err != nil {
			return nil, fmt.Errorf("split proposal portion %q: %w", portion.Title, err)
		}
		portion.Owns = resolved
		out[i] = promotionPortion{SplitProposal: portion, LaneAdditions: additions}
	}
	add := func(index int, path, reason string) {
		path = cleanLanePath(path)
		if path == "" || anyClaimContains(ownsClaims(out[index].Owns), path) {
			return
		}
		out[index].Owns = append(out[index].Owns, path)
		out[index].LaneAdditions = append(out[index].LaneAdditions, fmt.Sprintf("%s (%s)", path, reason))
	}

	for _, raw := range strings.Split(rec.SuspectedFiles, "\n") {
		path, resolution, err := resolvePromotionLanePath(projectRoot, raw)
		if err != nil {
			// suspected_files is advisory evidence, not the enforced lane. Keep
			// Owns strict above, but do not make a triager's guessed or ambiguous
			// basename impossible for the person to promote or edit later.
			note := ignoredSuspectedFileNote(raw, err)
			for i := range out {
				out[i].TriageNotes = append(out[i].TriageNotes, note)
			}
			continue
		}
		if path == "" {
			continue
		}
		owner := -1
		for i := range out {
			if anyClaimContains(ownsClaims(out[i].Owns), path) {
				owner = i
				break
			}
		}
		if owner >= 0 {
			continue
		}
		if len(out) != 1 {
			return nil, fmt.Errorf("split proposal does not assign suspected file %s to a portion; edit the proposal before promoting so Ducklab does not guess between lanes", path)
		}
		reason := "triage suspected file"
		if resolution != "" {
			reason += "; " + resolution
		}
		add(0, path, reason)
	}

	for i := range out {
		for _, source := range append([]string(nil), out[i].Owns...) {
			ext := strings.ToLower(filepath.Ext(source))
			if ext != ".c" && ext != ".cc" && ext != ".cpp" && ext != ".cxx" {
				continue
			}
			stem := strings.TrimSuffix(source, filepath.Ext(source))
			for _, headerExt := range []string{".h", ".hh", ".hpp", ".hxx"} {
				header := stem + headerExt
				if _, err := os.Stat(filepath.Join(projectRoot, filepath.FromSlash(header))); err == nil {
					add(i, header, "existing sibling header")
				}
			}
		}
	}

	cfg, err := config.LoadProject(filepath.Join(projectRoot, ".ducklab", "project.toml"))
	if err != nil {
		return nil, fmt.Errorf("load project config for promotion lane: %w", err)
	}
	if len(out) == 1 && strings.EqualFold(strings.TrimSpace(rec.TestStrategy), "test-first") {
		for _, path := range promotionNamedTestPaths(projectRoot, rec, cfg.Verify.TestGlobs) {
			add(0, path, "test-first suite named by triage")
		}
	}
	var testPortions []int
	testClaims := make([][]string, len(out))
	for i, portion := range out {
		text := portion.Title + "\n" + strings.Join(portion.Acceptance, "\n")
		for _, owned := range portion.Owns {
			if verify.ClaimsTestLane(owned, cfg.Verify.TestGlobs) {
				testClaims[i] = append(testClaims[i], cleanLanePath(owned))
			}
		}
		if len(testClaims[i]) > 0 || verify.MentionsTests(text) {
			testPortions = append(testPortions, i)
		}
	}
	if len(testPortions) == 0 && verify.MentionsTests(rec.Deliverables+"\n"+rec.TestStrategy+"\n"+rec.TestReason) {
		if len(out) == 1 {
			testPortions = []int{0}
		} else {
			var checked []string
			for i, portion := range out {
				owns := "no paths"
				if len(portion.Owns) > 0 {
					owns = strings.Join(portion.Owns, ", ")
				}
				checked = append(checked, fmt.Sprintf("portion %d %q (owns: %s)", i+1, portion.Title, owns))
			}
			globs := cfg.Verify.TestGlobs
			if len(globs) == 0 {
				globs = verify.DefaultTestGlobs
			}
			return nil, fmt.Errorf("split proposal requires test or coverage work but no portion claims it; checked %s. Assign a path matching %s to exactly one portion, or add an acceptance slice such as %q to that portion before promoting", strings.Join(checked, "; "), strings.Join(globs, ", "), "Regression test covers <behavior>")
		}
	}
	if len(testPortions) > 1 {
		// Multiple portions may each own their own regression. What is unsafe is
		// shared ownership of the same test file or tree: those tasks would race
		// and the plan's lane checker would reject them anyway. The old count-only
		// rule mistook an ordinary backend + frontend split for shared ownership.
		for _, portion := range testPortions {
			if len(testClaims[portion]) == 0 {
				return nil, fmt.Errorf("split portion %d %q requires tests but owns no test path; assign its regression file or test root explicitly before promoting", portion+1, out[portion].Title)
			}
		}
		for left := 0; left < len(testPortions); left++ {
			for right := left + 1; right < len(testPortions); right++ {
				i, j := testPortions[left], testPortions[right]
				for _, a := range testClaims[i] {
					for _, b := range testClaims[j] {
						if lanePathsOverlap(a, b) {
							return nil, fmt.Errorf("split portions %d and %d share test lane %q / %q; give that regression path to only one portion", i+1, j+1, a, b)
						}
					}
				}
			}
		}
	}
	if len(testPortions) == 1 {
		profile, _ := capability.DefaultRegistry().ResolveProject(capability.Context{ProjectRoot: projectRoot, Policies: cfg.Capabilities.Policy}, cfg.Capabilities.Auto, cfg.Capabilities.Enabled, cfg.Capabilities.Disabled)
		for _, hints := range promotionStackLaneHints(profile, out[testPortions[0]].Owns) {
			for _, root := range uniqueStrings(hints.TestRoots) {
				add(testPortions[0], root, "stack test root")
			}
			for _, file := range uniqueStrings(hints.TestRegistrationFiles) {
				add(testPortions[0], file, "stack test registration")
			}
		}
	}
	return out, nil
}

func promotionNamedTestPaths(projectRoot string, rec *store.Bug, globs []string) []string {
	if rec == nil {
		return nil
	}
	// SuspectedFiles is where the triage contract puts concrete paths. The
	// behavioural test reason often names no file at all (TI-36X B-003), so
	// ignoring this field silently dropped the exact suite test-first needed.
	// Text becomes Owns here, so it must name a real or plausible new file
	// (B-503: prose like `angle/2nd/menu` was offered as a lane).
	text := rec.TestReason + "\n" + rec.Deliverables + "\n" + rec.SuspectedFiles
	seen := map[string]bool{}
	var paths []string
	for _, loc := range advisorPathPattern.FindAllStringIndex(text, -1) {
		path := cleanLanePath(text[loc[0]:loc[1]])
		if path == "" || seen[path] || !verify.ClaimsTestLane(path, globs) || !plausibleLanePath(path, projectRoot) {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
}

// promotionStackLaneHints returns hints from the stack(s) represented by a
// portion's owned source files. A project profile is deliberately polyglot;
// applying its aggregate hints would make a Go regression claim Node's package
// registration merely because the repository also has a frontend.
func promotionStackLaneHints(profile capability.Profile, owns []string) []capability.LaneHints {
	var hints []capability.LaneHints
	stackIDs := make([]string, 0, len(profile.StackLaneHints))
	for id := range profile.StackLaneHints {
		stackIDs = append(stackIDs, id)
	}
	sort.Strings(stackIDs)
	for _, id := range stackIDs {
		stackHints := profile.StackLaneHints[id]
		for _, owned := range owns {
			if slices.Contains(stackHints.TestExtensions, strings.ToLower(filepath.Ext(owned))) {
				hints = append(hints, stackHints)
				break
			}
		}
	}
	// Triagers may assign directories rather than concrete files. With no
	// extension evidence, keep the historical aggregate hints so a test-owning
	// portion still receives a writable test root and registration file.
	if len(hints) == 0 {
		return []capability.LaneHints{profile.LaneHints}
	}
	return hints
}

func lanePathsOverlap(a, b string) bool {
	a, b = strings.TrimSuffix(cleanLanePath(a), "/"), strings.TrimSuffix(cleanLanePath(b), "/")
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// resolvePromotionLanePaths turns the model's lane vocabulary into paths the
// write invariant can actually enforce. Repository-relative paths are kept as
// written. A bare basename is only useful when it identifies exactly one item
// in the project tree; otherwise promotion stops before creating an
// impossible task.
func resolvePromotionLanePaths(projectRoot string, raw []string) ([]string, []string, error) {
	var paths, additions []string
	for _, item := range raw {
		path, resolution, err := resolvePromotionLanePath(projectRoot, item)
		if err != nil {
			return nil, nil, err
		}
		if path == "" {
			continue
		}
		paths = append(paths, path)
		if resolution != "" {
			additions = append(additions, fmt.Sprintf("%s (%s)", path, resolution))
		}
	}
	return uniqueStrings(paths), uniqueStrings(additions), nil
}

func resolvePromotionLanePath(projectRoot, raw string) (string, string, error) {
	path := cleanLanePath(raw)
	if path == "" || strings.Contains(path, "/") {
		return path, "", nil
	}
	// Root-level files and directories such as meson.build and tests are
	// already valid repository-relative paths, despite having no slash.
	if _, err := os.Stat(filepath.Join(projectRoot, filepath.FromSlash(path))); err == nil {
		return path, "", nil
	}

	var matches []string
	err := filepath.WalkDir(projectRoot, func(candidate string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".ducklab", "build", "node_modules", "vendor":
				if candidate != projectRoot {
					return filepath.SkipDir
				}
			}
		}
		if candidate == projectRoot || entry.Name() != path {
			return nil
		}
		rel, relErr := filepath.Rel(projectRoot, candidate)
		if relErr != nil {
			return relErr
		}
		matches = append(matches, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", "", fmt.Errorf("resolve bare lane path %q: %w", path, err)
	}
	slices.Sort(matches)
	switch len(matches) {
	case 0:
		return "", "", &promotionLanePathError{Path: path}
	case 1:
		return matches[0], fmt.Sprintf("resolved bare lane %s", path), nil
	default:
		return "", "", &promotionLanePathError{Path: path, Matches: matches}
	}
}

type promotionLanePathError struct {
	Path    string
	Matches []string
}

func (e *promotionLanePathError) Error() string {
	if len(e.Matches) == 0 {
		return fmt.Sprintf("bare lane path %q matches no repository path; use a repository-relative path", e.Path)
	}
	return fmt.Sprintf("bare lane path %q is ambiguous; use one of: %s", e.Path, strings.Join(e.Matches, ", "))
}

func ignoredSuspectedFileNote(raw string, err error) string {
	path := cleanLanePath(raw)
	if resolutionErr, ok := err.(*promotionLanePathError); ok {
		if len(resolutionErr.Matches) == 0 {
			return fmt.Sprintf("suspected file %s ignored: no such repository path", resolutionErr.Path)
		}
		return fmt.Sprintf("suspected file %s ignored: basename is ambiguous (%s)", resolutionErr.Path, strings.Join(resolutionErr.Matches, ", "))
	}
	return fmt.Sprintf("suspected file %s ignored: %v", path, err)
}

// promotedPortionBody puts a split portion's contract ahead of the original
// report. The report can describe the whole incident (and therefore sibling
// work), so it is useful evidence but must not become this task's checklist.
type promotionPortion struct {
	agent.SplitProposal
	LaneAdditions []string
	TriageNotes   []string
}

func promotedPortionBody(b *store.Bug, portion promotionPortion, reopenContext string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Fixes %s.\n\n## Current portion contract (authoritative)\n\n**Acceptance slices:**\n", b.ID)
	for _, criterion := range portion.Acceptance {
		fmt.Fprintf(&sb, "- %s\n", criterion)
	}
	if len(portion.Owns) > 0 {
		fmt.Fprintf(&sb, "\n**Owns:** %s\n", strings.Join(portion.Owns, ", "))
	}
	if len(portion.LaneAdditions) > 0 {
		fmt.Fprintf(&sb, "\n- **Lane widened at promote**: %s\n", strings.Join(portion.LaneAdditions, "; "))
	}
	if len(portion.TriageNotes) > 0 {
		fmt.Fprintf(&sb, "\n- **Triage notes at promote**: %s\n", strings.Join(uniqueStrings(portion.TriageNotes), "; "))
	}
	sb.WriteString("\nOnly the Acceptance slices and Owns above are required for this portion.\n")
	sb.WriteString("\n## Parent context (non-binding)\n\n")
	sb.WriteString(promotedTaskBody(b, reopenContext))
	return sb.String()
}

// reporterBoldFieldLabel moves the colon outside reporter-authored labels only
// where the artifact grammar would read one: at the start of a prose line.
// Inline quotations and fenced examples are evidence and stay byte-for-byte.
var reporterBoldFieldLabel = regexp.MustCompile(`^(\s*)\*\*([^*\n]+):\*\*`)

func reporterContext(body string) string {
	lines := strings.Split(body, "\n")
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence == "" {
			lines[i] = reporterBoldFieldLabel.ReplaceAllString(line, "$1**$2**:")
		}
	}
	return strings.Join(lines, "\n")
}

func promotedTaskBody(b *store.Bug, reopenContext string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Fixes %s.\n\n", b.ID)
	if strings.TrimSpace(reopenContext) != "" {
		sb.WriteString(reopenEvidenceHeading + "\n\n")
		sb.WriteString(strings.TrimSpace(reporterContext(reopenContext)))
		sb.WriteString("\n\nAn unchanged tree cannot satisfy this task: it exists because the previous fix did not hold.\n\n")
	}
	if strings.TrimSpace(b.Body) != "" {
		sb.WriteString("## Reported\n\n")
		sb.WriteString(strings.TrimSpace(reporterContext(b.Body)))
		sb.WriteString("\n")
	}
	// The implementer's numbered work contract, in the same shape the plan
	// architects are dictated (stage.TaskBodyContract): top-level bullets
	// under the canonical **Acceptance slices:** field become the checklist it
	// reports against. A
	// promoted bug was the one door into the build loop without one.
	if b.Deliverables != "" {
		sb.WriteString("\n**Acceptance slices:**\n")
		for _, line := range strings.Split(b.Deliverables, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				fmt.Fprintf(&sb, "- %s\n", line)
			}
		}
	}
	if b.Component != "" || b.SuspectedFiles != "" || b.TriageReason != "" {
		sb.WriteString("\n## Triage\n\n")
		if b.Component != "" {
			fmt.Fprintf(&sb, "- **Component**: %s\n", b.Component)
		}
		if b.SuspectedFiles != "" {
			fmt.Fprintf(&sb, "- **Suspected files**: %s\n",
				strings.Join(strings.Split(b.SuspectedFiles, "\n"), ", "))
		}
		if b.TriageReason != "" {
			sb.WriteString("\n" + strings.TrimSpace(reporterContext(b.TriageReason)) + "\n")
		}
		if b.TestStrategy != "" {
			fmt.Fprintf(&sb, "\n- **Verification (triage recommends)**: %s", b.TestStrategy)
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

func promotionReopenEvidence(db *store.DB, rec *store.Bug, history []bug.AuditEntry, promoteNote string) string {
	reopened, ok := latestReopen(history)
	if !ok {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Reopened: %s", strings.TrimSpace(reopened.Note))
	if reopened.TS != "" {
		fmt.Fprintf(&sb, " (%s)", reopened.TS)
	}
	sb.WriteString(".\n")
	if strings.TrimSpace(promoteNote) != "" {
		fmt.Fprintf(&sb, "\nPerson's promote note (verbatim):\n%s\n", strings.TrimSpace(promoteNote))
	}
	reports, err := db.ListBugs()
	if err != nil {
		return sb.String()
	}
	for _, report := range reports {
		if report.DuplicateOf != rec.ID {
			continue
		}
		fmt.Fprintf(&sb, "\nEvidence linked from %s — %s:\n%s\n", report.ID, report.Title, strings.TrimSpace(report.Body))
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
const reopenEvidenceHeading = "## Reopen evidence (authoritative)"

// planWithPromotedTasks adds one task per proposal portion, each with its own
// lane, to the plan in memory; the caller writes and commits it. With no
// portions it preserves the legacy single-task promotion exactly.
func planWithPromotedTasks(projectRoot string, rec *store.Bug, portions []promotionPortion, reopenContext string) (*artifact.Document, []string, error) {
	plan, err := artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil {
		return nil, nil, err
	}
	if plan == nil {
		plan = &artifact.Document{Front: artifact.Frontmatter{Kind: artifact.KindPlan}}
	}
	var existing []artifact.Section
	for _, m := range plan.Sections {
		existing = append(existing, m.Children...)
	}
	hasProposal := len(portions) > 0
	if !hasProposal {
		portions = []promotionPortion{{SplitProposal: agent.SplitProposal{Title: promotedTaskTitle(rec)}}}
	}
	// One shared milestone per promotion: every portion lands under the same
	// "Reported bugs" heading, keeping the plan document's structure readable.
	// N separate identically titled milestones would be a readability smell.
	ids := make([]string, 0, len(portions))
	for _, portion := range portions {
		id := fmt.Sprintf("T-%03d", stage.NextFree(existing, "T"))
		existing = append(existing, artifact.Section{ID: id})
		task := artifact.Section{ID: id, Title: portion.Title, Body: promotedTaskBody(rec, reopenContext)}
		if hasProposal {
			task.Body = promotedPortionBody(rec, portion, reopenContext)
			task.Owns = portion.Owns
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
				ID: fmt.Sprintf("M-%03d", stage.NextFree(plan.Sections, "M")), Title: bugsMilestoneTitle,
				Children: []artifact.Section{task},
			})
		}
		ids = append(ids, id)
	}
	plan.Front.Kind = artifact.KindPlan
	return plan, ids, nil
}

// writePlan writes the accepted plan.
func writePlan(projectRoot string, plan *artifact.Document) error {
	if err := os.MkdirAll(artifact.DocsDir(projectRoot), 0o755); err != nil {
		return err
	}
	return os.WriteFile(artifact.Path(projectRoot, artifact.KindPlan), []byte(artifact.Render(plan)), 0o644)
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
	entry, entryErr := s.registry.Get(projectID)
	var audit map[string][]bug.AuditEntry
	if entryErr == nil {
		audit = readBugAudit(entry.Path)
	}

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
		refreshing := bug.Status(rec.Status) == bug.Triaged && bugNeedsRetriage(audit[rec.ID])
		if sev, _ := p["severity"].(string); sev != "" {
			rec.Severity = sev
		}
		invalidDuplicate := ""
		if dup, _ := p["duplicate_of"].(string); dup != "" {
			target, targetErr := db.GetBug(dup)
			if targetErr != nil {
				rec.DuplicateOf = ""
				invalidDuplicate = fmt.Sprintf("duplicate target %s does not exist; kept for human triage", dup)
			} else {
				targetStatus := bug.Status(target.Status)
				if targetStatus != bug.Open && targetStatus != bug.Triaged && targetStatus != bug.InProgress {
					rec.DuplicateOf = ""
					invalidDuplicate = fmt.Sprintf("duplicate target %s is %s; kept for human triage instead of retiring this report", dup, target.Status)
				} else {
					rec.DuplicateOf = dup
				}
			}
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
		if invalidDuplicate != "" {
			if rec.TriageReason != "" {
				rec.TriageReason += "; "
			}
			rec.TriageReason += invalidDuplicate
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
		if items, ok := p["deliverables"].([]interface{}); ok {
			var lines []string
			for _, it := range items {
				if line, _ := it.(string); strings.TrimSpace(line) != "" {
					lines = append(lines, strings.TrimSpace(line))
				}
			}
			rec.Deliverables = strings.Join(lines, "\n")
		}
		if items, ok := p["deliverables"].([]string); ok {
			rec.Deliverables = strings.Join(items, "\n")
		}
		if proposal, ok := p["proposal"]; ok {
			data, err := json.Marshal(proposal)
			if err != nil {
				return applied, fmt.Errorf("store split proposal: %w", err)
			}
			rec.Proposal = string(data)
		}
		if refreshing && strings.TrimSpace(rec.Proposal) == "" {
			return applied, fmt.Errorf("retriage %s must produce a fresh proposal with acceptance slices before it can be promoted again", rec.ID)
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
				if entryErr == nil {
					appendBugAudit(entry.Path, bug.AuditEntry{
						Bug: rec.ID, From: from, To: rec.Status, Actor: "engine", Via: "triage",
					})
				}
			}
		}
		if err := db.UpdateBug(rec); err != nil {
			return applied, err
		}
		if refreshing && entryErr == nil {
			refresh := bug.AuditEntry{
				Bug: rec.ID, From: rec.Status, To: rec.Status, Actor: "engine", Via: "retriage",
				Note: "fresh contract recorded after reopen",
			}
			appendBugAudit(entry.Path, refresh)
			audit[rec.ID] = append(audit[rec.ID], refresh)
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
		if rec.TaskID != taskID && !bugActiveProposalContainsTask(db, rec.ID, rec.TaskID, taskID) {
			continue
		}
		if rec.Proposal != "" {
			if err := db.SetTaskStatus(taskID, "accepted"); err != nil {
				return "", err
			}
			all, _, err := bugProposalTasksAccepted(db, rec.ID, rec.TaskID)
			if err != nil {
				return "", err
			}
			if !all {
				return "", nil
			}
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

// boardTaskStatus is each task's status as the board shows it: derived from
// the runs, not the tasks table, which only promote and the fixed gate write.
// An unreadable plan yields an empty map and bugTasks falls back to the table.
func (s *Service) boardTaskStatus(ctx context.Context, projectID string) map[string]string {
	out := map[string]string{}
	tasks, err := s.TaskList(ctx, projectID)
	if err != nil {
		return out
	}
	for _, t := range tasks {
		out[t.ID] = t.Status
	}
	return out
}

// bugTasks lists every task a report's trace edges name, in promotion order,
// marking the current promotion the same way bugProposalTasksAccepted counts
// it: from the bound first task onward.
func bugTasks(db *store.DB, activeFirstTask string, traces []string, status map[string]string) []bug.Task {
	var out []bug.Task
	active := false
	for _, trace := range traces {
		id, ok := strings.CutPrefix(trace, "task:")
		if !ok {
			continue
		}
		if activeFirstTask != "" && id == activeFirstTask {
			active = true
		}
		st := status[id]
		if st == "" {
			if task, err := db.GetTask(id); err == nil {
				st = task.Status
			}
		}
		out = append(out, bug.Task{ID: id, Status: st, Current: active})
	}
	return out
}

func bugActiveProposalContainsTask(db *store.DB, bugID, activeFirstTask, taskID string) bool {
	if activeFirstTask == "" {
		return false
	}
	traces, err := db.TracesFrom("bug", bugID)
	if err != nil {
		return false
	}
	active := false
	for _, trace := range traces {
		if trace == "task:"+activeFirstTask {
			active = true
		}
		if active && trace == "task:"+taskID {
			return true
		}
	}
	return false
}

func bugProposalTasksAccepted(db *store.DB, bugID, activeFirstTask string) (bool, []string, error) {
	// Trace edges are permanent provenance, while TaskID names the first task
	// in the current promotion. A reopened bug keeps its old edges so history
	// remains navigable, but those consumed (or abandoned) tasks must not enter
	// the fixed gate of the new attempt. Promotions allocate monotonically
	// increasing IDs and add each split portion in order, so the current
	// generation begins at activeFirstTask in the bug's ordered trace list.
	if activeFirstTask == "" {
		return true, nil, nil
	}
	traces, err := db.TracesFrom("bug", bugID)
	if err != nil {
		return false, nil, err
	}
	tasks := 0
	active := false
	var pending []string
	for _, trace := range traces {
		id, ok := strings.CutPrefix(trace, "task:")
		if !ok {
			continue
		}
		if id == activeFirstTask {
			active = true
		}
		if !active {
			continue
		}
		task, err := db.GetTask(id)
		if err != nil {
			return false, nil, err
		}
		tasks++
		if task.Status != "accepted" {
			pending = append(pending, fmt.Sprintf("%s (%s)", id, task.Status))
		}
	}
	if !active {
		return false, []string{activeFirstTask + " (missing trace)"}, nil
	}
	// A proposal with no promoted task has no portion in flight: nothing can
	// land, so nothing blocks. Refusing here stranded every bug the triager
	// had read but a person then fixed by hand (B-286, 2026-08-29): the gate
	// is "every portion landed", not "a proposal exists".
	return tasks == 0 || len(pending) == 0, pending, nil
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
	entry, entryErr := s.registry.Get(projectID)
	db, err := s.openProjectDB(projectID)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rec, err := db.GetBug(bugID)
	if err != nil {
		return nil, fmt.Errorf("no bug %s", bugID)
	}
	needsFreshContract := entryErr == nil && bugNeedsRetriage(readBugAudit(entry.Path)[rec.ID])
	freshContract := false
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
	// The split is the person's to write, correct or discard — the triager
	// only recommends one. Until promote, that is: the portions became tasks
	// then, and editing a proposal that was already consumed would describe a
	// split the plan does not have.
	if req.Proposal != nil {
		if rec.TaskID != "" || (bug.Status(rec.Status) != bug.Open && bug.Status(rec.Status) != bug.Triaged) {
			became := rec.TaskID
			if became == "" {
				became = "a task"
			}
			return nil, fmt.Errorf("%s is %s: its split was consumed when it became %s; edit the tasks instead",
				rec.ID, rec.Status, became)
		}
		portions, err := bug.ValidatePortions(*req.Proposal)
		if err != nil {
			return nil, fmt.Errorf("split proposal: %w", err)
		}
		if len(portions) == 0 {
			rec.Proposal = ""
		} else {
			data, err := json.Marshal(portions)
			if err != nil {
				return nil, fmt.Errorf("store split proposal: %w", err)
			}
			rec.Proposal = string(data)
			freshContract = needsFreshContract
		}
	}
	if err := db.UpdateBug(rec); err != nil {
		return nil, err
	}
	out := toBug(rec)
	if freshContract {
		audit := bug.AuditEntry{
			Bug: rec.ID, From: rec.Status, To: rec.Status, Actor: "human", Via: "contract-edit",
			Note: "fresh contract recorded after reopen",
		}
		appendBugAudit(entry.Path, audit)
		out.History = []bug.AuditEntry{audit}
	}
	return out, nil
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

	plan, removed, unreferenced, err := planWithoutTask(entry.Path, taskID)
	if err != nil {
		return nil, err
	}
	if !removed {
		return nil, fmt.Errorf("no task %s in the plan", taskID)
	}

	// Everything the removal will change in the database, read before any of
	// it changes, so a refused later step can put back exactly what was there.
	// A plan-only task (no row) has nothing to restore.
	savedTask, err := db.GetTask(taskID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("cannot remove %s: read its database row: %w", taskID, err)
	}
	savedEdges, err := db.TaskEdges(taskID)
	if err != nil {
		return nil, fmt.Errorf("cannot remove %s: read its trace edges: %w", taskID, err)
	}
	// The report goes back to where it was, or it would sit in in_progress
	// forever pointing at a task nobody can find.
	bugs, err := db.ListBugs()
	if err != nil {
		return nil, fmt.Errorf("cannot remove %s: read the bug that points at it: %w", taskID, err)
	}
	var linked []store.Bug
	for _, rec := range bugs {
		if rec.TaskID == taskID {
			linked = append(linked, *rec)
		}
	}

	// Database first, plan and commit last — the order promotion uses. The
	// first version committed the plan and then ran the database half
	// best-effort: a failed DeleteTask or bug reset returned success with a
	// warning while HEAD no longer had the task, and a retry could not find
	// the section to remove (review of #153). Every step here either completes
	// or is undone, and any failure is an error.
	var resetBugs []store.Bug
	taskDeleted := false
	rollback := func(cause error) error {
		var undo []string
		for i := range resetBugs {
			// Verbatim, updated_at included: see RestoreBug.
			restored := resetBugs[i]
			if err := db.RestoreBug(&restored); err != nil {
				undo = append(undo, fmt.Sprintf("bug %s: %v", restored.ID, err))
			}
		}
		if taskDeleted && savedTask != nil {
			if err := db.RestoreTask(savedTask, savedEdges); err != nil {
				undo = append(undo, fmt.Sprintf("task %s row: %v", taskID, err))
			}
		}
		if len(undo) > 0 {
			return fmt.Errorf("remove %s: %w (and could not undo: %s)", taskID, cause, strings.Join(undo, "; "))
		}
		return fmt.Errorf("remove %s: %w", taskID, cause)
	}
	if err := db.DeleteTask(taskID); err != nil {
		return nil, rollback(err)
	}
	taskDeleted = true
	type bugMove struct{ id, from, to string }
	var moves []bugMove
	for _, original := range linked {
		rec := original
		rec.TaskID = ""
		if next, mErr := bug.Move(bug.Status(rec.Status), bug.Triaged); mErr == nil {
			rec.Status = string(next)
		}
		if err := db.UpdateBug(&rec); err != nil {
			return nil, rollback(err)
		}
		resetBugs = append(resetBugs, original)
		moves = append(moves, bugMove{rec.ID, original.Status, rec.Status})
	}

	// Left in the working tree, the removal was the mirror of B-489: history
	// still carried a task the plan no longer did, and a worktree cut from
	// the branch would offer it to a build.
	planPath := artifact.Path(entry.Path, artifact.KindPlan)
	snap := snapshotDocs(entry.Path, planPath)
	if err := writePlan(entry.Path, plan); err != nil {
		if restoreErr := snap.restore(); restoreErr != nil {
			err = fmt.Errorf("%v (also failed to restore the plan: %v)", err, restoreErr)
		}
		return nil, rollback(err)
	}
	sha, skipped, err := commitOrRestoreDocs(snap, "plan task removal",
		fmt.Sprintf("ducklab: remove %s from the plan", taskID),
		map[string]string{"Ducklab-Task": taskID, "Ducklab-Action": "task_removed"})
	if err != nil {
		return nil, rollback(err)
	}

	out := map[string]interface{}{"removed": taskID}
	if sha != "" {
		out["commit"] = sha
	}
	if skipped != "" {
		out["warning"] = fmt.Sprintf("the plan was updated but not committed: %s", skipped)
	}
	if len(unreferenced) > 0 {
		// Said, so the person knows which tasks just changed under them.
		out["dependencies_cleaned"] = unreferenced
	}
	for i, m := range moves {
		if i == 0 {
			out["bug"] = m.id
			out["bug_status"] = m.to
		}
		appendBugAudit(entry.Path, bug.AuditEntry{
			Bug: m.id, From: m.from, To: m.to, Actor: "engine",
			Via: "task-removed", Note: taskID,
		})
	}
	return out, nil
}

// planWithoutTask takes a task out of the plan in memory, and the milestone
// with it if that leaves it empty; the caller writes and commits it.
//
// The document is what allocates ids and what every reader parses, so a removal
// that only touched the database would leave the task visible everywhere anyone
// actually looks.
func planWithoutTask(projectRoot, taskID string) (plan *artifact.Document, removed bool, unreferenced []string, err error) {
	plan, err = artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil {
		return nil, false, nil, err
	}
	if plan == nil {
		return nil, false, nil, nil
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
		return nil, false, nil, nil
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
	// A removal is a new revision of the accepted plan (B-489). Who approved
	// it is not known here, so approved_by keeps its last attribution.
	plan.Front.Version++
	plan.Front.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return plan, true, unreferenced, nil
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

// triageSubject names what a triage run is reading, in the space a task id
// would occupy: the bug ids when they fit on a row, the count when they
// would not. "triage" alone made every triage row identical.
func triageSubject(todo []bug.Bug) string {
	if len(todo) == 0 {
		return ""
	}
	if len(todo) <= 3 {
		ids := make([]string, len(todo))
		for i, b := range todo {
			ids[i] = b.ID
		}
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%d open bugs", len(todo))
}
