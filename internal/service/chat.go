package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

// Chat: a conversation with a chosen duckling ABOUT something — a bug whose
// fix landed and did not fix it, a task that went sideways — with the
// subject's history as context and read-only tools to investigate.
//
// The person's words: "this bug is not fixed; check why the 401 still
// happens". That is not a run to launch or a gate to decide — it is a
// consultation, and until now the only consultant was outside the product.
// A chat IS a run (stage "chat"): it gets the record, the spend tracking,
// the live stream and the transcript for free, and the run view is the
// conversation panel.
//
// The consultant reads and advises; it does not touch the tree. Its closing
// duty is to suggest actions from the person's own menu — reopen the bug,
// file a new one, relaunch the task with a note — which the person executes
// with the buttons that already exist (I2).
//
// One exception, and only on the person's explicit word: bug_file. A
// consultant that had verified every page and written the complete report
// ended with "file a new bug with the title above" — and the person carried
// two thousand characters to the form by hand. The instruction in the chat
// IS the click (I2 holds); the persona forbids filing uninvited, and a bug
// is loop data — additive, closable — not a tree mutation.

// chatToolbelt is read-only investigation — code, history, records — plus
// bug_file, the one loop-side act a conversation can conclude in.
const chatToolbelt = "fs_read,fs_search,fs_list,git_log,git_diff,task_read,bug_read,bug_file,artifact_read,run_list,run_read"

// ChatRequest starts a conversation about a subject.
type ChatStartRequest struct {
	// Duckling is the model the person chose from the fleet.
	Duckling string `json:"duckling"`
	// AboutKind and AboutID name the subject: "bug" B-004, "task" T-050.
	AboutKind string `json:"about_kind"`
	AboutID   string `json:"about_id"`
	// Message is the person's opening question.
	Message string `json:"message"`
}

// ChatStart opens the conversation: one run, stage "chat", the subject's
// dossier assembled deterministically into the first prompt.
func (s *Service) ChatStart(ctx context.Context, projectID string, req ChatStartRequest) (*runlog.Run, error) {
	if strings.TrimSpace(req.Message) == "" {
		return nil, fmt.Errorf("a chat starts with a question")
	}
	if strings.TrimSpace(req.Duckling) == "" {
		return nil, fmt.Errorf("pick the duckling to talk to")
	}
	if _, err := s.ducklings.Get(config.DucklingID(req.Duckling)); err != nil {
		return nil, err
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}

	run := &runlog.Run{
		ID:        runlog.GenerateRunID(),
		ProjectID: projectID,
		Stage:     "chat",
		Mode:      "solo",
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Stream:    true,
		Gate:      "none",
		Roster:    map[string]string{"consultant": req.Duckling},
		// The subject rides the record: the runs list should say what a chat
		// was about without opening it.
		Note: strings.TrimSpace(fmt.Sprintf("chat about %s %s", req.AboutKind, req.AboutID)),
	}
	if req.AboutKind == "task" {
		run.TaskID = req.AboutID
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
		"stage": "chat", "mode": "solo", "task_id": run.TaskID,
		"about": req.AboutKind + " " + req.AboutID, "duckling": req.Duckling,
	})
	// The person's opening message, on the record like every turn after it.
	writer.AppendEvent("message", map[string]interface{}{
		"role": "human", "content": req.Message,
	})

	// Read-only tools touch no tree: a chat may run beside anything.
	s.queue.submit(s, &queued{
		rs: rs, ctx: runCtx, parallel: true,
		exec: func(c context.Context) { s.executeChatTurn(c, rs, entry.Path, req.AboutKind, req.AboutID, req.Duckling) },
	})
	return run, nil
}

// ChatSend continues a paused conversation with the person's next message.
func (s *Service) ChatSend(ctx context.Context, runID, message string) (*runlog.Run, error) {
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("say something")
	}
	s.runsMu.RLock()
	rs, ok := s.runs[runID]
	s.runsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	if rs.run.Stage != "chat" {
		return nil, fmt.Errorf("%s is a %s run, not a chat", runID, rs.run.Stage)
	}
	if rs.run.Status != "paused" || rs.run.PendingKind != "chat" {
		return nil, fmt.Errorf("the chat is not waiting for you (status %s)", rs.run.Status)
	}
	w, err := s.ensureWriter(rs)
	if err != nil {
		return nil, err
	}
	entry, err := s.entryFor(rs)
	if err != nil {
		return nil, err
	}

	about := strings.TrimPrefix(rs.run.Note, "chat about ")
	kind, id, _ := strings.Cut(about, " ")
	duckling := rs.run.Roster["consultant"]

	w.AppendEvent("message", map[string]interface{}{
		"role": "human", "content": message,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	rs.cancel = cancel
	rs.done = make(chan struct{})
	rs.run.Status = "running"
	clearPending(rs.run)
	w.WriteState()
	s.queue.submit(s, &queued{
		rs: rs, ctx: runCtx, parallel: true,
		exec: func(c context.Context) { s.executeChatTurn(c, rs, entry.Path, kind, id, duckling) },
	})
	return rs.run, nil
}

// ChatEnd closes a conversation as what it was: finished, not failed. Abort
// was the only exit, and a consultation that did its job ended ABORTED on
// the record — a word that means the person gave up on it.
func (s *Service) ChatEnd(ctx context.Context, runID string) (*runlog.Run, error) {
	s.runsMu.RLock()
	rs, ok := s.runs[runID]
	s.runsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	if rs.run.Stage != "chat" {
		return nil, fmt.Errorf("%s is a %s run, not a chat", runID, rs.run.Stage)
	}
	if rs.run.Status != "paused" || rs.run.PendingKind != "chat" {
		return nil, fmt.Errorf("the chat is not at rest (status %s); wait for the reply or abort", rs.run.Status)
	}
	w, err := s.ensureWriter(rs)
	if err != nil {
		return nil, err
	}
	rs.run.Status = "done"
	rs.run.Resolution = "ended by human"
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	clearPending(rs.run)
	w.AppendEvent("human", map[string]interface{}{"action": "end_chat"})
	w.AppendEvent("run_end", map[string]interface{}{"verdict": ""})
	if err := w.WriteState(); err != nil {
		return nil, err
	}
	return rs.run, nil
}

// executeChatTurn runs ONE consultant reply: dossier + conversation so far +
// the person's last message, with read-only tools, then pauses for the next.
func (s *Service) executeChatTurn(ctx context.Context, rs *runState, projectRoot, aboutKind, aboutID, ducklingID string) {
	defer recoverRun(rs)
	defer close(rs.done)

	projCfg, err := config.LoadProject(filepath.Join(projectRoot, ".ducklab", "project.toml"))
	if err != nil {
		s.failRun(rs, fmt.Errorf("load project config: %w", err))
		return
	}
	tracker := rs.tracker
	if tracker == nil {
		limits := &budget.Budget{
			MaxUSD:    projCfg.Budget.MaxUSD,
			MaxTokens: int64(s.cfg.Defaults.Budget.MaxTokens),
			MaxTurns:  s.cfg.Defaults.Budget.MaxTurns,
			// No wallclock ceiling: the tracker's clock starts when the
			// conversation opens and never stops, so it measures the
			// PERSON's thinking time between messages, not the model's
			// work. A chat left open through an afternoon died mid-question
			// at 7515s against the 1800s meant to stop runaway runs. Each
			// reply is still bounded — turn caps, provider timeouts — and
			// tokens and dollars, which measure real spend, keep their caps.
			MaxWallclockS: 0,
		}
		merged := projectBudget(*limits, projCfg.Budget)
		// Chat never caps wallclock time: the clock includes the person's
		// thinking time between messages, not just model work.
		merged.MaxWallclockS = 0
		limits = &merged
		tracker = budget.NewTracker(limits)
		recordLimits(rs, limits)
		rs.setTracker(tracker)
	}
	ectx := &tools.ExecContext{
		ProjectRoot: projectRoot,
		RunID:       rs.run.ID,
		Autonomy:    config.AutonomyGuarded,
		ShellPolicy: projCfg.Shell,
	}
	cache := &loopCache{
		svc: s, tracker: tracker,
		writer:  s.llmWriter(rs, tracker),
		capLift: rs.capLifted.Load,
		loops:   map[config.DucklingID]*agent.Loop{},
	}
	s.attachStreaming(rs, cache)

	prompt := s.chatPromptFor(ctx, rs, projectRoot, aboutKind, aboutID)

	loop, err := cache.get(ctx, config.DucklingID(ducklingID))
	if err != nil {
		s.failRun(rs, err)
		return
	}
	turnNo := chatTurnCount(rs)
	rs.writer.AppendEvent("turn_start", map[string]interface{}{
		"round": turnNo, "turn": 0, "role": "consultant", "duckling": ducklingID,
	})
	belt := strings.Split(chatToolbelt, ",")
	outcome, terr := agent.RunTurn(ctx, loop, &agent.Turn{
		Role: config.RoleArchitect, Duckling: config.DucklingID(ducklingID),
		Prompt: prompt, Toolbelt: belt, Contract: "freeform",
		MaxTurns: 12, Persona: "consultant",
		Round: turnNo, Index: 0,
	}, ectx)
	recordSpend(rs, tracker)
	s.publishSpend(rs, tracker)
	if terr != nil {
		if outcome != nil && strings.TrimSpace(outcome.Text) != "" {
			// A partial answer is still an answer worth reading.
			rs.writer.AppendEvent("message", map[string]interface{}{
				"round": turnNo, "turn": 0, "role": "consultant",
				"duckling": ducklingID, "content": outcome.Text,
			})
		}
		s.failRun(rs, terr)
		return
	}
	rs.writer.AppendEvent("message", map[string]interface{}{
		"round": turnNo, "turn": 0, "role": "consultant",
		"duckling": ducklingID, "content": outcome.Text,
	})
	rs.writer.AppendEvent("turn_end", map[string]interface{}{
		"round": turnNo, "turn": 0, "role": "consultant",
	})

	rs.run.Status = "paused"
	rs.run.PendingKind = "chat"
	rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
	rs.writer.AppendEvent("human_needed", map[string]interface{}{"kind": "chat"})
	rs.writer.WriteState()
}

// chatTurnCount numbers the consultant's replies so each lands in its own
// lane block.
func chatTurnCount(rs *runState) int {
	events, err := runlog.ReadEvents(rs.runDir)
	if err != nil {
		return 1
	}
	n := 1
	for _, e := range events {
		if e.Type == "turn_end" {
			n++
		}
	}
	return n
}

// chatPromptFor assembles the dossier and the conversation so far.
func (s *Service) chatPromptFor(ctx context.Context, rs *runState, projectRoot, aboutKind, aboutID string) string {
	var b strings.Builder

	b.WriteString("## The subject of this conversation\n\n")
	switch aboutKind {
	case "ducklab":
		// The harness itself. The guide rail answers WHAT is next; this chat
		// is where the person asks WHY — with the concepts in the dossier
		// and the project's live state beside them, so "what should I do
		// now?" gets the same answer the rail computes, explained.
		b.WriteString(harnessDossier)
		b.WriteString("\n### This project right now\n\n")
		if steps, err := s.ProjectNext(ctx, rs.run.ProjectID); err == nil {
			b.WriteString("The engine's own suggested next steps, in order:\n")
			for _, st := range steps {
				fmt.Fprintf(&b, "- %s — %s\n", st.Action, st.Reason)
			}
		}
		if tasks, err := s.TaskList(ctx, rs.run.ProjectID); err == nil {
			byStatus := map[string]int{}
			for _, t := range tasks {
				byStatus[t.Status]++
			}
			fmt.Fprintf(&b, "\nTasks: %d total (%v)\n", len(tasks), byStatus)
		}
		if bugs, err := s.BugList(ctx, rs.run.ProjectID, false); err == nil && len(bugs) > 0 {
			fmt.Fprintf(&b, "Bugs on the board: %d\n", len(bugs))
		}
	case "bug":
		if list, err := s.BugList(ctx, rs.run.ProjectID, false); err == nil {
			for _, bug := range list {
				if bug.ID != aboutID {
					continue
				}
				fmt.Fprintf(&b, "Bug %s [%s, %s]: %s\n\n%s\n", bug.ID, bug.Severity, bug.Status, bug.Title, bug.Body)
				if bug.TaskID != "" {
					fmt.Fprintf(&b, "\nFix task: %s", bug.TaskID)
					// The fix runs, newest first: what was actually done.
					if runs, rErr := s.RunList(ctx, RunFilter{ProjectID: rs.run.ProjectID}); rErr == nil {
						for _, r := range runs {
							if r.TaskID == bug.TaskID {
								fmt.Fprintf(&b, "\n- run %s: %s %s %s (accepted=%v, commit=%.8s)",
									r.ID, r.Stage, r.Status, r.Verdict, r.Accepted, r.CommitSHA)
							}
						}
					}
					b.WriteString("\n")
				}
			}
		}
	case "task":
		b.WriteString(s.buildTaskPrompt(ctx, rs.run.ProjectID, projectRoot, aboutID))
		if runs, rErr := s.RunList(ctx, RunFilter{ProjectID: rs.run.ProjectID}); rErr == nil {
			b.WriteString("\n### Its runs\n")
			for _, r := range runs {
				if r.TaskID == aboutID {
					fmt.Fprintf(&b, "- run %s: %s %s %s (accepted=%v)\n", r.ID, r.Stage, r.Status, r.Verdict, r.Accepted)
				}
			}
		}
	}

	// The conversation so far, replayed from the record — a chat's memory IS
	// its event log, so an engine restart loses nothing.
	events, _ := runlog.ReadEvents(rs.runDir)
	b.WriteString("\n## The conversation so far\n\n")
	for _, e := range events {
		if e.Type != "message" {
			continue
		}
		role := fmt.Sprintf("%v", e.Data["role"])
		content := fmt.Sprintf("%v", e.Data["content"])
		if role == "human" {
			b.WriteString("HUMAN: " + content + "\n\n")
		} else {
			b.WriteString("YOU: " + firstN(content, 4000) + "\n\n")
		}
	}
	b.WriteString("Reply to the human's last message.")
	return b.String()
}

// harnessDossier is what a consultant needs to explain Ducklab itself —
// embedded, because the chat must work offline and the answer must describe
// THIS binary, not whatever a model remembers about tools in general. Kept
// deliberately conceptual: the live half of the dossier (the guide's steps,
// the task and bug counts) is assembled beside it at chat time.
const harnessDossier = `You are answering questions about ducklab, the harness this chat runs in.
What follows is the authoritative description; prefer it over anything you
believe about other tools.

Ducklab is a full-cycle software development harness where multiple models
("ducklings" — local endpoints or API providers, mixed freely) hold real
roles under engineering discipline. Its laws:

- The cycle: intake (or adopt, for existing code) -> requirements -> spec ->
  plan -> tasks -> test-first builds -> review -> bugs -> release. Documents
  are drafted by model councils and APPROVED BY THE HUMAN; nothing is
  written into the approved set without a person accepting it.
- Review's light exit: a small, NON-CORE change amends the plan directly
  (the Cycle plan tab's "Amend the plan", or plan_extend) — one to three
  tasks, no redesign. Tasks no spec section covers wear a spec-debt marker;
  a one-click settle run (Cycle spec tab, or spec_settle) teaches the spec
  what was built and the markers come off on accept. Changes that alter what
  the product IS still go through a brief.
- Test-first: for a task, a model writes the failing test first; it lands
  red and is committed; the build then runs against it. The chain is one
  authorization.
- Roles are enforced by construction: a reviewer's toolbelt cannot write
  files; a tournament's judge reads anonymized candidates; the implementer's
  reasoning is never shown to the reviewer (independent second reading).
- The gate is deterministic: the project's verify command decides green or
  red. No model grades its own work. A run without an executable gate says
  UNVERIFIED and always waits for the human.
- Modes: solo (one implementer), pair (implementer + reviewer), tournament
  (contestants + blind judge), split (parallel subtasks). Documents use
  councils (drafter + critics).
- Budgets: tokens, dollars, turns and wallclock per run, attributed per
  model, visible live. Any cap can be lifted mid-run (one-way, recorded).
  No error discards work: budget death pauses the run resumable.
- Everything is on the record: events, every model call, diffs, verdicts,
  human decisions — replayable from disk.
- The guide rail (left edge) shows the engine's computed next steps for the
  project. It is the same introspection you are being given below.

Where things live in the UI: Now (the inbox: what needs the person), Work
(task board, bug board), Cycle (the documents and their stages), Records
(runs, reports, reviews, releases), the header (launch/stop the app under
development), Settings (sub-menu: your team, ducklings & providers,
budgets & limits, autopilot & autonomy, appearance, engine).

THE PATH FROM AN IDEA TO A RELEASE — walk a first-time user through these
steps IN ORDER, one at a time, checking the project state below to see
where they actually are. Never dump the whole list; meet them at their
step.

1. A place to work: a ducklab project is a git repository. Projects (in
   Settings) registers an existing one or initializes a new folder. Nothing
   runs without a repo — every change lands as a commit a person accepted.
2. A team: at least one provider (Settings -> ducklings & providers; an
   OpenRouter key via environment variable, or any local OpenAI-compatible
   endpoint) and at least one duckling on it. "Your team" seats them per
   mode and sets the default modes runs open with.
3. Say what you want to build (intake): from the guide rail or Cycle,
   describe the idea in plain words — "a fitness tracker where I log
   workouts and see progress". A council drafts the requirements; the
   person reads and approves them at the gate in Now. Approval is editing
   power: request changes in plain words instead of accepting, and the
   council revises.
4. Spec, then plan, the same way: each is drafted from the previous
   document and approved by the person. The plan lands as milestones and
   tasks on the Work board, each task carrying its acceptance criteria.
5. The verify gate — the single most important setting: the project's
   .ducklab/project.toml [verify] section must hold a real test command
   (pytest, npm test…). Green/red from that command is what "done" MEANS
   here; without it every run ends UNVERIFIED and waits for a human eye.
   The [run] command is what the header's Launch button starts.
6. Build the first task: the guide rail names the next buildable task.
   Test first writes a failing test that pins the acceptance criteria — it
   lands red, is committed, and the build chains against it. Gate green
   plus reviewer approval reaches the person as an accept decision, with
   the diff and the committed test in front of them. Accept commits.
7. Repeat: the rail always knows the next step. Runs live in Now while
   they need someone and in Records -> Runs forever after.
8. Bugs: File a bug on the bug board (or ask this chat to, explicitly).
   Triage classifies (one bug from its panel, all open from the list
   header), promote turns a report into a task, and the loop above fixes
   it. A fixed bug waits for the person to mark it verified.
9. Autopilot (optional, later): the switch in the rail drives the guide's
   own next steps unattended — test-first and build only, yolo, capped by
   tasks-per-activation and consecutive failures (Settings -> autopilot).
   Every human gate still stops it: documents, dissent, UNVERIFIED, money.
10. Release: Records -> Releases -> "Draft a release" collects everything
    accepted since the last tag and a scribe writes the notes; the person
    approves them (it is a run in Now), then Cut tags the version.

You have read tools (files, tasks, bugs, artifacts, git, and the run
history: run_list to trace a task's attempts, run_read for one run's
verdicts, gates and how it ended) — use them to answer from THIS project's
reality, not from generalities. "Why did X fail?" is answered by reading
the run record, not by guessing. You may file a bug only when the human
explicitly asks.

When the human asks "what should I do next", ground your answer in the
project state below. When they ask "why does ducklab do X", answer from
the laws above, plainly. When they are lost, find their place on the path
above and give them exactly the next step.
`
