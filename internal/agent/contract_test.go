package agent

import (
	"strings"
	"testing"
)

func TestVerdictContractParsesFindings(t *testing.T) {
	text := `{"verdict":"request-changes","findings":[
		{"severity":"major","file":"auth.go","line":88,"issue":"nil deref when the token is expired","fix":"guard before deref"},
		{"severity":"minor","file":"auth.go","line":12,"issue":"unused import","fix":"remove it"}]}`
	got, err := ParseContract("verdict", text)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := got.(*Verdict)
	if !ok {
		t.Fatalf("got %T, want *Verdict", got)
	}
	if v.Approved() {
		t.Error("request-changes reported as approved")
	}
	if len(v.Findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(v.Findings))
	}
	if v.Findings[0].Line != 88 || v.Findings[0].File != "auth.go" {
		t.Errorf("finding anchor lost: %+v", v.Findings[0])
	}
	if len(v.Blocking()) != 1 {
		t.Errorf("got %d blocking findings, want 1 (major)", len(v.Blocking()))
	}
}

func TestVerdictApproveWithNoFindingsIsValid(t *testing.T) {
	got, err := ParseContract("verdict", `{"verdict":"approve","findings":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !got.(*Verdict).Approved() {
		t.Error("approve not recognised")
	}
}

// A reviewer cannot approve while reporting blocking problems; accepting that
// would let a run look approved with majors outstanding.
func TestVerdictApproveWithBlockingFindingsIsRejected(t *testing.T) {
	_, err := ParseContract("verdict", `{"verdict":"approve","findings":[
		{"severity":"critical","file":"a.go","line":1,"issue":"data loss","fix":"do not"}]}`)
	if err == nil {
		t.Fatal("approve with a critical finding was accepted")
	}
}

func TestVerdictRejectsBadSeverity(t *testing.T) {
	for _, body := range []string{
		`{"verdict":"request-changes","findings":[{"file":"a.go","issue":"x","fix":"y"}]}`,
		`{"verdict":"request-changes","findings":[{"severity":"nit","file":"a.go","issue":"x","fix":"y"}]}`,
		`{"verdict":"request-changes","findings":[{"severity":"major","file":"a.go","issue":"","fix":"y"}]}`,
	} {
		if _, err := ParseContract("verdict", body); err == nil {
			t.Errorf("accepted an invalid finding: %s", body)
		}
	}
}

func TestVerdictRejectsUnknownVerdict(t *testing.T) {
	if _, err := ParseContract("verdict", `{"verdict":"lgtm","findings":[]}`); err == nil {
		t.Error(`accepted verdict "lgtm"`)
	}
}

// Models wrap JSON in fences and prose even when told not to.
func TestContractToleratesFencesAndPreamble(t *testing.T) {
	texts := []string{
		"```json\n{\"verdict\":\"approve\",\"findings\":[]}\n```",
		"Here is my review:\n\n{\"verdict\":\"approve\",\"findings\":[]}",
		"```\n{\"verdict\":\"approve\",\"findings\":[]}\n```\n",
	}
	for _, text := range texts {
		if _, err := ParseContract("verdict", text); err != nil {
			t.Errorf("failed to extract JSON from %q: %v", text, err)
		}
	}
}

func TestExtractJSONHandlesBracesInStrings(t *testing.T) {
	text := `{"verdict":"request-changes","findings":[{"severity":"minor","file":"a.go","line":1,"issue":"uses {placeholder} syntax","fix":"escape it"}]}`
	got, err := ParseContract("verdict", text)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.(*Verdict).Findings[0].Issue, "{placeholder}") {
		t.Error("braces inside a string broke extraction")
	}
}

// AC-21: prose instead of JSON must fail, never be guessed at.
func TestVerdictRejectsProse(t *testing.T) {
	prose := "The code looks good to me overall, I'd approve it."
	if _, err := ParseContract("verdict", prose); err == nil {
		t.Fatal("prose was accepted as a verdict")
	}
}

func TestChoiceContract(t *testing.T) {
	got, err := ParseContract("choice", `{"choice":"B","reason":"smallest change that passes"}`)
	if err != nil {
		t.Fatal(err)
	}
	c := got.(*Choice)
	if c.Choice != "B" || !c.Chosen() {
		t.Errorf("got %+v, want choice B", c)
	}
}

func TestChoiceNoneIsValidButNotChosen(t *testing.T) {
	got, err := ParseContract("choice", `{"choice":"none","reason":"all candidates ignore the task"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.(*Choice).Chosen() {
		t.Error(`"none" reported as chosen`)
	}
}

func TestChoiceRejectsBadLabelsAndMissingReason(t *testing.T) {
	for _, body := range []string{
		`{"choice":"candidate B","reason":"x"}`,
		`{"choice":"1","reason":"x"}`,
		`{"choice":"","reason":"x"}`,
		`{"choice":"A"}`,
		`{"choice":"A","reason":"   "}`,
	} {
		if _, err := ParseContract("choice", body); err == nil {
			t.Errorf("accepted invalid choice: %s", body)
		}
	}
}

func TestMarkdownSectionsContract(t *testing.T) {
	text := `Some preamble.

## REQ-001 — Users can log in
**Priority:** must

Body of the first requirement.

## REQ-002 - Sessions expire
Body of the second.
`
	got, err := ParseContract("markdown_sections:REQ", text)
	if err != nil {
		t.Fatal(err)
	}
	secs := got.([]Section)
	if len(secs) != 2 {
		t.Fatalf("got %d sections, want 2", len(secs))
	}
	if secs[0].ID != "REQ-001" || secs[0].Title != "Users can log in" {
		t.Errorf("section 0 = %+v", secs[0])
	}
	// Both an em dash and a hyphen separator must work; models produce both.
	if secs[1].ID != "REQ-002" || secs[1].Title != "Sessions expire" {
		t.Errorf("section 1 = %+v", secs[1])
	}
	if !strings.Contains(secs[0].Body, "Body of the first requirement.") {
		t.Errorf("body lost: %q", secs[0].Body)
	}
}

func TestMarkdownSectionsRejectsNoMatches(t *testing.T) {
	if _, err := ParseContract("markdown_sections:REQ", "## Introduction\n\nNo ids here."); err == nil {
		t.Error("accepted text with no matching sections")
	}
}

func TestFreeformAndEditsParseToNil(t *testing.T) {
	for _, c := range []string{"", "freeform", "edits"} {
		got, err := ParseContract(c, "anything at all")
		if err != nil || got != nil {
			t.Errorf("contract %q: got (%v, %v), want (nil, nil)", c, got, err)
		}
	}
}

func TestUnknownContractIsAnError(t *testing.T) {
	if _, err := ParseContract("telepathy", "{}"); err == nil {
		t.Error("unknown contract accepted")
	}
}

func TestEmptyResponseIsAnError(t *testing.T) {
	for _, c := range []string{"verdict", "choice", "json:triage"} {
		if _, err := ParseContract(c, "   "); err == nil {
			t.Errorf("contract %q accepted an empty response", c)
		}
	}
}
