package engineapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The whole point of the route table: the mux and the document are built from
// one source, so an endpoint cannot exist in one and not the other.
func TestEveryTableRouteIsRegisteredOnTheMux(t *testing.T) {
	srv := &Server{mux: http.NewServeMux(), token: "t", version: "0.3.0"}
	srv.routes()

	for _, r := range routeTable() {
		req := httptest.NewRequest(r.Method, concrete(r.Path), nil)
		_, pattern := srv.mux.Handler(req)
		if pattern == "" {
			t.Errorf("%s %s is in the table but matched no mux pattern", r.Method, r.Path)
		}
	}
}

func TestOpenAPIDocumentsEveryTableRoute(t *testing.T) {
	doc := BuildOpenAPI("0.3.0")
	paths, _ := doc["paths"].(map[string]any)

	for _, r := range routeTable() {
		item, ok := paths[r.Path].(map[string]any)
		if !ok {
			t.Errorf("%s is missing from the document", r.Path)
			continue
		}
		if _, ok := item[strings.ToLower(r.Method)]; !ok {
			t.Errorf("%s %s is missing from the document", r.Method, r.Path)
		}
	}
}

func TestOnlyHealthIsUnauthenticated(t *testing.T) {
	for _, r := range routeTable() {
		if r.Path == "/v1/health" {
			if r.Auth {
				t.Error("/v1/health requires auth; a client cannot reach it before reading the token file")
			}
			continue
		}
		if !r.Auth {
			t.Errorf("%s %s is unauthenticated", r.Method, r.Path)
		}
	}
}

// The schema must describe what the handler encodes. Reflecting the DTO is what
// guarantees that; this checks the reflection produces something usable.
func TestSchemasAreReflectedFromDTOs(t *testing.T) {
	doc := BuildOpenAPI("0.3.0")
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)

	run, ok := schemas["RunlogRun"].(map[string]any)
	if !ok {
		t.Fatalf("runlog.Run was not reflected; schemas: %v", keys(schemas))
	}
	props, _ := run["properties"].(map[string]any)
	for _, field := range []string{"id", "status", "verdict", "mode", "task_id"} {
		if _, ok := props[field]; !ok {
			t.Errorf("Run schema is missing %q", field)
		}
	}
}

// time.Time is a struct in Go but a string on the wire; documenting its fields
// would describe a shape no client ever sees.
func TestTimeIsDocumentedAsAString(t *testing.T) {
	doc := BuildOpenAPI("0.3.0")
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), `"TimeTime"`) {
		t.Error("time.Time was reflected as a struct")
	}
	if !strings.Contains(string(raw), `"date-time"`) {
		t.Error("no date-time format emitted; timestamps are untyped")
	}
}

// I7 reaches the generated clients: the candidates schema has nowhere to put
// authorship, so no client can render it.
func TestCandidateSchemaHasNoAuthorship(t *testing.T) {
	doc := BuildOpenAPI("0.3.0")
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)

	cand, ok := schemas["ServiceCandidateView"].(map[string]any)
	if !ok {
		t.Fatalf("CandidateView not in schemas: %v", keys(schemas))
	}
	props, _ := cand["properties"].(map[string]any)
	for _, forbidden := range []string{"duckling", "author", "provider", "model"} {
		if _, present := props[forbidden]; present {
			t.Errorf("candidate schema exposes %q", forbidden)
		}
	}
}

func TestHealthReportsInstalledBinaryProvenance(t *testing.T) {
	srv := &Server{mux: http.NewServeMux(), token: "t", version: "0.4.0"}
	srv.routes()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/engine", nil)
	req.Header.Set("Authorization", "Bearer t")
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"branch", "commit"} {
		if strings.TrimSpace(fmt.Sprint(got[key])) == "" {
			t.Errorf("health.%s is missing installed provenance: %#v", key, got[key])
		}
	}
}

func TestOpenAPIIsServed(t *testing.T) {
	srv := &Server{mux: http.NewServeMux(), token: "t", version: "0.3.0"}
	srv.routes()

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("served document is not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", doc["openapi"])
	}
}

// Generation must be deterministic, or api-check reports drift on every run.
func TestDocumentIsDeterministic(t *testing.T) {
	first, _ := json.Marshal(BuildOpenAPI("0.3.0"))
	for i := 0; i < 20; i++ {
		again, _ := json.Marshal(BuildOpenAPI("0.3.0"))
		if string(again) != string(first) {
			t.Fatal("BuildOpenAPI is not deterministic; api-check would flap")
		}
	}
}

func concrete(path string) string {
	out := []string{}
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, "{") {
			out = append(out, "x")
		} else {
			out = append(out, seg)
		}
	}
	return strings.Join(out, "/")
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
