package service

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// The plan declares its toolchain per milestone; the task's own milestone is
// consulted first, and a missing binary is named for the person.
func TestDeclaredToolchainComesFromTheTasksMilestone(t *testing.T) {
	plan, err := artifact.Parse("## M-01 — Build\n\n**Toolchain:** meson, ninja, pkg-config\n\n### T-001 — Scaffold\n\n**Implements:** SPEC-001\n\nBody.\n\n## M-02 — Tests\n\n**Toolchain:** pytest\n\n### T-002 — Tests\n\n**Implements:** SPEC-002\n\nBody.\n", artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(declaredToolchain(plan, "T-001"), ","); got != "meson,ninja,pkg-config" {
		t.Fatalf("T-001 toolchain = %q", got)
	}
	if got := strings.Join(declaredToolchain(plan, "T-002"), ","); got != "pytest" {
		t.Fatalf("T-002 toolchain = %q", got)
	}
	// A tool that is surely absent is reported; one that is surely present is not.
	missing := missingTools([]string{"sh", "definitely-not-a-binary-xyz (apt: nothing)"})
	if len(missing) != 1 || !strings.HasPrefix(missing[0], "definitely-not-a-binary-xyz") {
		t.Fatalf("missing = %v", missing)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	q := toolchainQuestion("T-001", missing)
	if !strings.Contains(q.Question, "definitely-not-a-binary-xyz") || len(q.Options) != 2 {
		t.Fatalf("question = %+v", q)
	}
}

func TestToolchainCapabilitiesIncludePkgConfigModules(t *testing.T) {
	declared := []string{
		"cmd:sh",
		"cmd:definitely-not-a-command-xyz",
		"pkg-config:definitely-not-a-module-xyz>=99.0",
	}
	missing := missingTools(declared)
	got := strings.Join(missing, ",")
	if !strings.Contains(got, "cmd:definitely-not-a-command-xyz") {
		t.Fatalf("missing capabilities = %q; absent command was not reported", got)
	}
	if !strings.Contains(got, "pkg-config:definitely-not-a-module-xyz>=99.0") {
		t.Fatalf("missing capabilities = %q; absent pkg-config module was not reported", got)
	}
	if strings.Contains(got, "cmd:sh") {
		t.Fatalf("missing capabilities = %q; available command was reported missing", got)
	}
}
