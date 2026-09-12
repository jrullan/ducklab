package service

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/capability"
)

func TestBuildVerdictRequiresEveryAvailableSignal(t *testing.T) {
	for _, tc := range []struct {
		name, project, task string
		dissent             bool
		want                string
	}{
		{"all green", "PASSED", "green", false, "PASSED"},
		{"no task contract", "PASSED", "none", false, "PASSED"},
		{"task red", "PASSED", "red", false, "FAILED"},
		{"reviewer dissent", "PASSED", "green", true, "FAILED"},
		{"project red stays red", "FAILED", "green", false, "FAILED"},
		{"unverified stays unverified", "UNVERIFIED", "none", false, "UNVERIFIED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := adjudicateBuildVerdict(tc.project, tc.task, tc.dissent); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestFinalGateAppliesRequiredCapabilityCoverage(t *testing.T) {
	findings := []capability.GateFinding{{
		Capability: "meson", Kind: "build-integration", Enforcement: capability.Required,
		Detail: "source is not in the build graph", Files: []string{"src/new.c"},
	}}
	gate, exit, output := applyFinalCapabilityCoverage("green", 0, "project gate green", findings)
	if gate != "red" || exit != 1 || !strings.Contains(output, "meson/build-integration, required") || !strings.Contains(output, "src/new.c") {
		t.Fatalf("final coverage = gate %q exit %d output %q", gate, exit, output)
	}
}

func TestFinalGateKeepsDiagnosticCapabilityCoverageNonBlocking(t *testing.T) {
	findings := []capability.GateFinding{{
		Capability: "meson", Kind: "build-integration", Enforcement: capability.Diagnostic,
		Detail: "inherited source is not in the build graph",
	}}
	gate, exit, output := applyFinalCapabilityCoverage("green", 0, "project gate green", findings)
	if gate != "green" || exit != 0 || !strings.Contains(output, "meson/build-integration, diagnostic") {
		t.Fatalf("final diagnostic coverage = gate %q exit %d output %q", gate, exit, output)
	}
}
