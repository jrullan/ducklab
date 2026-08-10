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
// The consultant reads and advises; it changes nothing. Its toolbelt has no
// write, and its closing duty is to suggest actions from the person's own
// menu — reopen the bug, file a new one, relaunch the task with a note —
// which the person executes with the buttons that already exist (I2).

// chatToolbelt is read-only investigation: code, history, records.
const chatToolbelt = "fs_read,fs_search,fs_list,git_log,git_diff,task_read,bug_read,artifact_read"

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
		Note: fmt.Sprintf("chat about %s %s", req.AboutKind, req.AboutID),
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
			MaxUSD:        projCfg.Budget.MaxUSD,
			MaxTokens:     int64(s.cfg.Defaults.Budget.MaxTokens),
			MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS,
			MaxTurns:      s.cfg.Defaults.Budget.MaxTurns,
		}
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
		writer: s.llmWriter(rs, tracker),
		loops:  map[config.DucklingID]*agent.Loop{},
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
