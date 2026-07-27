package engineclt

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func respWith(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// The engine's message is the only part a user can act on, so it must survive
// intact rather than arriving wrapped in the URL and the raw JSON.
func TestHTTPErrorUnwrapsTheEngineMessage(t *testing.T) {
	err := httpError("PATCH", "/v1/projects/x",
		respWith(400, `{"error":{"code":"bad_request","message":"unknown key \"verify.timout_s\""}}`))

	if got := err.Error(); got != `unknown key "verify.timout_s"` {
		t.Errorf("message not unwrapped: %q", got)
	}
	api, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T", err)
	}
	if api.Code != "bad_request" || api.Status != 400 {
		t.Errorf("lost code or status: %+v", api)
	}
}

// A body that is not the engine's envelope (a proxy's HTML, a truncated
// response) must still say what failed rather than collapsing to "".
func TestHTTPErrorKeepsContextWhenTheBodyIsNotOurs(t *testing.T) {
	err := httpError("GET", "/v1/health", respWith(502, "<html>bad gateway</html>"))
	msg := err.Error()
	for _, want := range []string{"GET", "/v1/health", "bad gateway"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q is missing %q", msg, want)
		}
	}
}
