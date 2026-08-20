package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
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

// A screenshot is evidence for precisely one reply.  It is retained on the
// human event, but must not be smuggled into later text-only turns when the
// conversation is replayed.
func TestChatImagesReachSeeingConsultantAndRemainWithTheirMessage(t *testing.T) {
	const image = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/bwAAAABJRU5ErkJggg=="
	var mu sync.Mutex
	var requests []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"stream":true`) {
			mu.Lock()
			requests = append(requests, string(body))
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"seen\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"))
	}))
	defer provider.Close()

	isolate(t)
	seeing := true
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{"test": {Kind: config.ProviderKindOpenAI, BaseURL: provider.URL}}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{"seer": {Provider: "test", Model: "m", Caps: config.Caps{Vision: &seeing}}}
	s, err := New(cfg, Options{Bus: bus.New(32)})
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := projectWithConfig(t, s, "chat-images")

	run := chatStartWithImages(t, s, projectID, ChatStartRequest{Duckling: "seer", Message: "What is wrong?"}, []string{image})
	waitForChatPause(t, s, run.ID)
	chatSendWithImages(t, s, run.ID, "What about this text-only state?", nil)
	waitForChatPause(t, s, run.ID)
	chatSendWithImages(t, s, run.ID, "And this screenshot?", []string{image})
	waitForChatPause(t, s, run.ID)

	mu.Lock()
	gotRequests := append([]string(nil), requests...)
	mu.Unlock()
	if len(gotRequests) != 3 {
		t.Fatalf("consultant requests = %d, want one reply per human message", len(gotRequests))
	}
	for _, n := range []int{0, 2} {
		if !strings.Contains(gotRequests[n], "image_url") || !strings.Contains(gotRequests[n], image) {
			t.Errorf("reply %d did not receive its screenshot: %s", n+1, gotRequests[n])
		}
	}
	if strings.Contains(gotRequests[1], "image_url") || strings.Contains(gotRequests[1], image) {
		t.Errorf("text-only reply was sent an earlier screenshot: %s", gotRequests[1])
	}

	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	humanImages, warnings := 0, 0
	for _, event := range detail.Events {
		if event.Type == "message" && event.Data["role"] == "human" && reflect.DeepEqual(event.Data["images"], []interface{}{image}) {
			humanImages++
		}
		if event.Type == "warning" && strings.Contains(fmt.Sprint(event.Data["detail"]), "1 screenshot(s) shown") {
			warnings++
		}
	}
	if humanImages != 2 {
		t.Errorf("human events retaining screenshots = %d, want 2", humanImages)
	}
	if warnings != 2 {
		t.Errorf("screenshot warnings = %d, want 2", warnings)
	}
}

// Both the declared capability and the decoded payload are boundaries. A
// text-only duckling must be refused before its endpoint is called; malformed,
// non-image, and over-limit data URLs must identify the offending image.
func TestChatImagesRequireSeeingDucklingAndValidPayload(t *testing.T) {
	textOnly := serviceWithDucklings(t, "text-only")
	projectID, _ := projectWithConfig(t, textOnly, "chat-images")
	valid := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("small image"))
	_, err := chatStartWithImagesErr(textOnly, projectID, ChatStartRequest{Duckling: "text-only", Message: "look"}, []string{valid})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "seeing duckling") {
		t.Fatalf("text-only consultant error = %v, want refusal to pick a seeing duckling", err)
	}

	seeing := true
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{"fake": {Kind: config.ProviderKindOpenAI, BaseURL: "fake://"}}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{"seer": {Provider: "fake", Model: "m", Caps: config.Caps{Vision: &seeing}}}
	s, err := New(cfg, Options{Bus: bus.New(8)})
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ = projectWithConfig(t, s, "chat-image-validation")
	for name, image := range map[string]string{
		"non-image": "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("not a picture")),
		"oversized": "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, (8<<20)+1)),
		"malformed": "data:image/png;base64,not base64!",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := chatStartWithImagesErr(s, projectID, ChatStartRequest{Duckling: "seer", Message: "look"}, []string{image})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "image 1") {
				t.Fatalf("invalid image error = %v, want invalid_request naming image 1", err)
			}
		})
	}
}

// A configured vision:true is a claim, not evidence. A llama.cpp endpoint without
// its projector rejects image parts, so the first image attempt must establish that
// fact before a chat stream is opened. The negative answer is then remembered: a
// second attempt is refused locally instead of repeatedly sending unsupported data.
func TestChatImagesProbeAndCacheMMProjLessEndpointBeforeStartingChat(t *testing.T) {
	const image = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/bwAAAABJRU5ErkJggg=="

	var mu sync.Mutex
	requests := 0
	streamedChat := false
	visionProbe := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests++
		visionProbe = visionProbe || strings.Contains(string(body), `"image_url"`)
		streamedChat = streamedChat || strings.Contains(string(body), `"stream":true`)
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("image input is not supported: no vision projector (mmproj) loaded"))
	}))
	defer server.Close()

	isolate(t)
	seeing := true
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{"llama": {Kind: config.ProviderKindOpenAI, BaseURL: server.URL}}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{"claimed-seer": {Provider: "llama", Model: "m", Caps: config.Caps{Vision: &seeing}}}
	s, err := New(cfg, Options{Bus: bus.New(8)})
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := projectWithConfig(t, s, "mmproj-less-chat")
	req := ChatStartRequest{Duckling: "claimed-seer", Message: "What does this show?"}

	_, err = chatStartWithImagesErr(s, projectID, req, []string{image})
	if err == nil {
		t.Fatal("image chat started against an endpoint that rejected image input")
	}
	for _, want := range []string{"mmproj", "--mmproj", "seeing duckling"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Errorf("image capability error = %q, want guidance mentioning %q", err, want)
		}
	}

	mu.Lock()
	firstRequests, firstStreamedChat, firstVisionProbe := requests, streamedChat, visionProbe
	mu.Unlock()
	if !firstVisionProbe {
		t.Error("declared vision capability was not verified with an image request")
	}
	if firstStreamedChat {
		t.Error("unsupported image opened a chat stream instead of being refused before the chat")
	}

	_, err = chatStartWithImagesErr(s, projectID, req, []string{image})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "seeing duckling") {
		t.Fatalf("cached non-vision duckling error = %v, want refusal to pick a seeing duckling", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != firstRequests {
		t.Errorf("cached non-vision result made %d additional endpoint calls, want 0", requests-firstRequests)
	}
}

func chatStartWithImages(t *testing.T, s *Service, projectID string, req ChatStartRequest, images []string) *runlog.Run {
	t.Helper()
	run, err := chatStartWithImagesErr(s, projectID, req, images)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func chatStartWithImagesErr(s *Service, projectID string, req ChatStartRequest, images []string) (*runlog.Run, error) {
	field := reflect.ValueOf(&req).Elem().FieldByName("Images")
	if !field.IsValid() {
		return nil, fmt.Errorf("ChatStartRequest must accept images")
	}
	field.Set(reflect.ValueOf(images))
	out := reflect.ValueOf(s.ChatStart).Call([]reflect.Value{reflect.ValueOf(context.Background()), reflect.ValueOf(projectID), reflect.ValueOf(req)})
	if !out[1].IsNil() {
		return nil, out[1].Interface().(error)
	}
	return out[0].Interface().(*runlog.Run), nil
}

func chatSendWithImages(t *testing.T, s *Service, runID, message string, images []string) {
	t.Helper()
	method := reflect.ValueOf(s).MethodByName("ChatSend")
	if method.Type().NumIn() != 4 {
		t.Fatalf("ChatSend accepts %d arguments, want context, run ID, message, and images", method.Type().NumIn())
	}
	out := method.Call([]reflect.Value{reflect.ValueOf(context.Background()), reflect.ValueOf(runID), reflect.ValueOf(message), reflect.ValueOf(images)})
	if !out[1].IsNil() {
		t.Fatal(out[1].Interface().(error))
	}
}

func waitForChatPause(t *testing.T, s *Service, runID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.runsMu.RLock()
		rs := s.runs[runID]
		s.runsMu.RUnlock()
		if rs != nil && rs.run.Status == "paused" && rs.run.PendingKind == "chat" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("chat did not pause for its next message")
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
