package service

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

func TestAmendmentCoverageRequiresEveryNumberedBehavior(t *testing.T) {
	request := "1) No keyboard-only operation is required 2) Saving functionality shall be implemented 3) Ubuntu newer versions use Wayland"
	doc, err := artifact.Parse("## REQ-001 — Input\n\nKeyboard-only operation is optional.\n\n## REQ-002 — Platform\n\nUbuntu uses Wayland.", artifact.KindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	findings := amendmentCoverageFindings(request, doc)
	if len(findings) != 1 || !strings.Contains(findings[0], "saving") {
		t.Fatalf("findings = %v, want missing saving only", findings)
	}
	doc.Sections = append(doc.Sections, artifact.Section{ID: "REQ-003", Title: "Saving", Body: "Captured images shall be saved locally.\n\n**Priority:** must"})
	if findings := amendmentCoverageFindings(request, doc); len(findings) != 0 {
		t.Fatalf("complete amendment findings = %v", findings)
	}
}

func TestAmendmentCoverageIgnoresUnnumberedBrief(t *testing.T) {
	doc := &artifact.Document{Sections: []artifact.Section{{ID: "REQ-001", Title: "Capture", Body: "Capture images."}}}
	if findings := amendmentCoverageFindings("Build a screenshot application.", doc); len(findings) != 0 {
		t.Fatalf("brief findings = %v", findings)
	}
}

func TestAmendmentCoverageRejectsPackedAndConflictingOverride(t *testing.T) {
	request := "1) No keyboard-only operation is required 2) Saving functionality shall be implemented 3) Ubuntu newer versions use Wayland"
	doc, err := artifact.Parse("## REQ-005 — Persistent File Storage\n\nSaving is out of scope.\n\n## REQ-008 — Amendment catch-all\n\nKeyboard-only operation is optional. Ubuntu uses Wayland. Saving shall be implemented; this overrides REQ-005.", artifact.KindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	findings := amendmentCoverageFindings(request, doc)
	if !containsFinding(findings, "combines amendment clauses") || !containsFinding(findings, "both requirements remain") {
		t.Fatalf("findings = %v, want packed-clause and override conflicts", findings)
	}
}

func containsFinding(findings []string, needle string) bool {
	for _, finding := range findings {
		if strings.Contains(finding, needle) {
			return true
		}
	}
	return false
}
