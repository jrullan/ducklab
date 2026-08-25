package engineapi

import (
	"net/http"
	"net/http/httptest"
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
