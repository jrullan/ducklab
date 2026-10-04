package engineapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/service"
)

// Review of #147: POST /answer dropped the actor whenever the answer also
// widened a lane, so an operator's approval went through as a person's. The
// actor must reach the lane-aware path, which refuses a non-human decider
// before it looks at the run at all.
func TestALaneWideningAnswerCarriesItsActor(t *testing.T) {
	s, err := service.New(config.DefaultGlobal(), service.Options{
		Bus:        bus.New(16),
		ConfigPath: filepath.Join(t.TempDir(), "config.toml"),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(s, bus.New(16), "token", "test", ""))
	t.Cleanup(server.Close)
	body := `{"question_id":"oracle-tests/parser.test.mjs:abc","answer":"The test is wrong — let the implementer correct it","widen_lane":["tests/parser.test.mjs"],"actor":"mcp:elena"}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/runs/r-none/answer", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNoContent || !strings.Contains(string(out), "a person must approve it") || !strings.Contains(string(out), "mcp:elena") {
		t.Fatalf("the actor did not reach the lane-aware answer: %d %s", resp.StatusCode, out)
	}
}
