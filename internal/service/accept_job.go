package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	done   chan struct{}
	cancel context.CancelCauseFunc
	result *AcceptResult
	err    error
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

// startAccept starts work as this run's accept, or returns the accept already
// running with joined=true. The work's context descends from Background, not
// from any caller: only cancelAccept stops it.
func (s *Service) startAccept(rs *runState, work func(context.Context) (*AcceptResult, error)) (job *acceptJob, joined bool) {
	rs.acceptMu.Lock()
	defer rs.acceptMu.Unlock()
	if rs.accepting != nil {
		return rs.accepting, true
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	job = &acceptJob{done: make(chan struct{}), cancel: cancel}
	rs.accepting = job
	go func() {
		defer func() {
			cancel(nil)
			rs.acceptMu.Lock()
			rs.accepting = nil
			rs.acceptMu.Unlock()
			close(job.done)
		}()
		job.result, job.err = work(ctx)
		s.recordAcceptSettled(rs, job.result, job.err)
	}()
	return job, false
}

// recordAcceptSettled closes the accept on the run's stream. A caller that
// stopped waiting was told the accept was in progress; this is where the
// outcome it was promised arrives, for every accept, whoever is watching.
func (s *Service) recordAcceptSettled(rs *runState, result *AcceptResult, err error) {
	// Refused because the paused worker was still tearing down: that worker
	// is about to close the log, and the door's promise is that it wrote
	// nothing (B-522). The run is unchanged and accept_in_progress has gone
	// false; a retry decides.
	var finishing *RunFinishingError
	if errors.As(err, &finishing) {
		return
	}
	w, werr := s.ensureWriter(rs)
	if werr != nil {
		return
	}
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
	_ = w.AppendEvent("accept_settled", data)
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
