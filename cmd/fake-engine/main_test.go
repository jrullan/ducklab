package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// These are the GET routes used by the frontend's documented development flow.
// Keep this list alongside the fake: adding a frontend route without teaching
// the double to answer it must fail here, rather than in a browser.
func TestFakeAnswersFrontendRoutes(t *testing.T) {
	f := newFakeEngine("test-token", "idle", 0, "*")
	server := httptest.NewServer(f)
	defer server.Close()

	routes := []string{
		"/v1/projects/demo/tasks?summary=true",
		"/v1/projects/demo/bugs?summary=true",
		"/v1/providers",
		"/v1/defaults/budget",
		"/v1/projects/demo/roster?mode=tournament",
		"/v1/projects/demo/skills",
	}
	client := server.Client()
	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, server.URL+route, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer test-token")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("fake engine returned %s", resp.Status)
			}
		})
	}
	// Avoid making the test depend on the scripted playback timeout while still
	// allowing the constructor's goroutine to observe its idle scenario.
	time.Sleep(1 * time.Millisecond)
}
