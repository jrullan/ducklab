package service

import (
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

// escalationShape is what an escalation card may truthfully say and offer for
// one run: whose seat the evidence is about, whether the run has a task whose
// brief could be the cause, and whether it has a safe point to pause at.
//
// B-522: a triage run (calculator r-20261026-171819-zzgs) crossed its history
// threshold and the card said "The run paused", diagnosed "task brief
// quality", offered "Open task body" and a permanently disabled "Relaunch with
// stronger seat". The run had no task, no implementer, and no pause: only the
// build loop and document stages ever landed a requested pause, so the triage
// walked straight into its next bug. Every escalation emitter now asks this
// one function instead of assuming a build run.
type escalationShape struct {
	// Role is the seat whose evidence this is; Seat is who sat in it.
	Role string
	Seat string
	// TaskRun says a task brief exists, so its quality is a real diagnosis
	// and editing it a real action.
	TaskRun bool
	// Pausable says the run lands a requested pause at a safe point and can
	// resume from there. Otherwise the pause is deferred, and DeferDetail is
	// the sentence that says what happens instead.
	Pausable    bool
	PauseDetail string
	DeferDetail string
}

func escalationShapeFor(run *runlog.Run) escalationShape {
	if run == nil {
		return escalationShape{}
	}
	sh := escalationShape{Role: escalationRole(run)}
	sh.Seat = run.Roster[sh.Role]
	switch run.Stage {
	case "build":
		// The task loop works in a durable checkout and replays an
		// interrupted turn from its checkpoint; the turn boundary is its safe
		// point (modes.go calls pauseAtSafePoint at turn_end).
		sh.TaskRun = run.TaskID != ""
		sh.Pausable = true
		sh.PauseDetail = "pausing when the current turn ends"
	case "test":
		// Test-first has no turn checkpoint: a resume restarts its script
		// from the first turn, so a pause after the writer's turn would buy
		// the person a decision at the price of redoing the work it paused
		// to protect — and after the last turn it would land between the
		// writer and the gate, with nothing left to protect. It continues
		// to its gate, which is a decision point anyway.
		sh.TaskRun = run.TaskID != ""
		sh.DeferDetail = "the test-first run continues to its gate; it has no checkpoint to resume a paused turn from"
	case "triage":
		// Each bug is its own turn and its own proposal; nothing spans two.
		// The end of a bug's turn is a safe point, and resume continues with
		// the bugs not yet triaged.
		sh.Pausable = true
		sh.PauseDetail = "pausing when the current bug's turn ends; resume continues with the bugs not yet triaged"
	case "intake", "spec", "plan":
		// Section-wise document updates are transactions held by the stage
		// orchestrator: triage selects sections, then independent councils
		// replace them in an in-memory candidate. A turn checkpoint can
		// preserve one council conversation, but not that outer replacement
		// ledger, so a pause resumed into the wrong pass and reported
		// "architect changed no sections" after approved work.
		sh.DeferDetail = "document transaction continues to its human gate"
	case "release":
		// One scribe draft, held in memory until the gate; a release has no
		// persisted request to re-enter from.
		sh.DeferDetail = "the release draft continues to its gate; it has no safe point to pause at"
	case "review":
		sh.DeferDetail = "the review continues to its verdict; it has no safe point to pause at"
	case "chat":
		sh.DeferDetail = "the conversation continues; it waits for you after every reply anyway"
	default:
		sh.DeferDetail = "this run has no safe point to pause at; it continues"
	}
	return sh
}

// escalationRole names the seat a run's evidence is about.
func escalationRole(run *runlog.Run) string {
	switch run.Stage {
	case "triage":
		return string(config.RoleTriager)
	case "intake", "spec", "plan":
		return string(config.RoleArchitect)
	case "release", "pr_body":
		return string(config.RoleScribe)
	case "review":
		return string(config.RoleReviewer)
	case "chat":
		return string(config.RoleConsultant)
	}
	return string(config.RoleImplementer)
}

// actions lists what the card may offer. Only a task run has a brief to
// improve; only task runs and triage have a relaunch door that re-runs the same
// work with a different seat. Every run that has not ended can be aborted —
// the task run's abort lives on its own run controls already.
func (sh escalationShape) actions() []string {
	switch {
	case sh.TaskRun:
		return []string{"relaunch_with_stronger_seat", "improve_task_body", "continue_as-is"}
	case sh.Role == string(config.RoleTriager):
		return []string{"relaunch_with_another_seat", "continue_as-is", "abort"}
	}
	return []string{"continue_as-is", "abort"}
}

// diagnoses keeps the seat diagnosis and adds the brief diagnosis only where
// there is a brief.
func (sh escalationShape) diagnoses(seat interface{}, brief string) map[string]interface{} {
	out := map[string]interface{}{"seat_at_capacity": seat}
	if sh.TaskRun && brief != "" {
		out["task_brief_quality"] = brief
	}
	return out
}

// stamp writes the shape onto an escalation event so every reader — the
// desktop card, MCP run_get, a person reading events.jsonl — learns the role
// and the seat without guessing them from the stage.
func (sh escalationShape) stamp(data map[string]interface{}) {
	data["role"] = sh.Role
	if sh.Seat != "" {
		data["seat"] = sh.Seat
	}
	data["actions"] = sh.actions()
}

// escalationView is the truthful state of a run's latest escalation card,
// derived on read for run_get: whether the run actually paused, will pause at
// its safe point, or continues. The desktop computes the same from the event
// stream it already holds.
func escalationView(run *runlog.Run, events []*runlog.Event) map[string]interface{} {
	var latest *runlog.Event
	for _, e := range events {
		if e != nil && e.Type == "escalation_suggestion" && (latest == nil || e.Seq > latest.Seq) {
			latest = e
		}
	}
	if latest == nil || run == nil {
		return nil
	}
	view := map[string]interface{}{"seq": latest.Seq}
	for _, k := range []string{"point", "role", "seat", "actions", "candidate", "detail"} {
		if v, ok := latest.Data[k]; ok {
			view[k] = v
		}
	}
	requested, deferred := "", ""
	for _, e := range events {
		if e == nil || e.Seq < latest.Seq {
			continue
		}
		detail, _ := e.Data["detail"].(string)
		switch e.Type {
		case "pause_requested":
			requested = detail
		case "pause_deferred":
			deferred = detail
		}
	}
	switch {
	case run.Status == "paused" && run.PendingKind == "history_duration":
		view["state"] = "paused"
		view["state_detail"] = "the run paused at a safe point; resume continues it"
	case deferred != "" && (run.Status == "running" || run.Status == "queued"):
		view["state"] = "continuing"
		view["state_detail"] = deferred
	case requested != "" && (run.Status == "running" || run.Status == "queued"):
		view["state"] = "pausing"
		view["state_detail"] = requested
	case run.Status == "running" || run.Status == "queued":
		view["state"] = "continuing"
		view["state_detail"] = "the run is still working; this is advice"
	case run.Status == "paused":
		view["state"] = "paused"
		view["state_detail"] = "the run is paused (" + run.PendingKind + ")"
	default:
		view["state"] = "ended"
		view["state_detail"] = "the run has ended (" + run.Status + ")"
	}
	return view
}
