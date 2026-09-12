package engineapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSDevelopmentOriginIsOptIn(t *testing.T) {
	origin := "http://localhost:5173"
	for _, tc := range []struct {
		name  string
		allow string
		want  string
	}{
		{"default", "", ""},
		{"opt-in", origin, origin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{allowOrigin: tc.allow, mux: http.NewServeMux()}
			req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
			req.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			s.setCORS(w, req)
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != tc.want {
				t.Fatalf("allow-origin = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCORSDevelopmentOriginPreflight(t *testing.T) {
	origin := "http://localhost:5173"
	s := &Server{allowOrigin: origin, mux: http.NewServeMux()}
	req := httptest.NewRequest(http.MethodOptions, "/v1/health", nil)
	req.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("preflight = %d, allow-origin %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSRejectsUnconfiguredDevelopmentOrigin(t *testing.T) {
	s := &Server{mux: http.NewServeMux()}
	req := httptest.NewRequest(http.MethodOptions, "/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("preflight status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// The desktop's project config edits are the one PATCH in the client. A
// hand-written method list left PATCH out, the webview refused the request
// after preflight, and the thrown fetch was reported as an engine restart
// (B-399). The list is derived from the routes table so a new method can
// never be forgotten again.
func TestCORSPreflightAllowsEveryRoutedMethod(t *testing.T) {
	seen := map[string]bool{}
	for _, rt := range routeTable() {
		seen[rt.Method] = true
	}
	if !seen[http.MethodPatch] {
		t.Fatal("the routes table no longer registers a PATCH route; this test guards the desktop's project edits")
	}
	for _, origin := range []string{"wails://localhost", "wails://wails", "http://wails.localhost"} {
		for method := range seen {
			s := &Server{mux: http.NewServeMux()}
			req := httptest.NewRequest(http.MethodOptions, "/v1/projects/p", nil)
			req.Header.Set("Origin", origin)
			req.Header.Set("Access-Control-Request-Method", method)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)
			allowed := w.Header().Get("Access-Control-Allow-Methods")
			if w.Code != http.StatusNoContent || !strings.Contains(allowed, method) {
				t.Errorf("origin %s: preflight for %s = %d, allow-methods %q", origin, method, w.Code, allowed)
			}
		}
	}
}
