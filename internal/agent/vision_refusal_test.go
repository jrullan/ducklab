package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

const testImage = "data:image/png;base64,iVBORw0KGgo="

// imageGate answers text and fails every request that carries an image with
// imageErr; textErr, when set, fails the text-only requests too.
type imageGate struct {
	mu       sync.Mutex
	requests []provider.ChatRequest
	imageErr error
	textErr  error
}

func (p *imageGate) ID() string { return "gate" }

func (p *imageGate) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	if RequestCarriesImages(req) && p.imageErr != nil {
		return provider.ChatResponse{}, p.imageErr
	}
	if p.textErr != nil {
		return provider.ChatResponse{}, p.textErr
	}
	return provider.ChatResponse{
		Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: "Looked at it."}, FinishReason: provider.FinishStop}},
		Usage:   provider.Usage{PromptTokens: 10, CompletionTokens: 5},
	}, nil
}

func (p *imageGate) ChatStream(ctx context.Context, req provider.ChatRequest, ch chan<- provider.Delta) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, provider.ErrUnsupported
}

func (p *imageGate) Models(ctx context.Context) ([]string, error) { return nil, nil }

type recovery struct {
	kind string
	data map[string]interface{}
}

func imageTurn(t *testing.T, p provider.Provider, sees func() bool) (*Outcome, error, []recovery, *recordingWriter) {
	t.Helper()
	loop := testLoop(p, 0)
	loop.SeesImages = sees
	w := &recordingWriter{}
	loop.RunWriter = w
	var recs []recovery
	loop.OnRecovery = func(_ *Turn, kind string, data map[string]interface{}) {
		recs = append(recs, recovery{kind, data})
	}
	turn := &Turn{Role: config.RoleImplementer, Prompt: "Build what the reference shows.", Contract: "freeform",
		MaxTurns: 2, Images: []string{testImage, testImage}}
	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: t.TempDir()})
	return out, err, recs, w
}

// B-515: an endpoint that rejects the images is retried ONCE without them,
// with a note in the message that carried them, and the rejection is on the
// record — the turn's text is still worth answering.
func TestB515ARefusedImageTurnContinuesOnceWithoutImages(t *testing.T) {
	p := &imageGate{imageErr: fmt.Errorf("chat: %w: no mmproj", provider.ErrVisionUnsupported)}
	out, err, recs, w := imageTurn(t, p, nil)
	if err != nil {
		t.Fatalf("the turn failed instead of continuing without images: %v", err)
	}
	if out.Text != "Looked at it." {
		t.Errorf("text = %q", out.Text)
	}
	if len(p.requests) != 2 {
		t.Fatalf("requests = %d, want the refused one and one retry", len(p.requests))
	}
	retry := p.requests[1]
	if RequestCarriesImages(retry) {
		t.Error("the retry still carried images")
	}
	var noted bool
	for _, m := range retry.Messages {
		noted = noted || (strings.Contains(m.Content, "Build what the reference shows.") && strings.Contains(m.Content, "endpoint rejected image input"))
	}
	if !noted {
		t.Errorf("the message that carried the images does not say why they are gone: %+v", retry.Messages)
	}
	if len(recs) != 1 || recs[0].kind != "images_refused" || recs[0].data["images"] != 2 {
		t.Errorf("recoveries = %+v, want one images_refused for two images", recs)
	}
	var refused bool
	for _, c := range w.calls {
		refused = refused || (c.FinishReason == "error" && strings.Contains(fmt.Sprint(c.Response["error"]), "image input is not supported"))
	}
	if !refused {
		t.Error("the refused call is not in llm.jsonl")
	}
}

// Never a loop: one refusal per call is retried; another failure of the
// retry is the turn's failure. Weather on an image request is not a
// refusal and gets no image-free retry.
func TestB515RefusalIsRetriedOnceAndWeatherIsNot(t *testing.T) {
	p := &imageGate{
		imageErr: fmt.Errorf("chat: %w", provider.ErrVisionUnsupported),
		textErr:  fmt.Errorf("chat: %w", provider.ErrVisionUnsupported),
	}
	if _, err, _, _ := imageTurn(t, p, nil); !provider.IsVisionUnsupported(err) || len(p.requests) != 2 {
		t.Errorf("err = %v after %d requests, want the second refusal to end the turn after 2", err, len(p.requests))
	}

	weather := &imageGate{imageErr: errors.New("chat: 400 Bad Request: context length exceeded")}
	_, err, recs, _ := imageTurn(t, weather, nil)
	if err == nil || len(weather.requests) != 1 || len(recs) != 0 {
		t.Errorf("weather: err=%v requests=%d recoveries=%+v, want one failed request and no image retry", err, len(weather.requests), recs)
	}
}

// A seat already known not to see (SeesImages false) is not sent the images
// at all: the turn is told, and the record says so.
func TestB515ABlindSeatsTurnWithholdsItsImages(t *testing.T) {
	p := &imageGate{imageErr: fmt.Errorf("chat: %w", provider.ErrVisionUnsupported)}
	_, err, recs, _ := imageTurn(t, p, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 1 || RequestCarriesImages(p.requests[0]) {
		t.Fatalf("requests = %d (images %v), want one image-free request", len(p.requests), len(p.requests) > 0 && RequestCarriesImages(p.requests[0]))
	}
	var noted bool
	for _, m := range p.requests[0].Messages {
		noted = noted || strings.Contains(m.Content, "no image is attached to this message")
	}
	if !noted {
		t.Error("the turn was not told its images were withheld")
	}
	if len(recs) != 1 || recs[0].kind != "images_withheld" || recs[0].data["images"] != 2 {
		t.Errorf("recoveries = %+v, want one images_withheld for two images", recs)
	}

	// A seeing seat keeps them.
	seer := &imageGate{}
	if _, err, recs, _ := imageTurn(t, seer, func() bool { return true }); err != nil || !RequestCarriesImages(seer.requests[0]) || len(recs) != 0 {
		t.Errorf("seeing seat: err=%v images=%v recoveries=%+v", err, RequestCarriesImages(seer.requests[0]), recs)
	}
}
