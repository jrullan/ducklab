package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jrullan/ducklab/internal/daemon"
)

// B-484's real offer: r-20261002-184627-suwe asked to amend T-290's Owns.
var b484Offer = []interface{}{"frontend/src/store/runs.test.ts", "frontend/src/store/runs.ts"}

// laneEngine is a fake engine holding one paused run. It records every POST
// body so a test can assert what reached the wire, and serves the event
// stream a resumed run ends on so the CLI's follow returns.
type laneEngine struct {
	mu        sync.Mutex
	run       map[string]interface{}
	events    []interface{}
	posts     map[string]map[string]interface{}
	acceptErr string
}

func (e *laneEngine) posted(path string) (map[string]interface{}, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	body, ok := e.posts[path]
	return body, ok
}

func startLaneEngine(t *testing.T, e *laneEngine) {
	t.Helper()
	e.posts = map[string]map[string]interface{}{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := str(e.run["id"])
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/"+id:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"run": e.run, "events": e.events})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, ev("run_end", `{"verdict":"PASSED"}`))
		case r.Method == http.MethodPost:
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			e.mu.Lock()
			e.posts[r.URL.Path] = body
			e.mu.Unlock()
			if r.URL.Path == "/v1/runs/"+id+"/accept" {
				if e.acceptErr != "" {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":{"message":"` + e.acceptErr + `"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"commit_sha":"acc0123"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected engine request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	enginePath, err := daemon.EngineJSONPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(enginePath), 0o755); err != nil {
		t.Fatal(err)
	}
	engine, _ := json.Marshal(daemon.EngineInfo{Port: port, Token: "test"})
	if err := os.WriteFile(enginePath, engine, 0o600); err != nil {
		t.Fatal(err)
	}
}

// captureRun runs a `ducklab run` verb and returns its exit code, stdout and
// stderr.
func captureRun(t *testing.T, verb string, args ...string) (int, string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	os.Stdout, os.Stderr = outW, errW
	var out, errOut bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&out, outR) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&errOut, errR) }()
	code := runCmd(verb, args, "")
	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	wg.Wait()
	return code, out.String(), errOut.String()
}

func questionRun(offer []interface{}) map[string]interface{} {
	pending := map[string]interface{}{
		"question_id": "q-lane",
		"question":    "T-290 must edit runs.ts; amend its Owns?",
		"options":     []interface{}{"Amend T-290 Owns to include frontend/src/store/runs.ts and frontend/src/store/runs.test.ts", "Keep the lane"},
	}
	if offer != nil {
		pending["lane_widening"] = offer
	}
	return map[string]interface{}{
		"id": "r-20261002-184627-suwe", "status": "paused", "task_id": "T-290",
		"pending_kind": "question", "pending_data": pending,
	}
}

// The real case: the person answers the offered amendment with a bare
// --widen-lane. The answer must carry exactly the engine's offered paths in
// widen_lane — the field that routes the engine to RunAnswerWithLaneAs
// (engineapi TestALaneWideningAnswerCarriesItsActor) — and no actor, so the
// engine records a person's approval.
func TestRunAnswerWidenLaneSendsExactlyTheOfferedPaths(t *testing.T) {
	e := &laneEngine{run: questionRun(b484Offer), events: []interface{}{
		map[string]interface{}{"seq": float64(9), "type": "lane_widened", "data": map[string]interface{}{"commit_sha": "abc1234", "paths": b484Offer}},
	}}
	startLaneEngine(t, e)
	code, out, errOut := captureRun(t, "answer", "r-20261002-184627-suwe",
		"--answer", "Amend T-290 Owns to include frontend/src/store/runs.ts and frontend/src/store/runs.test.ts", "--widen-lane")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	body, ok := e.posted("/v1/runs/r-20261002-184627-suwe/answer")
	if !ok {
		t.Fatal("the answer never reached the engine")
	}
	if got := asStrings(body["widen_lane"]); !slices.Equal(got, []string{"frontend/src/store/runs.test.ts", "frontend/src/store/runs.ts"}) {
		t.Errorf("widen_lane = %v, want exactly the offered paths", got)
	}
	if _, hasActor := body["actor"]; hasActor {
		t.Errorf("a person's lane approval carried an actor: %v", body["actor"])
	}
	if !strings.Contains(out, "lane widened: frontend/src/store/runs.test.ts, frontend/src/store/runs.ts") || !strings.Contains(out, "abc1234") {
		t.Errorf("the CLI did not report the applied paths and amendment commit:\n%s", out)
	}
}

// --widen-lane path,... approves a subset of the offer, and only that subset.
func TestRunAnswerWidenLaneSendsANamedSubset(t *testing.T) {
	e := &laneEngine{run: questionRun(b484Offer)}
	startLaneEngine(t, e)
	code, _, errOut := captureRun(t, "answer", "r-20261002-184627-suwe", "--answer", "only the store", "--widen-lane", "frontend/src/store/runs.ts")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	body, _ := e.posted("/v1/runs/r-20261002-184627-suwe/answer")
	if got := asStrings(body["widen_lane"]); !slices.Equal(got, []string{"frontend/src/store/runs.ts"}) {
		t.Errorf("widen_lane = %v, want the named subset", got)
	}
}

// A path the engine did not offer fails in the CLI, before anything resumes.
func TestRunAnswerWidenLaneRefusesAnUnofferedPath(t *testing.T) {
	e := &laneEngine{run: questionRun(b484Offer)}
	startLaneEngine(t, e)
	code, _, errOut := captureRun(t, "answer", "r-20261002-184627-suwe", "--answer", "x", "--widen-lane", "internal/service/service.go")
	if code != 2 || !strings.Contains(errOut, "was not offered") {
		t.Fatalf("exit = %d, stderr: %s; want refusal naming the unoffered path", code, errOut)
	}
	if _, ok := e.posted("/v1/runs/r-20261002-184627-suwe/answer"); ok {
		t.Error("an unoffered widening reached the engine")
	}
}

// --widen-lane on a run with no offer is a mistake about which run this is.
func TestRunAnswerWidenLaneRefusesWhenNothingIsOffered(t *testing.T) {
	e := &laneEngine{run: questionRun(nil)}
	startLaneEngine(t, e)
	code, _, errOut := captureRun(t, "answer", "r-20261002-184627-suwe", "--answer", "x", "--widen-lane")
	if code != 2 || !strings.Contains(errOut, "no pending lane widening offer") {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if _, ok := e.posted("/v1/runs/r-20261002-184627-suwe/answer"); ok {
		t.Error("a widening with no offer reached the engine")
	}
}

// B-484 itself: the text "Amend T-290 Owns ..." without a lane decision used
// to resume the run unamended. With an offer pending, the CLI now refuses
// before resuming and names the offered paths and both doors.
func TestRunAnswerRefusesATextOnlyAnswerToALaneOffer(t *testing.T) {
	e := &laneEngine{run: questionRun(b484Offer)}
	startLaneEngine(t, e)
	code, _, errOut := captureRun(t, "answer", "r-20261002-184627-suwe",
		"--answer", "Amend T-290 Owns to include frontend/src/store/runs.ts and frontend/src/store/runs.test.ts")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr: %s", code, errOut)
	}
	for _, want := range []string{"frontend/src/store/runs.test.ts", "frontend/src/store/runs.ts", "--widen-lane", "--keep-lane"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("refusal omitted %q:\n%s", want, errOut)
		}
	}
	if _, ok := e.posted("/v1/runs/r-20261002-184627-suwe/answer"); ok {
		t.Error("a text-only answer to a lane offer resumed the run")
	}
}

// --keep-lane is the explicit decline: the answer goes through unamended.
func TestRunAnswerKeepLaneAnswersWithoutWidening(t *testing.T) {
	e := &laneEngine{run: questionRun(b484Offer)}
	startLaneEngine(t, e)
	code, _, errOut := captureRun(t, "answer", "r-20261002-184627-suwe", "--answer", "Keep the lane", "--keep-lane")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	body, ok := e.posted("/v1/runs/r-20261002-184627-suwe/answer")
	if !ok || body["answer"] != "Keep the lane" {
		t.Fatalf("answer body = %v", body)
	}
	if _, widened := body["widen_lane"]; widened {
		t.Errorf("--keep-lane sent widen_lane: %v", body["widen_lane"])
	}
}

func TestRunAnswerRefusesBothLaneDecisions(t *testing.T) {
	e := &laneEngine{run: questionRun(b484Offer)}
	startLaneEngine(t, e)
	code, _, errOut := captureRun(t, "answer", "r-20261002-184627-suwe", "--answer", "x", "--widen-lane", "--keep-lane")
	if code != 2 || !strings.Contains(errOut, "opposite decisions") {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
}

// A question with no offer answers as before: no flag required, no widen_lane.
func TestRunAnswerWithoutAnOfferIsUnchanged(t *testing.T) {
	e := &laneEngine{run: questionRun(nil)}
	startLaneEngine(t, e)
	code, _, errOut := captureRun(t, "answer", "r-20261002-184627-suwe", "--answer", "yes")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	body, ok := e.posted("/v1/runs/r-20261002-184627-suwe/answer")
	if !ok {
		t.Fatal("the answer never reached the engine")
	}
	if _, widened := body["widen_lane"]; widened {
		t.Errorf("an answer with no offer sent widen_lane: %v", body["widen_lane"])
	}
}

func gateRun(offer []interface{}) map[string]interface{} {
	pending := map[string]interface{}{}
	if offer != nil {
		pending["lane_widening"] = offer
	}
	return map[string]interface{}{
		"id": "r-gate", "status": "paused", "task_id": "T-290",
		"pending_kind": "gate", "pending_data": pending,
	}
}

// Same class at the Accept gate (#143): a refused Accept offers the paths, and
// `run accept --widen-lane` approves exactly those as a person.
func TestRunAcceptWidenLaneSendsExactlyTheOfferedPaths(t *testing.T) {
	e := &laneEngine{run: gateRun(b484Offer)}
	startLaneEngine(t, e)
	code, out, errOut := captureRun(t, "accept", "r-gate", "--widen-lane")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	body, ok := e.posted("/v1/runs/r-gate/accept")
	if !ok {
		t.Fatal("accept never reached the engine")
	}
	if got := asStrings(body["lane_widening"]); !slices.Equal(got, []string{"frontend/src/store/runs.test.ts", "frontend/src/store/runs.ts"}) {
		t.Errorf("lane_widening = %v, want exactly the offered paths", got)
	}
	if _, hasActor := body["actor"]; hasActor {
		t.Errorf("a person's accept carried an actor: %v", body["actor"])
	}
	if !strings.Contains(out, "lane widened:") || !strings.Contains(out, "accepted: commit acc0123") {
		t.Errorf("accept output:\n%s", out)
	}
}

func TestRunAcceptWidenLaneRefusesAnUnofferedPath(t *testing.T) {
	e := &laneEngine{run: gateRun(b484Offer)}
	startLaneEngine(t, e)
	code, _, errOut := captureRun(t, "accept", "r-gate", "--widen-lane", "go.mod")
	if code != 2 || !strings.Contains(errOut, "was not offered") {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if _, ok := e.posted("/v1/runs/r-gate/accept"); ok {
		t.Error("an unoffered widening reached accept")
	}
}

// A plain accept stays a plain accept: no lane_widening on the wire.
func TestRunAcceptWithoutTheFlagSendsNoWidening(t *testing.T) {
	e := &laneEngine{run: gateRun(nil)}
	startLaneEngine(t, e)
	if code, _, errOut := captureRun(t, "accept", "r-gate"); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	body, _ := e.posted("/v1/runs/r-gate/accept")
	if _, widened := body["lane_widening"]; widened {
		t.Errorf("plain accept sent lane_widening: %v", body["lane_widening"])
	}
}

// The refusal that creates the offer is where the person is looking; it names
// the paths and the command that approves them.
func TestRunAcceptRefusalNamesTheLaneOffer(t *testing.T) {
	e := &laneEngine{run: gateRun(b484Offer), acceptErr: "accept refused: 2 edit(s) are outside T-290's declared Produces/Modifies/Owns lane"}
	startLaneEngine(t, e)
	code, _, errOut := captureRun(t, "accept", "r-gate")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	for _, want := range []string{"frontend/src/store/runs.ts", "ducklab run accept r-gate --widen-lane"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("refusal omitted %q:\n%s", want, errOut)
		}
	}
}

// `run show` is the pending-question display: question, options, offer, doors.
func TestRunSummaryShowsTheQuestionAndItsLaneOffer(t *testing.T) {
	var out bytes.Buffer
	printRunSummary(&out, questionRun(b484Offer))
	for _, want := range []string{"question: T-290 must edit runs.ts", "- Keep the lane", "frontend/src/store/runs.test.ts", "--widen-lane", "--keep-lane"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary omitted %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	printRunSummary(&out, gateRun(b484Offer))
	if !strings.Contains(out.String(), "ducklab run accept r-gate --widen-lane") {
		t.Errorf("gate summary omitted the accept door:\n%s", out.String())
	}
}

// Following a run that pauses on a lane question shows the offer, not only
// "--answer ...", which was the only door B-484's operator saw.
func TestFollowRunQuestionShowsTheLaneOffer(t *testing.T) {
	client := sseServer(t, false, ev("human_needed", `{"kind":"question","lane_widening":["frontend/src/store/runs.ts"]}`))
	oldOut := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	code := followRun(client, "r-1")
	w.Close()
	os.Stdout = oldOut
	out, _ := io.ReadAll(r)
	if code != 7 {
		t.Errorf("exit = %d, want 7 (paused)", code)
	}
	if !strings.Contains(string(out), "frontend/src/store/runs.ts") || !strings.Contains(string(out), "--widen-lane") {
		t.Errorf("follow output omitted the lane offer:\n%s", out)
	}
}
