package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// heldGate is a clean-checkout gate the test holds open. The gate announces
// itself by writing to the started FIFO — which blocks until the test reads
// it — and then blocks reading the release FIFO until the test writes it.
// Every step is a rendezvous, so nothing here waits on a clock (B-529).
type heldGate struct {
	started, release, count string
}

func newHeldGate(t *testing.T, dir string) *heldGate {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the held gate rendezvous uses POSIX FIFOs")
	}
	tmp := t.TempDir()
	g := &heldGate{
		started: filepath.Join(tmp, "started"),
		release: filepath.Join(tmp, "release"),
		count:   filepath.Join(tmp, "count"),
	}
	for _, p := range []string{g.started, g.release} {
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	appendVerifyPreparation(t, dir, fmt.Sprintf(`mode = "custom"
custom = "echo replay >> %s; echo started > %s; cat %s > /dev/null"`, g.count, g.started, g.release))
	return g
}

// awaitStart returns once the gate replay is running.
func (g *heldGate) awaitStart(t *testing.T) {
	t.Helper()
	fifoStep(t, "the gate replay starting", func() error {
		_, err := os.ReadFile(g.started)
		return err
	})
}

// open lets the held replay finish green.
func (g *heldGate) open(t *testing.T) {
	t.Helper()
	fifoStep(t, "the held gate being released", func() error {
		return os.WriteFile(g.release, []byte("go\n"), 0o600)
	})
}

// fifoStep runs one blocking rendezvous under a failure bound, so a broken
// accept fails the test with its name instead of hanging the package until
// go test's own timeout.
func fifoStep(t *testing.T, what string, step func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- step() }()
	if err := awaitWithin(t, done, what); err != nil {
		t.Fatal(err)
	}
}

func (g *heldGate) replays(t *testing.T) int {
	t.Helper()
	body, err := os.ReadFile(g.count)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(body), "replay")
}

// heldAcceptRun is a worktree run paused at its gate whose accept must
// rebase: the default branch moved after the run branched, which is exactly
// the path that replayed ti36xpro's suite from a clean checkout.
func heldAcceptRun(t *testing.T, runID string) (*Service, *runlog.Run, *heldGate, string) {
	t.Helper()
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gate := newHeldGate(t, dir)
	gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, runID)
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "candidate.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", "default moved")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("move default: %s", out)
	}
	return s, run, gate, dir
}

// awaitWithin fails the test if ch does not deliver. It is a failure bound, not
// synchronization: every correct path delivers through a rendezvous above.
func awaitWithin[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(60 * time.Second):
		t.Fatalf("%s never happened", what)
		panic("unreachable")
	}
}

type acceptCall struct {
	res *AcceptResult
	err error
}

func acceptAsync(s *Service, ctx context.Context, runID, msg string) <-chan acceptCall {
	ch := make(chan acceptCall, 1)
	go func() {
		res, err := s.RunAcceptAs(ctx, runID, msg, "mcp:test")
		ch <- acceptCall{res, err}
	}()
	return ch
}

func runEvents(t *testing.T, s *Service, runID string) []*runlog.Event {
	t.Helper()
	detail, err := s.RunGet(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	return detail.Events
}

// noInterruptedGateRecorded is the ti36xpro symptom, negated: the record
// never says a killed replay exited -1 and never pages a person about it.
func noInterruptedGateRecorded(t *testing.T, events []*runlog.Event) {
	t.Helper()
	for _, e := range events {
		switch e.Type {
		case "human_needed":
			t.Errorf("human_needed recorded for a replay that never finished: %v", e.Data)
		case "gate_reproduced":
			if code, _ := e.Data["exit_code"].(float64); code == -1 {
				t.Errorf("exit -1 reproduction recorded: %v", e.Data)
			}
			if code, _ := e.Data["exit_code"].(int); code == -1 {
				t.Errorf("exit -1 reproduction recorded: %v", e.Data)
			}
		case "accept_refused":
			t.Errorf("an interrupted or outlived accept was recorded as refused: %v", e.Data)
		}
	}
}

func settledOutcomes(events []*runlog.Event) []string {
	var out []string
	for _, e := range events {
		if e.Type == "accept_settled" {
			out = append(out, fmt.Sprint(e.Data["outcome"]))
		}
	}
	return out
}

// B-529: the caller gives up while the clean-checkout replay runs — the MCP
// client's 30 s timeout in ti36xpro. The accept is answered "in progress",
// keeps running on the run's own context, and lands; a retry while it runs
// joins it instead of replaying the gate a second time.
func TestAcceptOutlivesItsCallerAndARetryJoinsIt(t *testing.T) {
	s, run, gate, dir := heldAcceptRun(t, "r-b529-outlive")

	caller, hangUp := context.WithCancel(context.Background())
	first := acceptAsync(s, caller, run.ID, "the suite passes out of band")
	gate.awaitStart(t)
	hangUp()
	got := awaitWithin(t, first, "the abandoned accept's answer")
	if got.err != nil || got.res == nil || !got.res.InProgress || got.res.CommitSHA != "" {
		t.Fatalf("abandoned accept = %+v, %v; want in_progress with no commit", got.res, got.err)
	}
	if !strings.Contains(got.res.Info, "Do not retry") {
		t.Errorf("in-progress answer does not tell the caller what to do: %q", got.res.Info)
	}
	if detail, _ := s.RunGet(context.Background(), run.ID); !detail.Run.AcceptInProgress {
		t.Error("run_get does not show the accept in progress")
	}

	// The operator retries while the replay is still held: it must join.
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	retry, err := s.RunAcceptAs(gone, run.ID, "retrying", "mcp:test")
	if err != nil || retry == nil || !retry.InProgress || !strings.Contains(retry.Info, "already in progress") {
		t.Fatalf("retry during the replay = %+v, %v; want a joined in-progress answer", retry, err)
	}
	// Bounded: a reject that queued behind the replay would wait for a gate
	// this test holds shut.
	rejected := make(chan error, 1)
	go func() { rejected <- s.RunRejectAs(context.Background(), run.ID, "", "human") }()
	if err := awaitWithin(t, rejected, "reject answering during the accept"); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("reject during the accept = %v; want a refusal naming the accept in progress", err)
	}

	// A caller that waits joins the same accept and sees it land.
	waiter := acceptAsync(s, context.Background(), run.ID, "")
	gate.open(t)
	landed := awaitWithin(t, waiter, "the joined accept's outcome")
	if landed.err != nil || landed.res == nil || landed.res.CommitSHA == "" {
		t.Fatalf("accept did not land after its caller left: %+v, %v", landed.res, landed.err)
	}
	if head := mustHead(t, vcs.New(dir)); head != landed.res.CommitSHA {
		t.Fatalf("default HEAD = %s, accepted = %s", head, landed.res.CommitSHA)
	}
	if n := gate.replays(t); n != 1 {
		t.Fatalf("gate replayed %d times; a retry during the replay must not start another", n)
	}

	events := runEvents(t, s, run.ID)
	noInterruptedGateRecorded(t, events)
	if got := settledOutcomes(events); len(got) != 1 || got[0] != "accepted" {
		t.Fatalf("accept_settled outcomes = %v, want exactly [accepted]", got)
	}
	detail, _ := s.RunGet(context.Background(), run.ID)
	if !detail.Run.Accepted || detail.Run.Status != "done" || detail.Run.AcceptInProgress {
		t.Fatalf("run after landing: accepted=%v status=%s in_progress=%v", detail.Run.Accepted, detail.Run.Status, detail.Run.AcceptInProgress)
	}
	if detail.Run.LastAccept["outcome"] != "accepted" {
		t.Fatalf("last_accept = %v", detail.Run.LastAccept)
	}
}

// Abort is one of the two things that may stop an accept: the replay is
// killed, the accept settles "interrupted" — not a failed gate — and nobody
// is asked to decide a gate that never finished.
func TestAbortDuringAcceptReplayCancelsItAsInterrupted(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-abort")

	accept := acceptAsync(s, context.Background(), run.ID, "accepting before the abort")
	gate.awaitStart(t)
	aborted := make(chan error, 1)
	go func() { aborted <- s.RunAbortAs(context.Background(), run.ID, "", "human") }()
	if err := awaitWithin(t, aborted, "abort returning"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	got := awaitWithin(t, accept, "the accept's outcome")
	if !errors.Is(got.err, ErrDecisionInterrupted) || !strings.Contains(got.err.Error(), "aborted") {
		t.Fatalf("accept after abort = %+v, %v; want ErrDecisionInterrupted naming the abort", got.res, got.err)
	}
	if strings.Contains(got.err.Error(), "failed its gate") {
		t.Fatalf("an interrupted replay was reported as a failed gate: %v", got.err)
	}

	events := runEvents(t, s, run.ID)
	noInterruptedGateRecorded(t, events)
	if got := settledOutcomes(events); len(got) != 1 || got[0] != "interrupted" {
		t.Fatalf("accept_settled outcomes = %v, want exactly [interrupted]", got)
	}
	outcome := ""
	for _, e := range events {
		if e.Type == "decision_outcome" && e.Data["action"] == "accept" {
			outcome = fmt.Sprint(e.Data["outcome"])
		}
	}
	if outcome != "interrupted" {
		t.Fatalf("the accept's decision_outcome = %q, want interrupted", outcome)
	}
	detail, _ := s.RunGet(context.Background(), run.ID)
	if detail.Run.Accepted || detail.Run.Verdict != "ABORTED" {
		t.Fatalf("aborted run: accepted=%v verdict=%s", detail.Run.Accepted, detail.Run.Verdict)
	}
}

// Engine shutdown is the other: the replay is killed, and the run keeps the
// gate it was waiting at — unchanged, unpaged — for an accept after restart.
func TestShutdownDuringAcceptReplayLeavesTheGateUntouched(t *testing.T) {
	s, run, gate, _ := heldAcceptRun(t, "r-b529-shutdown")

	accept := acceptAsync(s, context.Background(), run.ID, "")
	gate.awaitStart(t)
	if err := s.PauseAllRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := awaitWithin(t, accept, "the accept's outcome")
	if !errors.Is(got.err, ErrDecisionInterrupted) || !strings.Contains(got.err.Error(), "shutting down") {
		t.Fatalf("accept after shutdown = %+v, %v; want ErrDecisionInterrupted naming the shutdown", got.res, got.err)
	}
	events := runEvents(t, s, run.ID)
	noInterruptedGateRecorded(t, events)
	detail, _ := s.RunGet(context.Background(), run.ID)
	if detail.Run.Status != "paused" || detail.Run.PendingKind != "gate" || detail.Run.Accepted {
		t.Fatalf("run after an interrupted accept: status=%s pending=%s accepted=%v; want it still at its gate",
			detail.Run.Status, detail.Run.PendingKind, detail.Run.Accepted)
	}
}

// Review of #181 (P1): an accept answered "in progress" while the paused
// worker was still tearing down could then be refused by B-522's teardown
// guard — which must write nothing — so no accept_settled ever came and the
// caller waited forever. An accept is not answered "in progress" before it
// holds the decision: a refusal in that window reaches its caller directly,
// still writes nothing to the log, and run_get's last_accept carries it.
func TestAcceptRefusedByTeardownIsNeverAnsweredInProgress(t *testing.T) {
	s, root, runID, release := heldRun(t, "accept")
	defer release()
	previous := settleWorkerLimit
	settleWorkerLimit = 50 * time.Millisecond
	defer func() { settleWorkerLimit = previous }()
	if got := mustRun(t, s, runID); got.Status != "paused" || got.PendingKind != "gate" {
		t.Fatalf("setup: held worker published %s/%s, want paused/gate", got.Status, got.PendingKind)
	}
	dir := runlog.RunDirFor(root, runID)
	stateBefore, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	eventsBefore, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))

	gone, cancel := context.WithCancel(context.Background())
	cancel()
	got := awaitWithin(t, acceptAsync(s, gone, runID, ""), "the accept's answer")
	var finishing *RunFinishingError
	if !errors.As(got.err, &finishing) {
		t.Fatalf("accept while the worker tears down = %+v, %v; want the RunFinishingError itself, never in_progress", got.res, got.err)
	}
	stateAfter, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	eventsAfter, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if string(stateBefore) != string(stateAfter) || string(eventsBefore) != string(eventsAfter) {
		t.Fatal("the refused accept wrote to a log the worker still owns")
	}
	detail, err := s.RunGet(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.AcceptInProgress || detail.Run.LastAccept["outcome"] != "refused" {
		t.Fatalf("run_get after the refusal: in_progress=%v last_accept=%v; want the refusal observable", detail.Run.AcceptInProgress, detail.Run.LastAccept)
	}
}

// Review of #181 (P2): the shutdown check ran before the job was installed.
// An accept could pass it, PauseAllRuns then set the flag and swept every
// run without finding a job, and the accept installed one mid-shutdown that
// nothing would cancel. The seam runs a whole shutdown in exactly that gap.
func TestAcceptStartingDuringShutdownIsRefused(t *testing.T) {
	s, run, _, _ := heldAcceptRun(t, "r-b529-start-vs-shutdown")
	beforeAcceptStart = func(*runState) { _ = s.PauseAllRuns(context.Background()) }
	t.Cleanup(func() { beforeAcceptStart = func(*runState) {} })

	got := awaitWithin(t, acceptAsync(s, context.Background(), run.ID, ""), "the accept's answer")
	if !errors.Is(got.err, ErrDecisionInterrupted) || !strings.Contains(got.err.Error(), "shutting down") {
		t.Fatalf("accept racing shutdown = %+v, %v; want it refused as interrupted by the shutdown", got.res, got.err)
	}
	if detail, _ := s.RunGet(context.Background(), run.ID); detail.Run.AcceptInProgress || detail.Run.Status != "paused" {
		t.Fatalf("after the refused start: in_progress=%v status=%s", detail.Run.AcceptInProgress, detail.Run.Status)
	}
}

// Review of #181 (4394889562): last_accept is how a client that missed the
// live accept_settled recovers the outcome — including its publication
// consequences (warning, info). It must say the same thing from memory and,
// after an engine restart, from the log.
func TestLastAcceptIsTheSameSettlementFromMemoryAndFromTheLog(t *testing.T) {
	s, run, gate, dir := heldAcceptRun(t, "r-b529-last-accept")
	// A push that fails: the durable consequence a client must not lose.
	path := filepath.Join(dir, ".ducklab", "project.toml")
	cfg, err := config.LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Remote.Name, cfg.Remote.OnAccept = "origin", "push"
	if err := config.SaveProject(path, cfg); err != nil {
		t.Fatal(err)
	}
	remote := exec.Command("git", "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	remote.Dir = dir
	if out, err := remote.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %s", out)
	}
	accepting := acceptAsync(s, context.Background(), run.ID, "")
	gate.awaitStart(t)
	gate.open(t)
	got := awaitWithin(t, accepting, "the accept")
	if got.err != nil || got.res.CommitSHA == "" {
		t.Fatalf("accept: %+v, %v", got.res, got.err)
	}
	if !strings.Contains(got.res.Warning, "push failed") {
		t.Fatalf("fixture: accept warning = %q, want a failed push", got.res.Warning)
	}
	live, _ := s.RunGet(context.Background(), run.ID)
	fromMemory := live.Run.LastAccept
	want := map[string]interface{}{"outcome": "accepted", "commit_sha": got.res.CommitSHA}
	if got.res.Warning != "" {
		want["warning"] = got.res.Warning
	}
	if got.res.Info != "" {
		want["info"] = got.res.Info
	}
	if fmt.Sprint(fromMemory) != fmt.Sprint(want) {
		t.Fatalf("last_accept = %v, want the accept's whole result %v", fromMemory, want)
	}
	// An engine restart keeps the log and loses memory.
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	rs.acceptMu.Lock()
	rs.lastAccept = nil
	rs.acceptMu.Unlock()
	restarted, _ := s.RunGet(context.Background(), run.ID)
	if fmt.Sprint(restarted.Run.LastAccept) != fmt.Sprint(want) {
		t.Fatalf("last_accept from the log = %v, want %v", restarted.Run.LastAccept, want)
	}
}
