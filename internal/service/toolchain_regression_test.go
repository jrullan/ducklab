package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
)

func TestToolchainRecheckReportsEquivalentCommand(t *testing.T) {
	missing := []string{"cmd:python"}
	q := toolchainQuestion("T-001", missing, true)
	if !strings.Contains(q.Question, "re-checked after your answer") || !strings.Contains(q.Question, "python") {
		t.Fatalf("recheck question = %q", q.Question)
	}
	if equivalentCommand("cmd:python") == "python3" && !strings.Contains(q.Question, "python3") {
		t.Fatalf("available equivalent was not surfaced: %q", q.Question)
	}
	if strings.Contains(q.Question, "python-is-python3") {
		t.Fatalf("question gave distro-specific installation advice: %q", q.Question)
	}
}

func TestPlanToolchainMismatchIsReportedOnceWithConfiguredEquivalent(t *testing.T) {
	if equivalentCommand("cmd:python") != "python3" {
		t.Skip("python3 is not available on this host")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ducklab"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultProject("p", "P")
	cfg.Run.Command = "python3 -m http.server"
	if err := config.SaveProject(filepath.Join(root, ".ducklab", "project.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	plan, err := artifact.Parse("## M-01 — Build\n\n**Toolchain:** cmd:python\n\n### T-001 — Build\n", artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	gotFindings := capabilityStructureFindings(root, plan)
	findings := strings.Join(gotFindings, "\n")
	if len(gotFindings) != 1 {
		t.Fatalf("toolchain findings = %d, want one:\n%s", len(gotFindings), findings)
	}
	if strings.Contains(findings, "python-is-python3") {
		t.Fatalf("finding gave distro-specific installation advice:\n%s", findings)
	}
}

func TestToolchainPlanRevisionOptionIsExact(t *testing.T) {
	if !toolchainPlanRevisionAnswer("Change the plan (revise it) instead") {
		t.Fatal("plan revision option was not recognized")
	}
	if toolchainPlanRevisionAnswer("Installed — continue. Do not revise.") {
		t.Fatal("non-option answer incorrectly requested plan revision")
	}
}

func TestPlanToolchainMismatchFindsPython3(t *testing.T) {
	if equivalentCommand("cmd:python") != "python3" {
		t.Skip("python3 is not available on this host")
	}
	plan, err := artifact.Parse("## M-01 — Build\n\n**Toolchain:** cmd:python\n\n### T-001 — Build\n", artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	findings := strings.Join(capabilityStructureFindings("", plan), "\n")
	if !strings.Contains(findings, "python3") || !strings.Contains(findings, "cmd:python") {
		t.Fatalf("toolchain findings = %q", findings)
	}
}
