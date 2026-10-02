package service

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
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
