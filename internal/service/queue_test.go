package service

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/jrullan/ducklab/internal/config"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

// queuedRun builds a queue item with a real writer — submit and start write
// run state, so a nil writer only works for tests that never enqueue.
func queuedRun(t *testing.T, id, proj string, exec func()) *queued {
	t.Helper()
	run := &runlog.Run{ID: id, ProjectID: proj, Status: "running"}
	w, err := runlog.NewWriter(t.TempDir(), run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return &queued{
		rs:   &runState{run: run, writer: w},
		ctx:  context.Background(),
		exec: func(context.Context) { exec() },
	}
}

// AC-25: with a limit of 1, a second run is QUEUED, not rejected, and starts
// automatically when the first finishes.
func TestQueueLimitsConcurrencyAndPromotesWaiting(t *testing.T) {
	q := newRunQueue(1)

	var mu sync.Mutex
	var order []string
	release := make(chan struct{})

	// Simulate the queue's start/done cycle without a real Service.
	run := func(id string, done func()) {
		mu.Lock()
		order = append(order, id)
		mu.Unlock()
		<-release
		done()
	}

	first := &queued{rs: fakeRunState("r-1", "proj")}
	second := &queued{rs: fakeRunState("r-2", "proj")}

	q.mu.Lock()
	if !q.canStart(first) {
		t.Fatal("first run could not start on an empty queue")
	}
	q.reserve(first)
	q.mu.Unlock()

	q.mu.Lock()
	canSecond := q.canStart(second)
	q.mu.Unlock()
	if canSecond {
		t.Error("second run was allowed to start while the limit was reached")
	}

	go run("r-1", func() {
		q.mu.Lock()
		q.running--
		q.perProj["proj"]--
		q.mu.Unlock()
	})
	close(release)
	time.Sleep(50 * time.Millisecond)

	q.mu.Lock()
	canNow := q.canStart(second)
	q.mu.Unlock()
	if !canNow {
		t.Error("second run still blocked after the first finished")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 1 || order[0] != "r-1" {
		t.Errorf("execution order = %v", order)
	}
}

// Document stages use the shared checkout and serialize within a project.
func TestQueueSerialisesDocumentStagesWithinAProject(t *testing.T) {
	q := newRunQueue(4)
	a := &queued{rs: fakeRunState("r-1", "proj")}
	a.rs.run.Stage = "document"
	b := &queued{rs: fakeRunState("r-2", "proj")}
	b.rs.run.Stage = "document"
	c := &queued{rs: fakeRunState("r-3", "other")}
	c.rs.run.Stage = "document"

	q.mu.Lock()
	defer q.mu.Unlock()
	q.reserve(a)

	if q.canStart(b) {
		t.Error("a second run in the same project was allowed to start")
	}
	if !q.canStart(c) {
		t.Error("a run in a different project was blocked")
	}

}

// Build and test-first are both isolated worktree users. They must be able to
// occupy the same project concurrently without requiring an explicit escape
// hatch intended for callers that knowingly share the checkout.
func TestQueueDoesNotSerializeBuildAndTestFirst(t *testing.T) {
	q := newRunQueue(4)
	build := &queued{rs: fakeRunState("r-build", "proj")}
	build.rs.run.Stage = "build"
	testFirst := &queued{rs: fakeRunState("r-test", "proj")}
	testFirst.rs.run.Stage = "test"

	q.mu.Lock()
	q.reserve(build)
	allowed := q.canStart(testFirst)
	q.mu.Unlock()
	if !allowed {
		t.Fatal("test-first was serialized behind an isolated build")
	}
}

// Build and test-first runs have private worktrees, so they do not consume the
// project checkout hold. Provider and global limits still apply.
func TestQueueAllowsConcurrentWorktreeRunsWithinAProject(t *testing.T) {
	q := newRunQueue(2)
	build := &queued{rs: fakeRunState("r-build", "proj")}
	build.rs.run.Stage = "build"
	testFirst := &queued{rs: fakeRunState("r-test", "proj")}
	testFirst.rs.run.Stage = "test"

	q.mu.Lock()
	defer q.mu.Unlock()
	q.reserve(build)
	if !q.canStart(testFirst) {
		t.Fatal("test-first run was blocked by a build in the same project")
	}
}

// A document stage retains the shared-checkout hold, but cannot block an
// isolated build that has its own worktree.
func TestQueueKeepsDocumentHoldWhileAllowingWorktreeRun(t *testing.T) {
	q := newRunQueue(2)
	document := &queued{rs: fakeRunState("r-document", "proj")}
	document.rs.run.Stage = "document"
	build := &queued{rs: fakeRunState("r-build", "proj")}
	build.rs.run.Stage = "build"

	q.mu.Lock()
	defer q.mu.Unlock()
	q.reserve(document)
	if !q.canStart(build) {
		t.Fatal("worktree build was blocked by an in-tree document stage")
	}
}

// Document stages use the shared checkout and therefore serialize even when the
// global queue has capacity. This is an execution-level assertion rather than
// only inspecting queue counters.
func TestDocumentStagesSerializeAgainstEachOther(t *testing.T) {
	q := newRunQueue(2)
	s := &Service{}
	release := make(chan struct{})
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})

	first := queuedRun(t, "r-document-1", "proj", func() {
		close(firstStarted)
		<-release
	})
	first.rs.run.Stage = "document"
	q.submit(s, first)
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first document stage did not start")
	}

	second := queuedRun(t, "r-document-2", "proj", func() { close(secondStarted) })
	second.rs.run.Stage = "document"
	q.submit(s, second)
	if second.rs.run.Status != "queued" {
		t.Fatalf("second document stage status = %q, want queued", second.rs.run.Status)
	}
	select {
	case <-secondStarted:
		t.Fatal("document stages ran concurrently in one working tree")
	case <-time.After(30 * time.Millisecond):
	}

	close(release)
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("queued document stage was not promoted")
	}
}

// A run paused at its gate has released its slot, but its uncommitted diff
// still owns the working tree — and accept commits the WHOLE tree. The queue
// must treat that project as busy, and must re-examine the line when the gate
// resolves, because no slot release will ever do it.
func TestAHeldTreeBlocksTheQueueUntilPoked(t *testing.T) {
	held := "another run holds this project's working tree"
	q := newRunQueue(2)
	q.held = func(string, string) string { return held }
	s := &Service{}

	started := make(chan struct{})
	item := queuedRun(t, "r-1", "proj", func() { close(started) })
	item.rs.run.Stage = "document"
	q.submit(s, item)

	if item.rs.run.Status != "queued" {
		t.Fatalf("status = %q, want queued — the paused run's diff is in the tree", item.rs.run.Status)
	}
	select {
	case <-started:
		t.Fatal("the run started over another run's undecided diff")
	case <-time.After(30 * time.Millisecond):
	}

	// The human decides the gate; accept/reject poke the queue.
	held = ""
	q.poke(s)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the queue never promoted the waiting run after the hold cleared")
	}
}

// The TDD chain is one unit: its build jumps the line, so no other task's
// test lands on the suite the chain has deliberately left red. Here a test
// run occupies the slot, another task queues behind it, and the chained build
// arrives last — yet runs first.
func TestAChainedBuildJumpsTheWaitingLine(t *testing.T) {
	q := newRunQueue(1)
	s := &Service{}

	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	record := func(id string) func() {
		return func() {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			wg.Done()
		}
	}

	release := make(chan struct{})
	wg.Add(3)
	occupier := queuedRun(t, "r-test-a", "proj", func() { <-release; wg.Done() })
	occupier.rs.run.Stage = "document"
	q.submit(s, occupier)

	otherTest := queuedRun(t, "r-test-b", "proj", record("other-test"))
	otherTest.rs.run.Stage = "document"
	q.submit(s, otherTest)

	chained := queuedRun(t, "r-build-a", "proj", record("chained-build"))
	chained.rs.run.Stage = "document"
	chained.chained = true
	q.submit(s, chained)

	close(release)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "chained-build" || order[1] != "other-test" {
		t.Errorf("execution order = %v, want the chained build first", order)
	}
}

// Provider capacity belongs at the run queue, rather than in a provider client:
// a local endpoint must be visible as queued before it is asked to serialize
// calls internally. The resolved roster identifies the provider the run seats.
func TestQueueQueuesLocalProviderAndRecordsWhy(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	p := s.cfg.Providers["fake"]
	p.BaseURL = "http://localhost:8081/v1"
	s.cfg.Providers["fake"] = p
	s.queue = newRunQueue(16)

	release := make(chan struct{})
	firstStarted := make(chan struct{})
	first := queuedRun(t, "r-first", "one", func() {
		close(firstStarted)
		<-release
	})
	first.parallel = true
	first.rs.run.Roster = map[string]string{"implementer": "pato-uno"}
	s.queue.submit(s, first)
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first run did not start")
	}

	secondStarted := make(chan struct{})
	second := queuedRun(t, "r-second", "two", func() { close(secondStarted) })
	second.parallel = true
	second.rs.run.Roster = map[string]string{"reviewer": "pato-uno"}
	s.queue.submit(s, second)
	if second.rs.run.Status != "queued" {
		t.Fatalf("second local-provider run = %q, want queued", second.rs.run.Status)
	}

	// QueuedReason is deliberately read by name here until the production field
	// lands with this test. It must be durable state, not an event clients have
	// to replay, and it must identify both the constrained provider and holder.
	reason := queuedReason(t, second.rs.run)
	want := "waiting for a slot on provider fake (held by r-first)"
	if reason != want {
		t.Errorf("queued reason = %q, want %q", reason, want)
	}

	close(release)
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second run was not promoted when the provider slot freed")
	}
	if second.rs.run.Status != "running" {
		t.Errorf("promoted run status = %q, want running", second.rs.run.Status)
	}
	if got := queuedReason(t, second.rs.run); got != "" {
		t.Errorf("promoted run retained queued reason %q", got)
	}
}

// queuedReason makes this test compile while the test-stage field declaration
// is applied separately. The acceptance behavior is still the public run
// record field named queued_reason, not queue internals.
func queuedReason(t *testing.T, run *runlog.Run) string {
	t.Helper()
	field := reflect.ValueOf(run).Elem().FieldByName("QueuedReason")
	if !field.IsValid() || field.Kind() != reflect.String {
		t.Fatal("run record has no QueuedReason string")
	}
	return field.String()
}

// A named cap wins over the local/hosted starter default. This uses a hosted
// URL so cap one can only be explained by max_concurrent, not locality.
func TestQueueHonorsExplicitProviderCap(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	p := s.cfg.Providers["fake"]
	p.BaseURL = "https://api.example.test/v1"
	setProviderMaxConcurrent(t, &p, 1)
	s.cfg.Providers["fake"] = p
	s.queue = newRunQueue(16)

	release := make(chan struct{})
	first := queuedRun(t, "r-cap-holder", "one", func() { <-release })
	first.parallel = true
	first.rs.run.Roster = map[string]string{"implementer": "pato-uno"}
	s.queue.submit(s, first)
	second := queuedRun(t, "r-cap-waiter", "two", func() {})
	second.parallel = true
	second.rs.run.Roster = map[string]string{"implementer": "pato-uno"}
	s.queue.submit(s, second)
	if second.rs.run.Status != "queued" {
		t.Errorf("explicit cap did not queue second hosted run; status = %q", second.rs.run.Status)
	}
	close(release)
}

func setProviderMaxConcurrent(t *testing.T, p *config.Provider, cap int) {
	t.Helper()
	field := reflect.ValueOf(p).Elem().FieldByName("MaxConcurrent")
	if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Int {
		t.Fatal("provider has no settable MaxConcurrent int")
	}
	field.SetInt(int64(cap))
}

// Hosted providers receive an eight-run starter cap when no explicit value is
// configured. All eight must start before a ninth waits.
func TestQueueHostedProviderDefaultAllowsEightRuns(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	p := s.cfg.Providers["fake"]
	p.BaseURL = "https://api.example.test/v1"
	s.cfg.Providers["fake"] = p
	s.queue = newRunQueue(16)

	release := make(chan struct{})
	started := make(chan string, 9)
	for i := 1; i <= 9; i++ {
		id := fmt.Sprintf("r-hosted-%d", i)
		item := queuedRun(t, id, fmt.Sprintf("project-%d", i), func() {
			started <- id
			<-release
		})
		item.parallel = true
		item.rs.run.Roster = map[string]string{"implementer": "pato-uno"}
		s.queue.submit(s, item)
		if i <= 8 && item.rs.run.Status != "running" {
			t.Errorf("hosted run %d status = %q, want running", i, item.rs.run.Status)
		}
		if i == 9 && item.rs.run.Status != "queued" {
			t.Errorf("ninth hosted run status = %q, want queued", item.rs.run.Status)
		}
	}
	for i := 0; i < 8; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("eight hosted runs did not start in parallel")
		}
	}
	close(release)
}

// A run reserves every provider represented by its resolved seats: a free
// hosted provider cannot make a roster start while its local seat is full.
func TestQueueBlocksRosterWhenAnyProviderIsAtCap(t *testing.T) {
	s := serviceWithDucklings(t, "local-duck", "hosted-duck")
	s.cfg.Providers = map[config.ProviderID]config.Provider{
		"local":  {Kind: config.ProviderKindOpenAI, BaseURL: "http://localhost:8081/v1"},
		"hosted": {Kind: config.ProviderKindOpenAI, BaseURL: "https://api.example.test/v1"},
	}
	s.cfg.Ducklings["local-duck"] = config.Duckling{Provider: "local", Model: "local"}
	s.cfg.Ducklings["hosted-duck"] = config.Duckling{Provider: "hosted", Model: "hosted"}
	s.queue = newRunQueue(16)

	release := make(chan struct{})
	holder := queuedRun(t, "r-local-holder", "one", func() { <-release })
	holder.parallel = true
	holder.rs.run.Roster = map[string]string{"implementer": "local-duck"}
	s.queue.submit(s, holder)

	spanning := queuedRun(t, "r-spanning", "two", func() {})
	spanning.parallel = true
	spanning.rs.run.Roster = map[string]string{
		"implementer": "local-duck", "reviewer": "hosted-duck",
	}
	s.queue.submit(s, spanning)
	if spanning.rs.run.Status != "queued" {
		t.Errorf("roster spanning a full local provider started: %q", spanning.rs.run.Status)
	}
	if got := queuedReason(t, spanning.rs.run); got != "waiting for a slot on provider local (held by r-local-holder)" {
		t.Errorf("spanning roster reason = %q", got)
	}
	close(release)
}

func TestQueueStats(t *testing.T) {
	q := newRunQueue(2)
	running, waiting, limit := q.stats()
	if running != 0 || waiting != 0 || limit != 2 {
		t.Errorf("stats = %d/%d/%d, want 0/0/2", running, waiting, limit)
	}
}

func TestQueueDefaultLimit(t *testing.T) {
	if _, _, limit := newRunQueue(0).stats(); limit != 2 {
		t.Errorf("default limit = %d, want 2", limit)
	}
}

// Test-first runs use isolated worktrees, so a paused in-tree legacy run does
// not block their independent checkout.
func TestATestFirstUsesAnIndependentWorktree(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"verify.mode": "tests", "verify.tests": "true",
	}); err != nil {
		t.Fatal(err)
	}
	paused := &runlog.Run{
		ID: "r-held", ProjectID: p.ID, TaskID: "T-001", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		StartedAt: "2026-08-06T10:00:00Z",
	}
	w, err := runlog.NewWriter(dir, paused)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	run, err := s.TestStart(context.Background(), p.ID, TestFirstRequest{TaskID: "T-002"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "running" {
		t.Errorf("status = %q, want running — test-first uses an independent worktree", run.Status)
	}
}

// A broken chain — test accepted, build failed — leaves the suite
// deliberately red. Every other task's run must wait (its test-first would
// land UNVERIFIED, its build's gate would fail on someone else's test), but
// the broken task's own runs are the cure and always pass.
func TestABrokenChainHoldsTheProjectForEveryTaskButItsOwn(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*runlog.Run{
		{ID: "r-t22", ProjectID: p.ID, TaskID: "T-022", Stage: "test",
			Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: "abc",
			StartedAt: "2026-08-06T15:14:00Z"},
		{ID: "r-b22", ProjectID: p.ID, TaskID: "T-022", Stage: "build",
			Status: "failed", Verdict: "FAILED",
			StartedAt: "2026-08-06T15:15:00Z"},
	} {
		w, err := runlog.NewWriter(dir, r)
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
	}
	s.RecoverRuns(context.Background())

	if reason := s.projectHeld(p.ID, "T-019"); reason == "" {
		t.Error("another task was allowed onto a deliberately red suite")
	} else if !strings.Contains(reason, "T-022") {
		t.Errorf("the reason does not name the broken task: %q", reason)
	}
	if reason := s.projectHeld(p.ID, "T-022"); reason != "" {
		t.Errorf("the cure was blocked by its own disease: %q", reason)
	}
}

// Draining on shutdown must hand back every waiting run so none is lost.
func TestQueueDrainReturnsWaitingRuns(t *testing.T) {
	q := newRunQueue(1)
	q.waiting = []*queued{{rs: fakeRunState("r-1", "p")}, {rs: fakeRunState("r-2", "p")}}
	drained := q.drain()
	if len(drained) != 2 {
		t.Fatalf("drained %d runs, want 2", len(drained))
	}
	if _, waiting, _ := q.stats(); waiting != 0 {
		t.Errorf("%d runs still waiting after drain", waiting)
	}
}

// T-075's relaunch sat queued forever in a project where nothing ran. Two
// holes, one story: the paused holder was aborted and nothing re-examined
// the line (the only pokes lived on gate decisions), and a run aborted WHILE
// queued stayed in the line, waiting to be promoted into "running" from its
// grave.
func TestAbortingThePausedHolderWakesTheQueue(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)

	// The holder: a paused build — projectHeld reports the tree busy.
	hold := &runlog.Run{
		ID: "r-hold", ProjectID: projectID, Stage: "document", TaskID: "T-1",
		Status: "paused", PendingKind: "error", StartedAt: "2026-08-11T00:44:00Z",
	}
	wh, err := runlog.NewWriter(entry.Path, hold)
	if err != nil {
		t.Fatal(err)
	}
	rsHold := &runState{run: hold, writer: wh, runDir: wh.RunDir(), projectPath: entry.Path}
	s.runsMu.Lock()
	s.runs["r-hold"] = rsHold
	s.runsMu.Unlock()

	// The relaunch: queues behind the held tree.
	started := make(chan struct{})
	wait := &runlog.Run{
		ID: "r-wait", ProjectID: projectID, Stage: "document", TaskID: "T-1",
		Status: "running", StartedAt: "2026-08-11T00:46:00Z",
	}
	ww, err := runlog.NewWriter(entry.Path, wait)
	if err != nil {
		t.Fatal(err)
	}
	rsWait := &runState{run: wait, writer: ww, runDir: ww.RunDir(), projectPath: entry.Path}
	s.runsMu.Lock()
	s.runs["r-wait"] = rsWait
	s.runsMu.Unlock()
	s.queue.submit(s, &queued{rs: rsWait, ctx: context.Background(), exec: func(context.Context) { close(started) }})
	if wait.Status != "queued" {
		t.Fatalf("relaunch = %s, want queued behind the paused holder", wait.Status)
	}

	// Aborting the holder frees the tree — and must wake the line.
	if err := s.RunAbort(context.Background(), "r-hold"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the holder was aborted and the queued run never started")
	}
}

// A run aborted while it waited leaves the line at the abort; and a terminal
// item that somehow remains is dropped at promotion, never resurrected —
// start() stamps whatever it is handed "running".
func TestATerminalRunNeverRisesFromTheWaitingLine(t *testing.T) {
	q := newRunQueue(1)
	first := queuedRun(t, "r-1", "proj", func() {})
	q.mu.Lock()
	q.reserve(first)
	q.mu.Unlock()

	dead := queuedRun(t, "r-dead", "proj", func() { t.Error("a dead run executed") })
	dead.rs.run.Status = "failed"
	live := queuedRun(t, "r-live", "proj", func() {})
	live.rs.run.Status = "queued"
	q.mu.Lock()
	q.waiting = append(q.waiting, dead, live)
	q.mu.Unlock()

	q.mu.Lock()
	q.running--
	q.perProj["proj"]--
	next := q.promoteLocked()
	q.mu.Unlock()
	if next == nil || next.rs.run.ID != "r-live" {
		t.Fatalf("promoted %v, want r-live", next)
	}
	if len(q.waiting) != 0 {
		t.Errorf("the dead item still waits: %d in line", len(q.waiting))
	}

	// And remove() withdraws an aborted waiter directly.
	back := queuedRun(t, "r-back", "proj", func() {})
	q.mu.Lock()
	q.waiting = append(q.waiting, back)
	q.mu.Unlock()
	if !q.remove(back.rs) {
		t.Error("remove did not find the waiting item")
	}
}
