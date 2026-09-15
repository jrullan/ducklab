package service

import (
	"context"
	"strings"
	"testing"
)

func TestHarnessDiagnosticProjectIsAValidatedRegisteredProject(t *testing.T) {
	s := writableService(t, "consultant")
	id, _ := projectWithConfig(t, s, "Ducklab source")

	if err := s.DiagnosticDefaultsSet(context.Background(), DiagnosticDefaultsView{HarnessProjectID: id}); err != nil {
		t.Fatal(err)
	}
	got := s.DiagnosticDefaults(context.Background())
	if got.HarnessProjectID != id || got.HarnessProjectName != "Ducklab source" || !got.Available {
		t.Fatalf("diagnostic defaults = %+v", got)
	}

	if err := s.DiagnosticDefaultsSet(context.Background(), DiagnosticDefaultsView{HarnessProjectID: "invented-path"}); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unregistered harness project error = %v", err)
	}
	if got := s.DiagnosticDefaults(context.Background()).HarnessProjectID; got != id {
		t.Fatalf("rejected update changed harness project to %q", got)
	}
}

func TestHarnessDiagnosticProjectCanBeCleared(t *testing.T) {
	s := writableService(t, "consultant")
	id, _ := projectWithConfig(t, s, "Ducklab source")
	if err := s.DiagnosticDefaultsSet(context.Background(), DiagnosticDefaultsView{HarnessProjectID: id}); err != nil {
		t.Fatal(err)
	}
	if err := s.DiagnosticDefaultsSet(context.Background(), DiagnosticDefaultsView{}); err != nil {
		t.Fatal(err)
	}
	if got := s.DiagnosticDefaults(context.Background()); got.HarnessProjectID != "" || got.Available {
		t.Fatalf("cleared diagnostics = %+v", got)
	}
}
