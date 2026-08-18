package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

// A chat is a run: the person picks a duckling, asks about a subject, the
// consultant answers with the dossier in hand and read-only tools, and the
// conversation pauses for the next message — memory in the event log, so
// nothing is lost between turns or restarts.
func TestAChatConversesAndPausesForTheNextMessage(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.BugAdd(context.Background(), p.ID, BugRequest{
		Title: "401 on every view", Body: "the frontend never sends X-User-ID",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := s.ChatStart(context.Background(), p.ID, ChatStartRequest{
		Duckling: "pato-dos", AboutKind: "bug", AboutID: b.ID,
		Message: "this bug is not fixed; check why the 401 still happens",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Stage != "chat" || run.Roster["consultant"] != "pato-dos" {
		t.Fatalf("run = %s/%v", run.Stage, run.Roster)
	}

	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	waitPaused := func() {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if rs.run.Status == "paused" && rs.run.PendingKind == "chat" {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("chat never paused for the next message: %s/%s (%s)", rs.run.Status, rs.run.PendingKind, rs.run.Failure)
	}
	waitPaused()
	if got := runNext(rs.run); len(got) == 0 || got[0] != "reply" {
		t.Errorf("next = %v, want reply first", got)
	}

	// The next message re-enters with the conversation intact.
	if _, err := s.ChatSend(context.Background(), run.ID, "and what should I do about it?"); err != nil {
		t.Fatal(err)
	}
	waitPaused()

	// The dossier reached the prompt: the consultant's context named the bug.
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	msgs := 0
	for _, e := range detail.Events {
		if e.Type == "message" {
			msgs++
		}
	}
	if msgs < 4 {
		t.Errorf("the record holds %d messages, want the full alternation (2 human + 2 consultant)", msgs)
	}
	if !strings.Contains(detail.Run.Note, b.ID) {
		t.Errorf("the run does not say what it is about: %q", detail.Run.Note)
	}

	// A chat cannot be fed while it is thinking or after it ends.
	if _, err := s.ChatSend(context.Background(), "r-nope", "hi"); err == nil {
		t.Error("sent to a chat that does not exist")
	}
}

// A finished consultation ends done, not ABORTED — abort means the person
// gave up on it, and the record must not say that about a chat that worked.
func TestEndingAChatIsNotAnAbort(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.ChatStart(context.Background(), p.ID, ChatStartRequest{
		Duckling: "pato-uno", AboutKind: "task", AboutID: "T-001", Message: "why?",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !(rs.run.Status == "paused" && rs.run.PendingKind == "chat") {
		time.Sleep(50 * time.Millisecond)
	}
	got, err := s.ChatEnd(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" || got.Verdict == "ABORTED" {
		t.Errorf("ended chat = %s/%s, want done and never ABORTED", got.Status, got.Verdict)
	}
	if !strings.Contains(got.Resolution, "ended by human") {
		t.Errorf("resolution = %q", got.Resolution)
	}
	// Ended is ended.
	if _, err := s.ChatEnd(context.Background(), run.ID); err == nil {
		t.Error("ended a chat twice")
	}
}

// Every tool the chat belt names must resolve in the registry. bug_read was
// named here since the chat existed — and never implemented, so the loop
// silently dropped it: the prompt promised a tool the model could not call.
// A named tool that does not resolve is a lie told to a prompt.
func TestEveryChatToolExists(t *testing.T) {
	r := tools.NewRegistry()
	for _, name := range strings.Split(chatToolbelt, ",") {
		if _, err := r.Get(strings.TrimSpace(name)); err != nil {
			t.Errorf("chat belt names %q: %v", name, err)
		}
	}
}

// The consultant may file a bug — the one loop-side act a conversation can
// conclude in — and still must not touch the tree.
func TestTheChatBeltFilesBugsButNeverWrites(t *testing.T) {
	belt := strings.Split(chatToolbelt, ",")
	has := func(name string) bool {
		for _, b := range belt {
			if strings.TrimSpace(b) == name {
				return true
			}
		}
		return false
	}
	if !has("bug_file") {
		t.Error("the chat belt lost bug_file")
	}
	for _, forbidden := range []string{"fs_write", "fs_patch", "fs_delete", "shell", "skill_run", "verify_run"} {
		if has(forbidden) {
			t.Errorf("the chat belt carries %s; a consultant must not touch the tree", forbidden)
		}
	}
}

// The guide rail says WHAT is next; a chat about "ducklab" is where the
// person asks WHY. The consultant answers from an embedded dossier — this
// binary's own laws, not a model's memory of other tools — with the live
// state the rail computes beside it, so the rail and the chat always tell
// the same story.
func TestAChatAboutDucklabCarriesTheHarnessDossier(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)

	run := &runlog.Run{
		ID: "r-why", ProjectID: projectID, Stage: "chat", Mode: "solo",
		Status: "running", StartedAt: "2026-08-11T09:00:00Z",
	}
	w, err := runlog.NewWriter(entry.Path, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: entry.Path}

	prompt := s.chatPromptFor(context.Background(), rs, entry.Path, "ducklab", "")
	for _, want := range []string{
		"authoritative description",       // the dossier frames itself as truth
		"gate is deterministic",           // the laws are in it
		"suggested next steps",            // and the live state rides beside it
		"Describe what you want to build", // a fresh project's guide says intake
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the ducklab dossier is missing %q", want)
		}
	}
}

// The dossier teaches the HOW, not only the why: a first-time user with
// nothing but an idea must be walkable, step by step, from a git repo to a
// cut release. The load-bearing steps are pinned so a future edit cannot
// quietly drop one.
func TestTheDossierWalksTheWholePath(t *testing.T) {
	for _, must := range []string{
		"git repository",        // step 1: a place to work
		"ducklings & providers", // step 2: a team
		"intake",                // step 3: say the idea
		"[verify]",              // step 5: the gate that makes "done" mean something
		"Test first",            // step 6: the build discipline
		"promote",               // step 8: bugs become tasks
		"Autopilot",             // step 9: the unattended loop, gated
		"Cut tags the version",  // step 10: the release
		"meet them at their",    // pedagogy: never dump the list
	} {
		if !strings.Contains(harnessDossier, must) {
			t.Errorf("the dossier lost %q from the idea-to-release path", must)
		}
	}
}

// A chat's tracker clock starts when the conversation opens and never stops,
// so a wallclock ceiling on it measures the PERSON's thinking time between
// messages. A consultant chat left open through an afternoon died
// mid-question at 7515s against the 1800s meant to stop runaway runs.
// Tokens, dollars and turns — the meters of real spend — keep their caps.
func TestAChatProjectWallclockOverrideDoesNotCreateACeiling(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, dir := projectWithConfig(t, s, "chat-budget")
	project, err := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	project.Budget.MaxWallclockS = 7200
	if err := config.SaveProject(filepath.Join(dir, ".ducklab", "project.toml"), project); err != nil {
		t.Fatal(err)
	}

	run, err := s.ChatStart(context.Background(), projectID, ChatStartRequest{
		Duckling: "pato-uno", Message: "how is the project doing?",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	deadline := time.Now().Add(5 * time.Second)
	for rs.run.Budget.Limit.Tokens == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rs.run.Budget.Limit.WallclockS != 0 {
		t.Errorf("chat inherited project wallclock ceiling = %d, want none", rs.run.Budget.Limit.WallclockS)
	}
}

func TestAChatHasNoWallclockCeiling(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.ChatStart(context.Background(), p.ID, ChatStartRequest{
		Duckling: "pato-uno", Message: "how is the project doing?",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	deadline := time.Now().Add(5 * time.Second)
	for rs.run.Budget.Limit.Tokens == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rs.run.Budget.Limit.WallclockS != 0 {
		t.Errorf("chat wallclock ceiling = %d, want none — idle time is not spend",
			rs.run.Budget.Limit.WallclockS)
	}
	if rs.run.Budget.Limit.Tokens == 0 {
		t.Error("the real-spend caps must survive: tokens ceiling is gone too")
	}
}
