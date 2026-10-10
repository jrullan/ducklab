package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/strategy"
)

// imageRefusingFake is the test fleet's fake provider behind an endpoint
// that has no vision projector for some models: their image requests are
// rejected the way llama.cpp without --mmproj rejects them; everything else
// reaches the fake.
type imageRefusingFake struct {
	*provider.Fake
	blind map[string]bool
	mu    sync.Mutex
	// refused counts the image requests rejected, per model.
	refused map[string]int
}

func (p *imageRefusingFake) refuse(req provider.ChatRequest) error {
	if !p.blind[req.Model] || !agent.RequestCarriesImages(req) {
		return nil
	}
	p.mu.Lock()
	p.refused[req.Model]++
	p.mu.Unlock()
	return fmt.Errorf("chat: 500 Internal Server Error: %w: model/server has no vision projector (mmproj)", provider.ErrVisionUnsupported)
}

func (p *imageRefusingFake) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if err := p.refuse(req); err != nil {
		return provider.ChatResponse{}, err
	}
	return p.Fake.Chat(ctx, req)
}

func (p *imageRefusingFake) ChatStream(ctx context.Context, req provider.ChatRequest, ch chan<- provider.Delta) (provider.ChatResponse, error) {
	if err := p.refuse(req); err != nil {
		return provider.ChatResponse{}, err
	}
	return p.Fake.ChatStream(ctx, req, ch)
}

func refuseImagesFor(s *Service, models ...string) *imageRefusingFake {
	p := &imageRefusingFake{Fake: s.providers["fake"].(*provider.Fake), blind: map[string]bool{}, refused: map[string]int{}}
	for _, m := range models {
		p.blind[m] = true
	}
	s.ducklings.RegisterProvider(p)
	return p
}

func visionStatusOf(s *Service, id string) string {
	for _, d := range s.ducklings.List() {
		if string(d.ID) == id {
			return d.VisionStatus
		}
	}
	return ""
}

func visionEvidence(events []*runlog.Event) map[string]string {
	out := map[string]string{}
	for _, e := range eventsOf(events, "vision_evidence") {
		out[stringValueAny(e.Data["duckling"])] = stringValueAny(e.Data["vision"])
	}
	return out
}

// B-515, TI-36X T-008 (r-20261009-152334-6x3f): luna and glm52 answered
// build and review turns that carried the reference image, and the list
// still said "declared". Every answered image request now verifies the
// duckling, under the probe's key, and the run records it once.
func TestB515ABuildThatShowedImagesVerifiesTheSeats(t *testing.T) {
	_, events, _, s := visionBuild(t, "pair", true, false, nil)
	for _, id := range []string{"luna", "glm52"} {
		if got := visionStatusOf(s, id); got != duckling.VisionVerified {
			t.Errorf("%s vision_status = %q after answering image turns, want verified", id, got)
		}
	}
	if got := visionEvidence(events); got["luna"] != "verified" || got["glm52"] != "verified" {
		t.Errorf("vision_evidence = %v, want both seats verified", got)
	}
	// Recorded once per change, not once per request.
	if n := len(eventsOf(events, "vision_evidence")); n != 2 {
		t.Errorf("vision_evidence events = %d, want one per seat", n)
	}
}

// An endpoint that rejects the images refutes the seat: the turn that hit
// the rejection continues once without them, and the seat's following
// turns in the run are blind — told why, recorded with the reason, never
// sent an image again. (This solo fixture runs three rounds.)
func TestB515ARefusedSeatIsBlindForTheRestOfTheRun(t *testing.T) {
	var gate *imageRefusingFake
	reqs, events, _, s := visionBuild(t, "solo", true, false, func(s *Service) { gate = refuseImagesFor(s, "m-luna") })
	if gate.refused["m-luna"] != 1 {
		t.Errorf("luna's endpoint rejected %d image requests, want exactly the first", gate.refused["m-luna"])
	}
	if got := visionStatusOf(s, "luna"); got != duckling.VisionRefuted {
		t.Errorf("luna vision_status = %q, want refuted", got)
	}
	if got := visionEvidence(events); got["luna"] != "refuted" {
		t.Errorf("vision_evidence = %v, want luna refuted", got)
	}
	if n := len(eventsOf(events, "images_refused")); n != 1 {
		t.Errorf("images_refused events = %d, want 1", n)
	}
	for _, req := range reqs {
		if agent.RequestCarriesImages(req) {
			t.Errorf("an image request reached the fake after the refusal")
		}
	}
	turns := eventsOf(events, "turn_images")
	if len(turns) < 2 {
		t.Fatalf("turn_images = %d, want one per implementer turn of several rounds", len(turns))
	}
	if turns[0].Data["can_see"] != true {
		t.Errorf("the first turn was decided blind before any evidence: %v", turns[0].Data)
	}
	for _, e := range turns[1:] {
		reason := stringValueAny(e.Data["reason"])
		if e.Data["can_see"] != false || !strings.Contains(reason, "rejected image input") ||
			!strings.Contains(stringSliceJoin(e.Data["notes"]), "rejected image input") {
			t.Errorf("a later turn_images = %v, want can_see false with the rejection as reason", e.Data)
		}
	}
	var told int
	for _, req := range reqs {
		for _, m := range req.Messages {
			if m.Role == "user" && strings.Contains(m.Content, "No image is attached to this turn: seat declares vision, but its endpoint rejected image input") {
				told++
				break
			}
		}
	}
	if told == 0 {
		t.Error("no later implementer prompt says why it has no image")
	}
	detail, err := s.RunGet(context.Background(), turns[0].RunID)
	if err == nil && detail.Run.Status == "failed" {
		t.Errorf("the run failed on the refusal: %s", detail.Run.Failure)
	}
}

// Provider weather on an image request is not evidence, and a text-only
// request proves nothing about vision either way.
func TestB515WeatherAndTextRequestsRecordNothing(t *testing.T) {
	s := serviceWithDucklings(t, "luna")
	setVision(s, "luna", true)
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(provider.ChatRequest, int) *provider.ChatResponse {
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: "ok"}, FinishReason: provider.FinishStop}}}
	}
	var emitted []string
	p := observedProvider{Provider: fake, id: "luna", registry: s.ducklings,
		emit: func(kind string, _ map[string]interface{}) error { emitted = append(emitted, kind); return nil }}
	text := provider.ChatRequest{Model: "m-luna", Messages: []provider.Message{{Role: "user", Content: "hi"}}}
	image := provider.ChatRequest{Model: "m-luna", Messages: []provider.Message{{Role: "user", Content: "look", Images: []string{"data:image/png;base64,AA=="}}}}

	if _, err := p.Chat(context.Background(), text); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		fmt.Errorf("chat: %w", provider.ErrRateLimit),
		fmt.Errorf("chat: %w", provider.ErrProviderUnavailable),
		errors.New("chat: 400 Bad Request: context length exceeded"),
	} {
		p.observe(image, err)
	}
	if got := visionStatusOf(s, "luna"); got != duckling.VisionDeclared {
		t.Errorf("vision_status = %q after text and weather, want declared", got)
	}
	if len(emitted) != 0 {
		t.Errorf("events = %v, want none", emitted)
	}
	if _, err := p.Chat(context.Background(), image); err != nil {
		t.Fatal(err)
	}
	if got := visionStatusOf(s, "luna"); got != duckling.VisionVerified || len(emitted) != 1 || emitted[0] != "vision_evidence" {
		t.Errorf("after an answered image: status %q, events %v", got, emitted)
	}
}

// Every agent loop — the only way a stage architect, a build, test-first or
// review seat, an advisor consult, a triager or a consultant reaches a
// provider — is built over the observed provider and consults the seat's
// recorded vision. This is what makes the one hook cover every path.
func TestB515EveryLoopRecordsEvidenceAndConsultsIt(t *testing.T) {
	s := serviceWithDucklings(t, "luna")
	setVision(s, "luna", true)
	loop, err := s.buildLoop(context.Background(), "luna", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loop.Provider.(observedProvider); !ok {
		t.Fatalf("loop provider is %T, want observedProvider", loop.Provider)
	}
	if loop.SeesImages == nil || !loop.SeesImages() {
		t.Fatal("a declared, untested seat must be shown images")
	}
	s.ducklings.RecordImageEvidence("luna", provider.ErrVisionUnsupported)
	if loop.SeesImages() {
		t.Error("a refuted seat is still shown images")
	}
}

// The advisor's turns go through the same runner wrap as the implementer's
// (B-507): once its seat is refuted it is told it cannot see, with why.
func TestB515ARefutedAdvisorIsToldWhy(t *testing.T) {
	s := serviceWithDucklings(t, "luna")
	setVision(s, "luna", true)
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: planDoc, artifact.KindSpec: visionSpecDoc, artifact.KindRequirements: visionReqDoc,
	})
	storeRefImage(t, dir, visionRefFile, solidPNG(t, 8, 16, color.RGBA{R: 40, G: 40, B: 40, A: 255}))
	var events []map[string]interface{}
	roster := map[config.Role]config.DucklingID{config.RoleAdvisor: "luna"}
	v := s.newTaskVision(context.Background(), projectID, "T-001", []string{dir}, roster,
		[]config.Role{config.RoleAdvisor}, func(kind string, data map[string]interface{}) {
			events = append(events, map[string]interface{}{"kind": kind, "data": data})
		})
	s.ducklings.RecordImageEvidence("luna", provider.ErrVisionUnsupported)
	var seen []seenTurn
	run := v.wrap(recordingRunner(&seen, func(*strategy.Turn, int) *agent.Outcome { return approve() }), roster)
	if _, err := run(context.Background(), &strategy.Turn{Role: config.RoleAdvisor}, "luna", "advise", nil, strategy.TurnContext{Round: 1}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || len(seen[0].images) != 0 || !strings.Contains(seen[0].prompt, "this seat cannot see images") ||
		!strings.Contains(seen[0].prompt, "endpoint rejected image input") {
		t.Errorf("advisor turn = %+v", seen)
	}
	var recorded bool
	for _, e := range events {
		d, _ := e["data"].(map[string]interface{})
		if e["kind"] == "turn_images" && d["can_see"] == false && strings.Contains(stringValueAny(d["reason"]), "rejected image input") {
			recorded = true
		}
	}
	if !recorded {
		t.Errorf("no turn_images records the refuted advisor: %v", events)
	}
}

// A document stage's architect answered the reference image: verified.
func TestB515AStageArchitectWhoSawTheImageIsVerified(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	setVision(s, "pato-uno", true)
	projectID, _ := projectWithDocs(t, s, map[artifact.Kind]string{})
	img := filepath.Join(t.TempDir(), "ti36x.png")
	writePNG(t, img, 30, 60)
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(provider.ChatRequest, int) *provider.ChatResponse {
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: "## REQ-001 — Matches REF-IMG-1\n\n**Priority:** must\n\nThe face matches REF-IMG-1 at 1x.\n"},
			FinishReason: provider.FinishStop,
		}}}
	}
	run, err := s.StageStart(context.Background(), projectID, StageRequest{
		Stage: "intake", Mode: "solo", From: "A pixel perfect calculator.", Refs: []string{img}, Ducklings: []string{"pato-uno"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, run.ID)
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	<-rs.done
	if got := visionStatusOf(s, "pato-uno"); got != duckling.VisionVerified {
		t.Errorf("architect vision_status = %q, want verified", got)
	}
}

// A stage started after the architect was refuted drops the images before
// the turn, and says why.
func TestB515ARefutedArchitectIsNotSentTheImage(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	setVision(s, "pato-uno", true)
	s.ducklings.RecordImageEvidence("pato-uno", provider.ErrVisionUnsupported)
	projectID, _ := projectWithDocs(t, s, map[artifact.Kind]string{})
	img := filepath.Join(t.TempDir(), "ti36x.png")
	writePNG(t, img, 30, 60)
	gate := refuseImagesFor(s, "m-pato-uno")
	gate.Fake.ScriptFunc = func(provider.ChatRequest, int) *provider.ChatResponse {
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: "## REQ-001 — Matches REF-IMG-1\n\n**Priority:** must\n\nThe face matches REF-IMG-1 at 1x.\n"},
			FinishReason: provider.FinishStop,
		}}}
	}
	run, err := s.StageStart(context.Background(), projectID, StageRequest{
		Stage: "intake", Mode: "solo", From: "A pixel perfect calculator.", Refs: []string{img}, Ducklings: []string{"pato-uno"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, run.ID)
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	<-rs.done
	if gate.refused["m-pato-uno"] != 0 {
		t.Errorf("a refuted architect was sent the image %d time(s)", gate.refused["m-pato-uno"])
	}
	events, _ := runlog.ReadEvents(rs.runDir)
	var dropped bool
	for _, e := range eventsOf(events, "warning") {
		dropped = dropped || strings.Contains(stringValueAny(e.Data["detail"]), "rejected image input")
	}
	if !dropped {
		t.Errorf("no warning says why the image was dropped: %v", eventsOf(events, "warning"))
	}
}

// The triager's screenshot, answered: verified. On a server without a
// projector, refuted beforehand, the screenshot is not sent at all.
func TestB515ATriagersScreenshotIsEvidence(t *testing.T) {
	for _, refuted := range []bool{false, true} {
		var images atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			// The attachment's own bytes, not the engine's 1x1 vision probe.
			if strings.Contains(string(body), "R42mP8z8BQDwAEhQ") {
				images.Add(1)
			}
			if refuted && strings.Contains(string(body), "image_url") {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("image input is not supported: no vision projector (mmproj) loaded"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"severity\":\"normal\",\"component\":\"ui\",\"reason\":\"seen\",\"task_title\":\"fix\",\"suspected_files\":[]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
		}))
		isolate(t)
		cfg := config.DefaultGlobal()
		yes := true
		cfg.Providers = map[config.ProviderID]config.Provider{"test": {Kind: "openai", BaseURL: srv.URL}}
		cfg.Ducklings = map[config.DucklingID]config.Duckling{"seer": {Provider: "test", Model: "m", Caps: config.Caps{Vision: &yes}}}
		s, err := New(cfg, Options{Bus: bus.New(64)})
		if err != nil {
			t.Fatal(err)
		}
		if refuted {
			s.ducklings.RecordImageEvidence("seer", provider.ErrVisionUnsupported)
		}
		projectID := newTestProject(t, s, "proj")
		entry, _ := s.registry.Get(projectID)
		if err := os.MkdirAll(filepath.Join(entry.Path, ".ducklab"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(entry.Path, ".ducklab", "project.toml"), []byte("id = \"proj\"\nname = \"proj\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		b, err := s.BugAdd(ctx, projectID, BugRequest{Title: "broken badge", Severity: "normal"})
		if err != nil {
			t.Fatal(err)
		}
		png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
		if _, err := s.BugAttach(ctx, projectID, b.ID, "shot.png", png); err != nil {
			t.Fatal(err)
		}
		run, err := s.BugTriage(ctx, projectID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = s.waitForRun(ctx, run.ID)
		srv.Close()
		switch {
		case refuted && images.Load() != 0:
			t.Errorf("a refuted triager was sent the screenshot")
		case !refuted && visionStatusOf(s, "seer") != duckling.VisionVerified:
			t.Errorf("triager vision_status = %q after seeing the screenshot, want verified", visionStatusOf(s, "seer"))
		}
		// The record must not claim a screenshot was shown when it was not.
		events, _ := runlog.ReadEvents(filepath.Join(entry.Path, ".ducklab", "runs", run.ID))
		var claimed bool
		for _, e := range eventsOf(events, "warning") {
			claimed = claimed || strings.Contains(stringValueAny(e.Data["detail"]), "shown to the triager")
		}
		if claimed == refuted {
			t.Errorf("refuted=%v: the record's \"shown to the triager\" warning = %v", refuted, claimed)
		}
	}
}

// The consultant chat: its probe said the duckling sees; then the server
// lost its projector. The chat turn that carried the screenshot continues
// without it, the duckling is refuted, and the next screenshot is refused
// before it is sent.
func TestB515AConsultantWhoseServerLostItsProjector(t *testing.T) {
	const image = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/bwAAAABJRU5ErkJggg=="
	var projector atomic.Bool
	projector.Store(true)
	var streamedImages atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		hasImage := strings.Contains(string(body), `"image_url"`)
		if hasImage && !projector.Load() {
			if strings.Contains(string(body), `"stream":true`) {
				streamedImages.Add(1)
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("image input is not supported: no vision projector (mmproj) loaded"))
			return
		}
		if !strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"seen\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	isolate(t)
	seeing := true
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{"llama": {Kind: config.ProviderKindOpenAI, BaseURL: srv.URL}}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{"seer": {Provider: "llama", Model: "m", Caps: config.Caps{Vision: &seeing}}}
	s, err := New(cfg, Options{Bus: bus.New(64)})
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := projectWithConfig(t, s, "lost-projector")
	run := chatStartWithImages(t, s, projectID, ChatStartRequest{Duckling: "seer", Message: "What is wrong?"}, []string{image})
	waitForChatPause(t, s, run.ID)
	if got := visionStatusOf(s, "seer"); got != duckling.VisionVerified {
		t.Fatalf("after an answered screenshot vision_status = %q, want verified", got)
	}

	projector.Store(false)
	chatSendWithImages(t, s, run.ID, "And this one?", []string{image})
	waitForChatPause(t, s, run.ID)
	if got := visionStatusOf(s, "seer"); got != duckling.VisionRefuted {
		t.Errorf("after a rejected screenshot vision_status = %q, want refuted", got)
	}
	if n := streamedImages.Load(); n != 1 {
		t.Errorf("rejected image requests = %d, want 1 (then the reply without it)", n)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status == "failed" {
		t.Fatalf("the chat failed on the rejection: %s", detail.Run.Failure)
	}
	var refused, evidence bool
	for _, e := range detail.Events {
		refused = refused || e.Type == "images_refused"
		evidence = evidence || (e.Type == "vision_evidence" && e.Data["vision"] == "refuted")
	}
	if !refused || !evidence {
		t.Errorf("images_refused=%v vision_evidence(refuted)=%v, want both on the record", refused, evidence)
	}
	if _, err := s.ChatSend(context.Background(), run.ID, "Third?", []string{image}); err == nil || !strings.Contains(err.Error(), "mmproj") {
		t.Errorf("a screenshot to a refuted consultant = %v, want the no-projector refusal", err)
	}
}
