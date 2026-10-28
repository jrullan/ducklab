package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// B-540's evidence: TI-36X after B-006 was split. Three tasks claim
// test/e2e.py; T-009 fixes the same bug as T-008, the task whose run edited it.
const b540Plan = "## M-01 — Reported bugs\n\n" +
	"### T-002 — Add decimal-entry regressions\n\nFixes B-002.\n\n**Owns:** test/e2e.py, package.json\n\n" +
	"### T-007 — Add neg->ansref to SECOND\n\nFixes B-007.\n\n**Owns:** app.js, test, package.json\n\n" +
	"### T-008 — Map shifted dot to comma\n\nFixes B-006.\n\n**Owns:** app.js\n\n" +
	"### T-009 — Cover shifted-dot comma entry\n\nFixes B-006.\n\n**Owns:** test/e2e.py, package.json\n"

func TestALaneFindingNamesEveryOwnerSameBugFirstAndTheSameEveryTime(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: b540Plan})

	const want = "edit is outside T-008's declared write lane (owned by T-009, which also fixes B-006; T-002; T-007)"
	for attempt := 0; attempt < 30; attempt++ {
		findings := taskLaneFindings(dir, "T-008", []string{"test/e2e.py"})
		if len(findings) != 1 {
			t.Fatalf("findings = %#v", findings)
		}
		if findings[0].Issue != want {
			t.Fatalf("attempt %d: issue = %q, want %q", attempt, findings[0].Issue, want)
		}
	}
	if got := taskLaneFindings(dir, "T-008", []string{"README.md"}); len(got) != 1 || !strings.Contains(got[0].Issue, "owned by no task") {
		t.Errorf("unowned path finding = %#v", got)
	}
}

// Who may approve, and how an agent asks, is said wherever the refusal lands.
func TestALaneRefusalTellsAnAgentToFileAnApprovalRequest(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: b540Plan})
	findings := taskLaneFindings(dir, "T-008", []string{"test/e2e.py"})
	if len(findings) != 1 {
		t.Fatalf("findings = %#v", findings)
	}
	for name, text := range map[string]string{"finding fix": findings[0].Fix, "accept detail": laneRefusalDetail(1, "T-008")} {
		for _, want := range []string{"the person approves", "operator agent cannot", "approval_request", "revert"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not say %q: %s", name, want, text)
			}
		}
	}
}

func TestTaskFixedBugsReadsThePromotionLine(t *testing.T) {
	got := taskFixedBugs("Fixes B-006, B-010\n\n## Current portion contract (authoritative)\n\nFixes B-006.\n\n## Reported\n\nFixes B-002.\n")
	if strings.Join(got, ",") != "B-006,B-010" {
		t.Errorf("taskFixedBugs = %v, want the task's own lines and not the reporter's", got)
	}
}

func TestALongOwnerListIsBounded(t *testing.T) {
	var owners []laneOwner
	for i := 1; i <= 9; i++ {
		owners = append(owners, laneOwner{id: fmt.Sprintf("T-%03d", i), claims: []laneClaim{{path: "src", tree: true}}})
	}
	got := laneOwnersOf(owners, "T-001", nil, "src/a.go")
	if got != "T-002; T-003; T-004; T-005; T-006; and 3 more" {
		t.Errorf("owners = %q", got)
	}
}
