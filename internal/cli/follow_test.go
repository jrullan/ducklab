package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineclt"
)

// sseServer serves a canned SSE stream. lines are written in order; if hold is
// true the handler then blocks until the request context is cancelled, which
// is what a run still in flight looks like to the client.
func sseServer(t *testing.T, hold bool, lines ...string) *engineclt.Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		for _, l := range lines {
			io.WriteString(w, l)
			if f != nil {
				f.Flush()
			}
		}
		if hold {
			<-r.Context().Done()
		}
	}))
	t.Cleanup(ts.Close)

	var port int
	fmt.Sscanf(strings.TrimPrefix(ts.URL, "http://127.0.0.1:"), "%d", &port)
	return engineclt.New(&daemon.EngineInfo{Port: port, Token: "t"})
}

func ev(typ, data string) string {
	return fmt.Sprintf("event: %s\ndata: {\"type\":%q,\"run_id\":\"r-1\",\"seq\":1,\"data\":%s}\n\n", typ, typ, data)
}

// AC-9: interrupting the CLI detaches; it must not abort the run and must exit 0.
func TestFollowRunDetachesOnInterrupt(t *testing.T) {
	client := sseServer(t, true, ev("turn_start", `{"turn":0,"role":"implementer"}`))

	sigCh := make(chan os.Signal, 1)
	codeCh := make(chan int, 1)
	go func() { codeCh <- followRunWith(context.Background(), sigCh, client, "r-1") }()

	// Let the first event arrive, then interrupt.
	time.Sleep(200 * time.Millisecond)
	sigCh <- os.Interrupt

	select {
	case code := <-codeCh:
		if code != 0 {
			t.Errorf("exit code = %d, want 0 (a detach is not a failure)", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("followRun did not return after an interrupt")
	}
}

// A run that finishes green exits 0.
func TestFollowRunPassedExitsZero(t *testing.T) {
	client := sseServer(t, false,
		ev("gate", `{"gate":"tests","exit":0}`),
		ev("verdict", `{"verdict":"PASSED"}`),
		ev("run_end", `{"verdict":"PASSED"}`),
	)
	if code := followRunWith(context.Background(), make(chan os.Signal), client, "r-1"); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// A red gate exits 5, per 01 §10.
func TestFollowRunFailedExitsFive(t *testing.T) {
	client := sseServer(t, false,
		ev("verdict", `{"verdict":"FAILED"}`),
		ev("run_end", `{"verdict":"FAILED"}`),
	)
	if code := followRunWith(context.Background(), make(chan os.Signal), client, "r-1"); code != 5 {
		t.Errorf("exit code = %d, want 5", code)
	}
}

// Reaching a human gate exits 7 and stops following: the run is waiting, not
// finished, and the CLI must hand control back rather than hang forever.
func TestFollowRunHumanGateExitsSeven(t *testing.T) {
	client := sseServer(t, true,
		ev("verdict", `{"verdict":"PASSED"}`),
		ev("human_needed", `{"kind":"gate","verdict":"PASSED"}`),
	)
	done := make(chan int, 1)
	go func() {
		done <- followRunWith(context.Background(), make(chan os.Signal), client, "r-1")
	}()
	select {
	case code := <-done:
		if code != 7 {
			t.Errorf("exit code = %d, want 7", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("followRun did not return at the human gate")
	}
}

// An engine-side pause (shutdown or restart) also returns control.
func TestFollowRunPauseCheckpointReturns(t *testing.T) {
	client := sseServer(t, true,
		ev("checkpoint", `{"reason":"engine_shutdown","status":"paused"}`),
	)
	done := make(chan int, 1)
	go func() {
		done <- followRunWith(context.Background(), make(chan os.Signal), client, "r-1")
	}()
	select {
	case code := <-done:
		if code != 7 {
			t.Errorf("exit code = %d, want 7", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("followRun did not return on a pause checkpoint")
	}
}

func TestFollowRunBudgetExceededExitsSix(t *testing.T) {
	client := sseServer(t, false,
		ev("verdict", `{"verdict":"BUDGET_EXCEEDED"}`),
		ev("run_end", `{"verdict":"BUDGET_EXCEEDED"}`),
	)
	if code := followRunWith(context.Background(), make(chan os.Signal), client, "r-1"); code != 6 {
		t.Errorf("exit code = %d, want 6", code)
	}
}

func TestLatestRunEventSeqFindsTail(t *testing.T) {
	events := []interface{}{
		map[string]interface{}{"seq": float64(4)},
		map[string]interface{}{"seq": float64(12)},
		map[string]interface{}{"seq": float64(7)},
	}
	if got := latestRunEventSeq(events); got != 12 {
		t.Fatalf("latest seq = %d, want 12", got)
	}
}
