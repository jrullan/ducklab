package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Walk the engine's route table rather than maintaining a second list here.
func TestFakeAnswersFrontendRoutes(t *testing.T) {
	table, err := os.ReadFile("../../internal/engineapi/routes_table.go")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := os.ReadFile("../../frontend/src/api/client.ts")
	if err != nil {
		t.Fatal(err)
	}

	mentioned := map[string]bool{}
	clientRoute := regexp.MustCompile("\\\"(GET|POST|PUT|DELETE|PATCH)\\\",\\s*\\n?\\s*[`\\\"](/v1[^`\\\"?]*)")
	placeholder := regexp.MustCompile(`\$\{[^}]+\}`)
	for _, m := range clientRoute.FindAllStringSubmatch(string(frontend), -1) {
		p := placeholder.ReplaceAllString(m[2], "{}")
		mentioned[m[1]+" "+strings.TrimRight(p, "/")] = true
	}

	// CLI/transport routes are intentionally outside the desktop contract.
	notInTheDesktop := map[string]string{
		"GET /v1/health":                           "desktop uses event-stream connection state",
		"GET /v1/events":                           "EventSource builds this URL directly",
		"GET /v1/openapi.json":                     "the document is for tooling",
		"GET /v1/engine":                           "engine metadata is read by the Go shell",
		"GET /v1/projects/{id}/doctor":             "configuration diagnostics are outside the fake playback contract",
		"GET /v1/projects/{id}/diagnostics":        "host diagnostics are outside the fake playback contract",
		"GET /v1/ducklings/scorecards":             "scorecards are outside the fake playback contract",
		"GET /v1/defaults/candidates":              "candidate criteria are outside the fake playback contract",
		"GET /v1/defaults/roster":                  "global roster is outside the fake playback contract",
		"GET /v1/projects/{id}/skills/{name}":      "skill detail is outside the fake playback contract",
		"GET /v1/bench":                            "bench history is outside the fake playback contract",
		"GET /v1/bench/{suite}/{stamp}":            "bench detail is outside the fake playback contract",
		"GET /v1/runs/{id}/captures/{name}":        "binary captures are outside the JSON fake playback contract",
		"GET /v1/runs/{id}/brief":                  "run brief is outside the fake playback contract",
		"GET /v1/runs/{id}/llm":                    "redacted model calls are outside the fake playback contract",
		"GET /v1/projects/{id}/bugs/{bug}":         "bug detail is outside the fake playback contract",
		"GET /v1/projects/{id}/reviews":            "reviews are outside the fake playback contract",
		"GET /v1/projects/{id}/reviews/{task}":     "review detail is outside the fake playback contract",
		"GET /v1/projects/{id}/releases":           "releases are outside the fake playback contract",
		"GET /v1/projects/{id}/releases/{version}": "release detail is outside the fake playback contract",
		"GET /v1/projects/{id}/artifacts/{kind}":   "artifact detail is outside the fake playback contract",
		"GET /v1/projects/{id}/next":               "next-action projection is outside the fake playback contract",
		"GET /v1/projects/{id}/tasks/next":         "next-task projection is outside the fake playback contract",
		"GET /v1/projects/{id}/trace/{anyID}":      "trace detail is outside the fake playback contract",
		"GET /v1/projects/{id}/report":             "report projection is outside the fake playback contract",
		"GET /v1/projects/{id}/trace/check":        "trace checking is outside the fake playback contract",
		"GET /v1/projects/{id}/trace/report":       "trace reporting is outside the fake playback contract",
	}
	for key, reason := range notInTheDesktop {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s has an empty skip reason", key)
		}
	}

	f := newFakeEngine("test-token", "idle", 0, "*")
	server := httptest.NewServer(f)
	defer server.Close()
	client := server.Client()

	routeRE := regexp.MustCompile(`\{Method: "GET", Path: "(/v1/[^"]+)"`)
	routes := routeRE.FindAllStringSubmatch(string(table), -1)
	if len(routes) == 0 {
		t.Fatal("no GET routes found in routes_table.go")
	}
	pathPlaceholder := regexp.MustCompile(`\{[^}]+\}`)
	for _, m := range routes {
		template := m[1]
		key := "GET " + strings.TrimRight(pathPlaceholder.ReplaceAllString(template, "{}"), "/")
		if _, skipped := notInTheDesktop["GET "+template]; skipped || !mentioned[key] {
			continue
		}
		route := pathPlaceholder.ReplaceAllString(template, "demo")
		if strings.HasSuffix(route, "/tasks") || strings.HasSuffix(route, "/bugs") {
			route += "?summary=true"
		}
		t.Run(template, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, server.URL+route, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer test-token")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				t.Fatalf("fake engine returned %s for %s", resp.Status, route)
			}
			var shape map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&shape); err != nil {
				t.Fatalf("%s returned invalid JSON: %v", route, err)
			}
			if shape == nil {
				t.Fatalf("%s returned a non-object JSON shape", route)
			}
		})
	}
}
