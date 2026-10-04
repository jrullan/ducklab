package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// The words the person knew when TI-36X T-014's chained build stopped on an
// error (B-490, B-491), and had no way to send (B-493).
const ti36xResumeNote = "The test-first expectation 0.015625 is wrong: 2^-9 is 0.001953125. " +
	"Write the path without a leading slash."

// pausedRunWithNote records a paused run the way the engine leaves one and
// loads it the way a restarted engine would.
func pausedRunWithNote(t *testing.T, s *Service, stage, kind string) (projectID string, rec *recordingProvider) {
	t.Helper()
	rec = &recordingProvider{}
	s.ducklings.RegisterProvider(rec)
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "TI-36X", GitInit: true, GitName: "Ada", GitEmail: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"verify.mode": "tests", "verify.tests": "true",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindPlan)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan),
		[]byte("## M-001 — Core\n\n### T-014 — Scientific notation\n\nRender 2^-9.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-20261003-145618-bbdk", ProjectID: p.ID, TaskID: "T-014", Stage: stage, Mode: "solo",
		Roster: map[string]string{"implementer": "pato-uno"},
		Status: "paused", PendingKind: kind, Failure: "the run stopped",
		Note:      "launch note: keep the display code untouched",
		StartedAt: "2026-10-03T14:56:18Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	return p.ID, rec
}

func waitResumed(t *testing.T, s *Service, id string) *runState {
	t.Helper()
	s.runsMu.RLock()
	rs := s.runs[id]
	s.runsMu.RUnlock()
	select {
	case <-rs.done:
	case <-time.After(20 * time.Second):
		t.Fatal("the resumed run never finished")
	}
	return rs
}

// noteSections are the "Note from the human" sections every recorded prompt
// carried.
func noteSections(rec *recordingProvider) []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var out []string
	for _, req := range rec.requests {
		for _, m := range req.Messages {
			if i := strings.Index(m.Content, "## Note from the human"); i >= 0 {
				out = append(out, m.Content[i:])
			}
		}
	}
	return out
}

// B-493, one cell per pause that offers Resume, for both stages that resume
// from the record: the note the person types rides the run's next prompt
// BESIDE the launch note, lands on the record with the pause it answered,
// and the resume checkpoint says what was said and by whom.
func TestAResumeNoteRidesTheNextPromptBesideTheLaunchNote(t *testing.T) {
	for _, stage := range []string{"build", "test"} {
		for _, kind := range []string{"error", "budget", "provider", "engine_restart", "engine_shutdown", "history_duration"} {
			t.Run(stage+"/"+kind, func(t *testing.T) {
				s := serviceWithDucklings(t, "pato-uno")
				_, rec := pausedRunWithNote(t, s, stage, kind)
				const id = "r-20261003-145618-bbdk"
				got, err := s.RunResumeWithNote(context.Background(), id, "  "+ti36xResumeNote+"\n", "")
				if err != nil {
					t.Fatalf("resume with a note was refused: %v", err)
				}
				if got.Status != "running" && got.Status != "queued" {
					t.Errorf("status after resume = %s", got.Status)
				}
				rs := waitResumed(t, s, id)

				sections := noteSections(rec)
				if len(sections) == 0 {
					t.Fatalf("no prompt carried a note from the human (%d requests)", len(rec.requests))
				}
				first := sections[0]
				launch := strings.Index(first, "launch note: keep the display code untouched")
				added := strings.Index(first, ti36xResumeNote)
				if launch < 0 || added < 0 || launch > added {
					t.Errorf("the next prompt must carry the launch note, then the resume note:\n%s", first)
				}
				if !strings.Contains(first, "paused ("+kind+")") {
					t.Errorf("the resume note does not say which pause it answered:\n%s", first)
				}

				record := rs.snapshotRun()
				if record.Note != "launch note: keep the display code untouched" {
					t.Errorf("the launch note on the record changed: %q", record.Note)
				}
				if len(record.ResumeNotes) != 1 || record.ResumeNotes[0].Note != ti36xResumeNote ||
					record.ResumeNotes[0].PendingKind != kind || record.ResumeNotes[0].Actor != "human" {
					t.Errorf("resume notes on the record = %+v", record.ResumeNotes)
				}
				events, err := runlog.ReadEvents(rs.runDir)
				if err != nil {
					t.Fatal(err)
				}
				said := false
				for _, e := range events {
					if e.Type == "checkpoint" && e.Data["reason"] == "resume" && e.Data["note"] == ti36xResumeNote && e.Data["actor"] == "human" {
						said = true
					}
				}
				if !said {
					t.Error("the resume checkpoint does not record what the person said")
				}
			})
		}
	}
}

// No note resumes exactly as before: no record entry, no event field, and the
// launch note still rides.
func TestAResumeWithoutANoteKeepsTheLaunchNote(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, rec := pausedRunWithNote(t, s, "build", "error")
	if _, err := s.RunResume(context.Background(), "r-20261003-145618-bbdk"); err != nil {
		t.Fatal(err)
	}
	rs := waitResumed(t, s, "r-20261003-145618-bbdk")
	if n := len(rs.snapshotRun().ResumeNotes); n != 0 {
		t.Errorf("a resume without a note recorded %d notes", n)
	}
	sections := noteSections(rec)
	if len(sections) == 0 || !strings.Contains(sections[0], "launch note") || strings.Contains(sections[0], "resumed") {
		t.Errorf("the launch note must ride alone: %q", sections)
	}
	events, _ := runlog.ReadEvents(rs.runDir)
	for _, e := range events {
		if e.Type == "checkpoint" && e.Data["note"] != nil {
			t.Errorf("a note-less resume wrote a note: %v", e.Data)
		}
	}
}

// A second resume keeps every earlier note: nothing the person said is
// dropped by saying something more.
func TestEveryResumeNoteRidesInOrder(t *testing.T) {
	req := resumeRequest(&runlog.Run{
		TaskID: "T-014", Mode: "solo", Note: "launch",
		ResumeNotes: []runlog.ResumeNote{
			{Note: "first fix", PendingKind: "error"},
			{Note: "second fix", PendingKind: "provider"},
		},
	})
	launch, first, second := strings.Index(req.Note, "launch"), strings.Index(req.Note, "first fix"), strings.Index(req.Note, "second fix")
	if launch < 0 || first < launch || second < first {
		t.Errorf("notes out of order or missing: %q", req.Note)
	}
	if got := humanNote(runNote(&runlog.Run{ResumeNotes: []runlog.ResumeNote{{Note: "only on resume"}}})); !strings.Contains(got, "only on resume") {
		t.Errorf("a run launched without a note lost its resume note: %q", got)
	}
}

// A note on a resume that cannot carry it is refused, never dropped: a gate is
// answered, not resumed, and a document stage takes its instruction as a
// revision.
func TestAResumeNoteIsRefusedWhereItCannotRide(t *testing.T) {
	for _, tc := range []struct{ stage, kind, want string }{
		{"build", "gate", "waits at its gate"},
		{"spec", "error", "takes no note on resume"},
		{"plan", "provider", "takes no note on resume"},
	} {
		t.Run(tc.stage+"/"+tc.kind, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			_, rec := pausedRunWithNote(t, s, tc.stage, tc.kind)
			_, err := s.RunResumeWithNote(context.Background(), "r-20261003-145618-bbdk", ti36xResumeNote, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want a refusal containing %q", err, tc.want)
			}
			s.runsMu.RLock()
			rs := s.runs["r-20261003-145618-bbdk"]
			s.runsMu.RUnlock()
			got := rs.snapshotRun()
			if got.Status != "paused" || len(got.ResumeNotes) != 0 {
				t.Errorf("a refused resume changed the run: status %s notes %v", got.Status, got.ResumeNotes)
			}
			if len(rec.requests) != 0 {
				t.Error("a refused resume called a model")
			}
		})
	}
}

// chainProject is a project whose task T-014 has an accepted red test that
// lives only in its (deleted) run branch — the TI-36X state after the chained
// build paused and was aborted.
func chainProject(t *testing.T, s *Service) (projectID, dir, redSHA string) {
	t.Helper()
	dir = t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "TI-36X", GitInit: true, GitName: "Ada", GitEmail: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindPlan)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan),
		[]byte("## M-001 — Core\n\n### T-014 — Scientific notation\n\nRender 2^-9.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("add", "-A")
	git("commit", "-q", "-m", "plan", "--allow-empty")
	mainBranch := git("rev-parse", "--abbrev-ref", "HEAD")
	git("checkout", "-q", "-b", "ducklab/T-014-qzkf")
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "sci.test.mjs"), []byte("assert(render(2**-9) === '0.001953125')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "ducklab: T-014")
	redSHA = git("rev-parse", "HEAD")
	git("checkout", "-q", mainBranch)
	// The accept's cleanup retires the run branch; the commit survives as an
	// object, which is all a relaunch needs.
	git("branch", "-q", "-D", "ducklab/T-014-qzkf")
	return p.ID, dir, redSHA
}

func recordRun(t *testing.T, s *Service, dir string, run *runlog.Run) {
	t.Helper()
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func acceptedRedTest(projectID, sha string) *runlog.Run {
	return &runlog.Run{
		ID: "r-20261003-140000-qzkf", ProjectID: projectID, TaskID: "T-014", Stage: "test", Mode: "solo",
		Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: sha,
		StartedAt: "2026-10-03T14:00:00Z", EndedAt: "2026-10-03T14:50:00Z",
	}
}

// B-493 (b): the real case. T-014's chained build was aborted; the person
// relaunches the build with the note it could not get on resume. The new
// build starts on the accepted red test — the base continueChain would have
// given it — and is judged by the same oracle tests.
func TestARelaunchedChainBuildStartsOnItsAcceptedRedTest(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	s.ducklings.RegisterProvider(&recordingProvider{})
	projectID, dir, red := chainProject(t, s)
	recordRun(t, s, dir, acceptedRedTest(projectID, red))

	run, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-014", Mode: "solo", Note: ti36xResumeNote})
	if err != nil {
		t.Fatalf("relaunching the build of a broken chain was refused: %v", err)
	}
	cleanupStartedRun(t, s, run.ID)
	if run.BaseSHA != red {
		t.Errorf("relaunched build base = %s, want the accepted red test %s", short(run.BaseSHA), short(red))
	}
	if want := chainOracleTests(dir, red); len(want) == 0 || !slices.Equal(run.OracleTests, want) {
		t.Errorf("oracle tests = %v, want the chain's %v", run.OracleTests, want)
	}
	if _, err := os.Stat(filepath.Join(run.WorktreePath, "tests", "sci.test.mjs")); err != nil {
		t.Errorf("the build's tree lacks the red test it must make green: %v", err)
	}
	events, _ := runlog.ReadEvents(filepath.Join(dir, ".ducklab", "runs", run.ID))
	rejoined := false
	for _, e := range events {
		if e.Type == "tdd_chain_rejoined" && e.Data["base"] == red && e.Data["test_run"] == "r-20261003-140000-qzkf" {
			rejoined = true
		}
	}
	if !rejoined {
		t.Error("the record does not say the build rejoined its chain")
	}
}

// When the red test's commit is gone (gc pruned it after its branch was
// deleted), starting from the default branch would build without the test;
// the launch is refused and names the commit and the way forward.
func TestARelaunchedChainBuildWhoseRedTestIsGoneIsRefused(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, dir, _ := chainProject(t, s)
	const gone = "2482177000000000000000000000000000000000"
	recordRun(t, s, dir, acceptedRedTest(projectID, gone))

	_, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-014", Mode: "solo"})
	if err == nil {
		t.Fatal("a build without its red test was started")
	}
	for _, want := range []string{"2482177", "r-20261003-140000-qzkf", "Retire the test"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
}

// The chain base is only for a chain that is still broken: a red test that
// reached the default branch, or a task already built after its test, starts
// from the default branch as always.
func TestABuildStartsFromTheDefaultBranchWhenNoChainIsBroken(t *testing.T) {
	t.Run("red test on the default branch", func(t *testing.T) {
		s := serviceWithDucklings(t, "pato-uno")
		s.ducklings.RegisterProvider(&recordingProvider{})
		projectID, dir, _ := chainProject(t, s)
		head, err := vcs.New(dir).DefaultBranchHead()
		if err != nil {
			t.Fatal(err)
		}
		recordRun(t, s, dir, acceptedRedTest(projectID, head))
		run, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-014", Mode: "solo"})
		if err != nil {
			t.Fatal(err)
		}
		cleanupStartedRun(t, s, run.ID)
		if run.BaseSHA != head || len(run.OracleTests) != 0 {
			t.Errorf("base = %s oracle = %v, want the default head and no oracle", short(run.BaseSHA), run.OracleTests)
		}
		events, _ := runlog.ReadEvents(filepath.Join(dir, ".ducklab", "runs", run.ID))
		for _, e := range events {
			if e.Type == "tdd_chain_rejoined" {
				t.Error("a build whose red test already landed claims to rejoin a broken chain")
			}
		}
	})
	t.Run("built after the test", func(t *testing.T) {
		s := serviceWithDucklings(t, "pato-uno")
		s.ducklings.RegisterProvider(&recordingProvider{})
		projectID, dir, red := chainProject(t, s)
		head, err := vcs.New(dir).DefaultBranchHead()
		if err != nil {
			t.Fatal(err)
		}
		recordRun(t, s, dir, acceptedRedTest(projectID, red))
		recordRun(t, s, dir, &runlog.Run{
			ID: "r-20261003-150000-done", ProjectID: projectID, TaskID: "T-014", Stage: "build", Mode: "solo",
			Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: head,
			StartedAt: "2026-10-03T15:00:00Z", EndedAt: "2026-10-03T15:30:00Z",
		})
		run, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-014", Mode: "solo", Redo: true, Note: "redo"})
		if err != nil {
			t.Fatal(err)
		}
		cleanupStartedRun(t, s, run.ID)
		if run.BaseSHA != head {
			t.Errorf("base = %s, want the default head %s", short(run.BaseSHA), short(head))
		}
	})
}
