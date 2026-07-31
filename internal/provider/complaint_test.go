package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A run died with "invalid response: no choices" — a message naming our own
// parser rather than the cause, which sends the reader looking in the wrong
// program. OpenRouter answers 200 with an `error` object, and the reason was in
// the body we had just read and thrown away.
func TestAResponseWithNoChoicesSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{
			"openrouter's shape",
			`{"error":{"message":"Rate limit exceeded: free-models-per-day","code":429}}`,
			"Rate limit exceeded",
		},
		{
			"with the upstream's own words underneath",
			`{"error":{"message":"Provider returned error","code":400,` +
				`"metadata":{"raw":"context length 200000 exceeded"}}}`,
			"context length 200000 exceeded",
		},
		{"a bare string", `{"error":"insufficient credits"}`, "insufficient credits"},
		{"no error object at all", `{"id":"x","choices":[]}`, "the server said"},
		{"nothing at all", ``, "empty body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			_, err := NewOpenAICompat("t", srv.URL, "").Chat(context.Background(),
				ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "go"}}})
			if err == nil {
				t.Fatal("a response with no choices was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A provider that returns a novel of an error must not put the whole thing in a
// run's failure banner.
func TestAComplaintIsBounded(t *testing.T) {
	long := strings.Repeat("x", 5000)
	got := providerComplaint([]byte(`{"error":{"message":"` + long + `"}}`))
	if len(got) > 500 {
		t.Errorf("complaint is %d bytes", len(got))
	}
}
