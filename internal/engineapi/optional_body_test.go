package engineapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/service"
)

// Review of #155: the document marked every request body required, so
// POST /resume — valid with no body since before it took a note — read as
// demanding one, and a generated client or validator would have broken the
// older call. A route whose handler accepts no body says so, and the document
// carries it.
func TestOptionalRequestBodiesAreDocumentedAsOptional(t *testing.T) {
	doc := BuildOpenAPI("0.3.0")
	paths, _ := doc["paths"].(map[string]any)
	var optional []string
	for _, r := range routeTable() {
		if r.Request == nil {
			continue
		}
		op, _ := paths[r.Path].(map[string]any)[strings.ToLower(r.Method)].(map[string]any)
		body, _ := op["requestBody"].(map[string]any)
		required, ok := body["required"].(bool)
		if !ok {
			t.Errorf("%s %s: requestBody.required is missing", r.Method, r.Path)
			continue
		}
		if required == r.RequestOptional {
			t.Errorf("%s %s: requestBody.required = %v, table says optional = %v", r.Method, r.Path, required, r.RequestOptional)
		}
		if !required {
			optional = append(optional, r.Method+" "+r.Path)
		}
	}
	sort.Strings(optional)
	want := []string{
		"POST /v1/projects/{id}/gate",
		"POST /v1/projects/{id}/skills/{name}/run",
		"POST /v1/projects/{id}/stages/{stage}",
		"POST /v1/runs/{id}/accept",
		"POST /v1/runs/{id}/reject",
		"POST /v1/runs/{id}/resume",
	}
	if !slices.Equal(optional, want) {
		t.Errorf("optional bodies = %v, want %v", optional, want)
	}

	// And the committed artifact clients are generated from says the same.
	raw, err := os.ReadFile("../../docs/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var committed struct {
		Paths map[string]map[string]struct {
			RequestBody struct {
				Required *bool `json:"required"`
			} `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &committed); err != nil {
		t.Fatal(err)
	}
	if req := committed.Paths["/v1/runs/{id}/resume"]["post"].RequestBody.Required; req == nil || *req {
		t.Errorf("docs/openapi.json: POST /v1/runs/{id}/resume requestBody.required = %v, want false", req)
	}
}

// The table's claim is checked against the handlers themselves: sent no body,
// a route documented as optional gets past decoding, and a route documented
// as requiring one refuses the call. Bench, bench/start and tests skip
// decoding an empty body but always refuse it in validation (no ducklings,
// no task), so their bodies are required in practice and documented so.
func TestTheTableMatchesWhichHandlersAcceptNoBody(t *testing.T) {
	root := t.TempDir()
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, filepath.Join(root, k))
	}
	svc, err := service.New(config.DefaultGlobal(), service.Options{Bus: bus.New(16), ConfigPath: filepath.Join(root, "config.toml")})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(svc, bus.New(16), "token", "test", ""))
	t.Cleanup(server.Close)

	for _, r := range routeTable() {
		if r.Request == nil {
			continue
		}
		req, err := http.NewRequest(r.Method, server.URL+concrete(r.Path), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if r.RequestOptional && resp.StatusCode == http.StatusBadRequest && strings.Contains(string(out), "EOF") {
			t.Errorf("%s %s is documented with an optional body but refuses none: %s", r.Method, r.Path, out)
		}
		if !r.RequestOptional && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s %s is documented with a required body but accepts none (%d %s) — mark it RequestOptional",
				r.Method, r.Path, resp.StatusCode, strings.TrimSpace(string(out)))
		}
	}
}
