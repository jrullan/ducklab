package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

// B-490: the chained build learns which tests its test-first run wrote.
func TestChainOracleTestsAreTheRedCommitsTests(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, dir := projectWithDocs(t, s, nil)
	g := gitProject(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"tests/parser.test.mjs": "assert\n", "notes.md": "n\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddAll(); err != nil {
		t.Fatal(err)
	}
	sha, err := g.Commit("ducklab: T-014 red test")
	if err != nil {
		t.Fatal(err)
	}
	got := chainOracleTests(dir, sha)
	if !slices.Equal(got, []string{"tests/parser.test.mjs"}) {
		t.Errorf("oracle tests = %v, want the test file only", got)
	}
}

func TestTheOracleBriefNamesTheTestsAndTheDisputeRoute(t *testing.T) {
	if oracleBrief(nil) != "" {
		t.Error("a build without an oracle got an oracle brief")
	}
	brief := oracleBrief([]string{"tests/parser.test.mjs"})
	for _, want := range []string{"`tests/parser.test.mjs`", "You may not edit them", "oracle_dispute", "the arithmetic"} {
		if !strings.Contains(brief, want) {
			t.Errorf("oracle brief lacks %q:\n%s", want, brief)
		}
	}
}

// B-490: an oracle dispute is the person's decision. Under yolo the advisor's
// draft is shown, never submitted; an ordinary question still is.
func TestYoloNeverAutoAnswersAnOracleDispute(t *testing.T) {
	for _, tc := range []struct {
		id         string
		autoAnswer bool
	}{
		{tools.OracleQuestionPrefix + "abc", false},
		{"q-ordinary", true},
	} {
		s := serviceWithDucklings(t, "pato-dos")
		p := &advisorTestProvider{replies: []string{tools.OracleCorrectAnswer}}
		s.ducklings.RegisterProvider(p)
		dir := t.TempDir()
		run := &runlog.Run{
			ID: "r-oracle-yolo", ProjectID: "p", TaskID: "T-014", Stage: "build", Status: "paused", PendingKind: "question",
			PendingData: map[string]interface{}{"question_id": tc.id}, Autonomy: "yolo",
			Roster: map[string]string{"advisor": "pato-dos"},
		}
		w, err := runlog.NewWriter(dir, run)
		if err != nil {
			t.Fatal(err)
		}
		rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
		s.runsMu.Lock()
		s.runs[run.ID] = rs
		s.runsMu.Unlock()
		s.adviseQuestion(rs, &tools.PendingQuestion{
			ID: tc.id, Question: "Is the test wrong?",
			Options: []string{tools.OracleCorrectAnswer, tools.OracleKeepAnswer},
		})
		taken := false
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			rs.wmu.Lock()
			advice, _ := rs.run.PendingData["advice"].(string)
			rs.wmu.Unlock()
			events, _ := runlog.ReadEvents(w.RunDir())
			for _, e := range events {
				taken = taken || e.Type == "advice_taken"
			}
			if advice != "" || taken {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		w.Close()
		if taken != tc.autoAnswer {
			t.Errorf("question %s: advice taken = %v, want %v", tc.id, taken, tc.autoAnswer)
		}
	}
}

// recordingProvider answers every call with a short reply and keeps the
// requests, so a test can read the prompt a run actually sent.
type recordingProvider struct {
	mu       sync.Mutex
	requests []provider.ChatRequest
}

func (p *recordingProvider) ID() string                               { return "fake" }
func (p *recordingProvider) Models(context.Context) ([]string, error) { return nil, nil }
func (p *recordingProvider) ChatStream(ctx context.Context, req provider.ChatRequest, _ chan<- provider.Delta) (provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}
func (p *recordingProvider) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: "Done."}, FinishReason: provider.FinishStop}}}, nil
}

// End to end through the real chain: the red test commit's tests become the
// build's oracle — on its record, in its execution context, and in the
// prompt its implementer receives.
func TestAChainedBuildIsBriefedOnItsOracle(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	rec := &recordingProvider{}
	s.ducklings.RegisterProvider(rec)
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true, GitName: "Ada", GitEmail: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindPlan)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan),
		[]byte("## M-001 — Core\n\n### T-003 — Do a thing\n\nDo it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// What the test-first run wrote, before its accept commits it.
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "thing.test.mjs"), []byte("assert(false)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-tf-oracle", ProjectID: p.ID, TaskID: "T-003", Stage: "test",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate", StartedAt: "2026-10-04T12:00:00Z",
		ChainBuild: map[string]interface{}{"task_id": "T-003", "mode": "solo"},
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())
	s.runsMu.RLock()
	rs := s.runs["r-tf-oracle"]
	s.runsMu.RUnlock()
	if _, err := s.ensureWriter(rs); err != nil {
		t.Fatal(err)
	}
	s.chainBuild(context.Background(), rs, TestFirstRequest{TaskID: "T-003", ThenBuild: true, Build: RunRequest{Mode: "solo"}})

	var build *runlog.Run
	runs, _ := s.RunList(context.Background(), RunFilter{ProjectID: p.ID})
	for _, r := range runs {
		if r.TaskID == "T-003" && r.Stage == "build" {
			build = r
		}
	}
	if build == nil {
		t.Fatal("no chained build started")
	}
	cleanupStartedRun(t, s, build.ID)
	if !slices.Equal(build.OracleTests, []string{"tests/thing.test.mjs"}) {
		t.Errorf("build oracle tests = %v", build.OracleTests)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		n := len(rec.requests)
		rec.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	briefed := false
	for _, req := range rec.requests {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "## The tests that decide this task") && strings.Contains(m.Content, "`tests/thing.test.mjs`") {
				briefed = true
			}
		}
	}
	if !briefed {
		t.Errorf("the chained build's implementer was not briefed on its oracle (%d requests)", len(rec.requests))
	}
}

// B-490 (c): the test writer and its reviewer are both told to derive and
// recompute every expected value; a test-first run once approved
// "2^-9 = 0.015625".
func TestTheTestFirstPromptDemandsCheckedDerivations(t *testing.T) {
	prompt := testFirstPrompt("Do a thing.", "npm test --silent")
	for _, want := range []string{"Derive every expected value", "write the derivation beside it", "A reviewer of this test recomputes each derivation"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("test-first prompt lacks %q", want)
		}
	}
}

// Review of #147: an oracle dispute is a person's decision at the answer
// boundary itself — an MCP operator (or any non-human actor) is refused, a
// person is not, and an operator's ordinary answer is attributed to it.
func TestOnlyAPersonAnswersAnOracleDispute(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	paused := func(id, qid string) *runlog.Run {
		dir := t.TempDir()
		run := &runlog.Run{ID: id, ProjectID: "p", TaskID: "T-014", Stage: "build", Status: "paused", PendingKind: "question",
			PendingData: map[string]interface{}{"question_id": qid, "question": "Is the test wrong?"}}
		w, err := runlog.NewWriter(dir, run)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.Close() })
		s.runsMu.Lock()
		s.runs[id] = &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
		s.runsMu.Unlock()
		return run
	}
	oracleQ := tools.OracleQuestionPrefix + "tests/parser.test.mjs:abc"

	run := paused("r-oracle-mcp", oracleQ)
	err := s.RunAnswerAs(context.Background(), run.ID, "", tools.OracleCorrectAnswer, "mcp:elena")
	if err == nil || !strings.Contains(err.Error(), "a person must answer it") {
		t.Fatalf("an operator answered an oracle dispute: %v", err)
	}
	if run.PendingKind != "question" {
		t.Fatalf("the refused answer changed the run: pending %q", run.PendingKind)
	}
	s.runsMu.RLock()
	given := s.runs[run.ID].answers()
	s.runsMu.RUnlock()
	if _, ok := given[oracleQ]; ok {
		t.Fatal("the refused answer was stored and would unlock the oracle on replay")
	}

	ordinary := paused("r-ordinary-mcp", "q-ordinary")
	if err := s.RunAnswerAs(context.Background(), ordinary.ID, "", "yes", "mcp:elena"); err != nil && !strings.Contains(err.Error(), "resume") {
		t.Logf("ordinary answer: %v", err)
	}
	events, _ := runlog.ReadEvents(s.runs[ordinary.ID].runDir)
	attributed := false
	for _, e := range events {
		if e.Type == "human" && e.Data["actor"] == "mcp:elena" {
			attributed = true
		}
	}
	if !attributed {
		t.Error("an operator's answer was recorded without its actor")
	}

	person := paused("r-oracle-person", oracleQ)
	if err := s.RunAnswer(context.Background(), person.ID, "", tools.OracleCorrectAnswer); err != nil && strings.Contains(err.Error(), "a person must answer it") {
		t.Fatalf("a person's answer to an oracle dispute was refused: %v", err)
	}
}

// Review of #147: a lane-widening answer from a non-human decider is refused
// before anything is committed — the offer stays, no lane is widened.
func TestANonHumanLaneWideningAnswerChangesNothing(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	run := &runlog.Run{ID: "r-lane-mcp", ProjectID: "p", TaskID: "T-014", Stage: "build", Status: "paused", PendingKind: "question",
		PendingData: map[string]interface{}{"question_id": "oracle-tests/parser.test.mjs:abc", "question": "Is the test wrong?",
			"lane_widening": []string{"tests/parser.test.mjs"}}}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	s.runsMu.Lock()
	s.runs[run.ID] = &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: dir}
	s.runsMu.Unlock()
	err = s.RunAnswerWithLaneAs(context.Background(), run.ID, "", tools.OracleCorrectAnswer, []string{"tests/parser.test.mjs"}, "mcp:elena")
	if err == nil || !strings.Contains(err.Error(), "a person must approve it") {
		t.Fatalf("an operator widened a lane by answering: %v", err)
	}
	if _, ok := run.PendingData["lane_widening"]; !ok || run.PendingKind != "question" {
		t.Fatalf("the refused answer changed the run: %+v", run.PendingData)
	}
	events, _ := runlog.ReadEvents(w.RunDir())
	for _, e := range events {
		if e.Type == "lane_widened" || e.Type == "human" {
			t.Fatalf("the refused answer was recorded: %s", e.Type)
		}
	}
}
