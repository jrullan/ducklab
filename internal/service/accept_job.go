package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
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
	markHeld := func() { job.heldOnce.Do(func() { close(job.held) }) }
	rs.accepting = job
	go func() {
		defer func() {
			cancel(nil)
			rs.acceptMu.Lock()
			rs.accepting = nil
			rs.lastAccept = settlementData(job.result, job.err)
			rs.acceptMu.Unlock()
			close(job.done)
		}()
		job.result, job.err = work(ctx, markHeld)
		s.recordAcceptSettled(rs, job.result, job.err)
	}()
	return job, false, nil
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

// recordAcceptSettled closes the accept on the run's stream. A caller that
// stopped waiting was told the accept was in progress; this is where the
// outcome it was promised arrives, for every accept, whoever is watching.
func (s *Service) recordAcceptSettled(rs *runState, result *AcceptResult, err error) {
	// Refused because the paused worker was still tearing down: that worker
	// is about to close the log, and the door's promise is that it wrote
	// nothing (B-522). No caller was told "in progress" for it — that waits
	// for the decision to be held — so the refusal reached its caller
	// directly, and run_get's last_accept carries it from memory.
	var finishing *RunFinishingError
	if errors.As(err, &finishing) {
		return
	}
	// A log that refuses the write leaves the outcome in memory only
	// (rs.lastAccept, set by the job): run_get still shows it.
	w, werr := s.ensureWriter(rs)
	if werr != nil {
		return
	}
	_ = w.AppendEvent("accept_settled", settlementData(result, err))
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

// acceptInFlightRefusal refuses another decision while an accept is in flight.
// Reject and land used to queue on decisionMu behind the whole gate replay —
// a click that hangs for the length of a test suite and then answers about a
// run that has meanwhile changed. Abort is the door that stops an accept.
func acceptInFlightRefusal(rs *runState, action string) error {
	if rs.acceptInFlight() == nil {
		return nil
	}
	return fmt.Errorf("%s refused: an accept of run %s is in progress (reproducing its gate from a clean checkout); wait for its accept_settled outcome, or abort the run to stop it", action, rs.run.ID)
}

// joinedNote marks a joined call's result: its own words decided nothing.
func joinedNote(res *AcceptResult, msg string) *AcceptResult {
	if res == nil {
		return nil
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
