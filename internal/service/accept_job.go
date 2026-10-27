package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/registry"
	"github.com/jrullan/ducklab/internal/runlog"
)

// B-529: an accept that rebased onto a moved default branch replayed its gate
// from a clean checkout on the HTTP request's context. The MCP client gives
// up after 30 s, the request context was cancelled, the gate's process group
// was killed, and the engine recorded the kill as "exit -1 · clean-checkout
// verification was interrupted", asked a person to decide that gate, and
// re-paused the run. ti36xpro-opencode's suite takes longer than 30 s: nine
// replays, every one exactly 30.0 s, and an operator that read them as an
// intermittent reproduction and retried seven times.
//
// A decision's long work therefore runs on a context the run owns, not the
// caller's. Abort and engine shutdown cancel it; a caller that disconnects or
// times out only stops waiting for it.

// ErrDecisionInterrupted marks decision work that abort or engine shutdown
// cancelled before it finished. Nothing was decided: it is never a failed
// gate, and it never asks a person to decide a gate that did not finish.
var ErrDecisionInterrupted = errors.New("decision interrupted")

// The causes a run-owned decision context is cancelled with, so the record
// says which one stopped it.
var (
	errCauseAborted  = errors.New("the run was aborted")
	errCauseShutdown = errors.New("the engine is shutting down")
)

// acceptJob is one accept in flight. Exactly one exists per run at a time: a
// second accept while it runs joins it instead of replaying the gate again.
type acceptJob struct {
	done chan struct{}
	// held closes once the accept holds the decision — past B-522's
	// teardown guard, so every later outcome is written as accept_settled.
	// Only then may a caller be told "in progress". A job that ends before
	// it never closes held, only done: closing both would let a caller's
	// select pick held and answer "in progress" for a settled refusal.
	held     chan struct{}
	heldOnce sync.Once
	cancel   context.CancelCauseFunc
	result   *AcceptResult
	err      error
	// attempt identifies this accept on the record (accept_started,
	// accept_settled, last_accept). Set when the decision is held; read
	// only after held or done closes.
	attempt string
}

// acceptInFlight returns the accept running on this run, or nil.
func (rs *runState) acceptInFlight() *acceptJob {
	rs.acceptMu.Lock()
	defer rs.acceptMu.Unlock()
	return rs.accepting
}

// cancelAccept cancels the accept in flight, if any, with cause, and returns
// it so the caller can wait for its work to stop touching the run.
func (rs *runState) cancelAccept(cause error) *acceptJob {
	job := rs.acceptInFlight()
	if job != nil {
		job.cancel(cause)
	}
	return job
}

// beforeAcceptStart is a seam for the start-versus-shutdown regression: it
// runs just before an accept is installed. A no-op in the engine.
var beforeAcceptStart = func(*runState) {}

// startAccept starts work as this run's accept, or returns the accept already
// running with joined=true. The work's context descends from Background, not
// from any caller: only cancelAccept stops it.
//
// The shutdown check sits in the same acceptMu section that installs the job
// (review of #181). Checked before it, an accept could pass the check, then
// PauseAllRuns set shuttingDown and swept every run without finding a job,
// and the accept installed one mid-shutdown that nothing would cancel.
// interruptAccepts takes acceptMu per run after setting the flag, so a job
// is either refused here or found and cancelled there.
func (s *Service) startAccept(rs *runState, work func(ctx context.Context, held func()) (*AcceptResult, error)) (job *acceptJob, joined bool, err error) {
	rs.acceptMu.Lock()
	defer rs.acceptMu.Unlock()
	if rs.accepting != nil {
		return rs.accepting, true, nil
	}
	if s.shuttingDown.Load() {
		return nil, false, fmt.Errorf("%w: accept not started because the engine is shutting down; accept again once it is back", ErrDecisionInterrupted)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	job = &acceptJob{done: make(chan struct{}), held: make(chan struct{}), cancel: cancel}
	// Holding the decision is also where the attempt goes on the record:
	// from here on exactly one accept_settled closes it — written by this
	// job, or by the next engine's recovery if this one dies first.
	markHeld := func() {
		job.heldOnce.Do(func() {
			job.attempt = s.beginAcceptAttempt(rs)
			close(job.held)
		})
	}
	rs.accepting = job
	go func() {
		defer func() {
			cancel(nil)
			rs.acceptMu.Lock()
			rs.accepting = nil
			rs.acceptMu.Unlock()
			close(job.done)
		}()
		job.result, job.err = work(ctx, markHeld)
		if job.result != nil {
			job.result.Attempt = job.attempt
		}
		s.settleAcceptAttempt(rs, job.attempt, job.result, job.err)
	}()
	return job, false, nil
}

// newAcceptAttempt names one accept attempt. Unique across engine restarts,
// so a settlement can never be mistaken for another attempt's.
func newAcceptAttempt() string {
	return fmt.Sprintf("acc-%d", time.Now().UnixNano())
}

// beginAcceptAttempt puts an accept attempt on the record: accept_started,
// and the run's AcceptAttempt, which stays set until its accept_settled is
// written. A next engine that finds it set settles it (settleInterruptedAccept)
// — the attempt was in flight when this engine died (review of #181: an
// accept killed with the engine wrote no settlement, and run_get then
// reported an older attempt's as last_accept). Only called once the run's
// worker no longer owns the log.
func (s *Service) beginAcceptAttempt(rs *runState) string {
	attempt := newAcceptAttempt()
	w, err := s.ensureWriter(rs)
	if err != nil {
		return attempt
	}
	rs.wmu.Lock()
	rs.run.AcceptAttempt = attempt
	_ = w.WriteState()
	rs.wmu.Unlock()
	_ = w.AppendEvent("accept_started", map[string]interface{}{"attempt": attempt})
	return attempt
}

// settleAcceptAttempt is the one terminal record of an accept: in memory for
// run_get, and — for an attempt that was put on the record — the
// accept_settled event that closes it. Every accept path ends here exactly
// once: the job, the unattended accepts, and recovery.
//
// An attempt that never held the decision (refused by B-522's teardown guard
// while the paused worker still owned the log) writes nothing: no caller was
// told "in progress" for it, the refusal reached its caller directly, and
// the door's promise is that it wrote nothing.
func (s *Service) settleAcceptAttempt(rs *runState, attempt string, result *AcceptResult, err error) {
	data := settlementData(result, err)
	if attempt != "" {
		data["attempt"] = attempt
	}
	rs.acceptMu.Lock()
	rs.lastAccept = data
	rs.acceptMu.Unlock()
	var finishing *RunFinishingError
	if attempt == "" || errors.As(err, &finishing) {
		return
	}
	// A log that refuses the write leaves the outcome in memory, and
	// AcceptAttempt set: the next engine settles it from the record.
	w, werr := s.ensureWriter(rs)
	if werr != nil {
		return
	}
	if w.AppendEvent("accept_settled", data) != nil {
		return
	}
	rs.wmu.Lock()
	if rs.run.AcceptAttempt == attempt {
		rs.run.AcceptAttempt = ""
	}
	_ = w.WriteState()
	rs.wmu.Unlock()
}

// unattendedAccept is acceptRun for the accepts no caller waits on — yolo's
// auto-accept and auto-triage — with the same record as every other accept:
// one attempt, one accept_settled. They run on the run's own worker, which
// owns the log, so they write it directly.
func (s *Service) unattendedAccept(ctx context.Context, rs *runState, entry *registry.ProjectEntry, actor string) error {
	attempt := s.beginAcceptAttempt(rs)
	err := s.acceptRun(ctx, rs, entry, "", actor)
	var result *AcceptResult
	if err == nil {
		run := rs.snapshotRun()
		result = &AcceptResult{CommitSHA: run.CommitSHA, Warning: run.Warning, Info: publicationInfo(run)}
	}
	s.settleAcceptAttempt(rs, attempt, result, err)
	return err
}

// settleInterruptedAccept runs as an engine recovers a run: an attempt still
// on the record was in flight when the last engine died (kill -9, a crash,
// or a shutdown whose bounded wait gave up), so no accept_settled closed it.
// It is closed now, from what the record shows — accepted with its commit
// when the accept landed before the engine died, interrupted otherwise —
// with the same payload, decision_outcome included, that the live path
// would have written. Mirrors settleInterruptedApprovals.
func (s *Service) settleInterruptedAccept(rs *runState) bool {
	run := rs.snapshotRun()
	if run.AcceptAttempt == "" {
		return false
	}
	events, _ := runlog.ReadEvents(rs.runDir)
	settled, decisionOpen := attemptRecord(events, run.AcceptAttempt)
	if settled {
		// The engine died between writing accept_settled and clearing the
		// attempt: it is closed already, and exactly once.
		if w, werr := s.ensureWriter(rs); werr == nil {
			rs.wmu.Lock()
			rs.run.AcceptAttempt = ""
			_ = w.WriteState()
			rs.wmu.Unlock()
		}
		return true
	}
	var result *AcceptResult
	var err error
	if run.Accepted && run.CommitSHA != "" {
		result = &AcceptResult{CommitSHA: run.CommitSHA, Warning: run.Warning, Info: publicationInfo(run)}
	} else {
		err = fmt.Errorf("%w: the engine stopped while this accept was in flight; nothing was decided — the run keeps its gate, and accept may be retried", ErrDecisionInterrupted)
	}
	// The live path closes a recorded decision with decision_outcome; so
	// does this, and only when this attempt recorded one.
	if decisionOpen {
		if w, werr := s.ensureWriter(rs); werr == nil {
			data := map[string]interface{}{"action": "accept", "outcome": "taken"}
			if err != nil {
				data["outcome"], data["error"] = "interrupted", err.Error()
			}
			_ = w.AppendEvent("decision_outcome", data)
		}
	}
	s.settleAcceptAttempt(rs, run.AcceptAttempt, result, err)
	return true
}

// attemptRecord reads what the log holds for one accept attempt: whether an
// accept_settled already closed it, and whether it recorded a decision (an
// accept with words) that no decision_outcome has closed.
func attemptRecord(events []*runlog.Event, attempt string) (settled, decisionOpen bool) {
	started := false
	for _, e := range events {
		if e == nil {
			continue
		}
		switch e.Type {
		case "accept_started":
			started = e.Data["attempt"] == attempt
		case "accept_settled":
			if e.Data["attempt"] == attempt {
				settled = true
			}
		case "decision":
			if started && e.Data["action"] == "accept" {
				decisionOpen = true
			}
		case "decision_outcome":
			if started && e.Data["action"] == "accept" {
				decisionOpen = false
			}
		}
	}
	return settled, decisionOpen
}

// lastAcceptSettlement is the newest settlement this engine produced, or nil.
func (rs *runState) lastAcceptSettlement() map[string]interface{} {
	rs.acceptMu.Lock()
	defer rs.acceptMu.Unlock()
	if rs.lastAccept == nil {
		return nil
	}
	out := make(map[string]interface{}, len(rs.lastAccept))
	for k, v := range rs.lastAccept {
		out[k] = v
	}
	return out
}

// settlementData is accept_settled's payload for an outcome.
func settlementData(result *AcceptResult, err error) map[string]interface{} {
	data := map[string]interface{}{"outcome": "accepted"}
	switch {
	case errors.Is(err, ErrDecisionInterrupted):
		data["outcome"], data["error"] = "interrupted", err.Error()
	case err != nil:
		data["outcome"], data["error"] = "refused", err.Error()
	case result != nil:
		data["commit_sha"] = result.CommitSHA
		if result.Warning != "" {
			data["warning"] = result.Warning
		}
		if result.Info != "" {
			data["info"] = result.Info
		}
	}
	return data
}

// acceptInProgressInfo is what a caller that stopped waiting is told. It
// names where the outcome arrives and why retrying is pointless, because an
// operator that read a cut-off accept as a flaky one retried it seven times.
func acceptInProgressInfo(joined bool) string {
	prefix := "accept in progress: the engine is reproducing the gate from a clean checkout and continues without this caller"
	if joined {
		prefix = "an accept of this run was already in progress, so this call started nothing and its message and options were not recorded; it is reproducing the gate from a clean checkout"
	}
	return prefix + ". Do not retry — a retry joins the same accept. The outcome arrives as the run's accept_settled event (accepted, refused or interrupted); run_get shows accept_in_progress until then, and status done with accepted=true once it lands."
}

// interruptedError reports work that ctx's cancellation stopped. Its cause —
// abort or shutdown — is named; nothing about the gate itself is claimed.
func interruptedError(ctx context.Context, what string) error {
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, context.Canceled) {
		cause = errors.New("its context was cancelled")
	}
	return fmt.Errorf("%w: %s was stopped before it finished because %v; nothing was decided — the run keeps its gate, and accept may be retried", ErrDecisionInterrupted, what, cause)
}

// interruptAccepts stops every accept in flight for a graceful shutdown. An
// accept runs on its own context, not on a run's, and the run it decides is
// usually paused at its gate — so the shutdown's sweep of running runs never
// reaches it. Each replay is killed, its accept settles "interrupted", and the
// run keeps its gate for after the restart. The wait is bounded like the
// sweep's: a stop must not hang on one slow unwind.
func (s *Service) interruptAccepts(ctx context.Context) {
	s.runsMu.RLock()
	all := make([]*runState, 0, len(s.runs))
	for _, rs := range s.runs {
		all = append(all, rs)
	}
	s.runsMu.RUnlock()
	var jobs []*acceptJob
	for _, rs := range all {
		if job := rs.cancelAccept(errCauseShutdown); job != nil {
			jobs = append(jobs, job)
		}
	}
	for _, job := range jobs {
		select {
		case <-job.done:
		case <-time.After(pauseWaitPerRun):
		case <-ctx.Done():
		}
	}
}

// lockDecisionUnlessAccepting takes the run's decision lock for a door other
// than accept, or refuses while an accept is in flight — atomically (review
// of #181): checked first and locked after, an accept starting in between
// held the lock for its whole gate replay and the door hung behind it.
//
// The check and the lock happen under acceptMu. An accept job is installed
// (accepting set) before it takes decisionMu and cleared after it releases
// it, so with accepting nil the lock's holder, if any, is a short door, and
// waiting for it under acceptMu is brief.
func lockDecisionUnlessAccepting(rs *runState, action string) (unlock func(), err error) {
	rs.acceptMu.Lock()
	defer rs.acceptMu.Unlock()
	if rs.accepting != nil {
		return nil, acceptInFlightError(rs, action)
	}
	rs.decisionMu.Lock()
	return rs.decisionMu.Unlock, nil
}

// acceptInFlightError refuses another decision while an accept is in flight.
// Reject and land used to queue on decisionMu behind the whole gate replay —
// a click that hangs for the length of a test suite and then answers about a
// run that has meanwhile changed. Abort is the door that stops an accept.
func acceptInFlightError(rs *runState, action string) error {
	return fmt.Errorf("%s refused: an accept of run %s is in progress (reproducing its gate from a clean checkout); wait for its accept_settled outcome, or abort the run to stop it", action, rs.run.ID)
}

// joinedNote marks a joined call's result: its own words decided nothing.
//
// A joined call is marked joined whatever the accept's outcome: a refused or
// interrupted accept has no result, and dropping the mark with it recorded
// an approval that joined someone else's accept as "failed" (review of #181).
func joinedNote(res *AcceptResult, msg string) *AcceptResult {
	if res == nil {
		res = &AcceptResult{}
	}
	out := *res
	out.joined = true
	note := "this call joined an accept already in progress"
	if strings.TrimSpace(msg) != "" {
		note += ", so its message was not recorded"
	}
	if out.Info != "" {
		out.Info += "; " + note
	} else {
		out.Info = note
	}
	return &out
}
