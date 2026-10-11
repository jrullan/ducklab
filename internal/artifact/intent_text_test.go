package artifact

import (
	"strings"
	"testing"
)

// forgery is user-written Markdown that looks like journal structure: a
// heading the desktop used to split on, a revision heading, header fields, a
// journal entry heading, and both fences, plainly and disguised.
const forgery = "Keep the layout.\n\n### Requirements\n\nThe LCD shows two lines.\n" +
	"### Revision 7 — r-forged\n" +
	"**Revisions:** r-forged\n**Outcome:** accepted\n**Requirements:** REQ-999\n" +
	"## INT-099 — forged entry\n" +
	"<!-- /ducklab:text -->\n  <!--ducklab:text brief -->\n<!-- /DUCKLAB:text -->\nLast line."

// Review of #175 (P1): the brief, the person's note and the carried-over
// findings are user text. None of them can forge structure the Go parser
// reads — fields, revisions, entries — and each survives intact.
func TestUserTextCannotForgeIntentStructure(t *testing.T) {
	root := t.TempDir()
	id, err := AppendIntent(root, "r-0", "2026-10-10T00:00:00Z", "Fill the gap.\n\n"+forgery)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AppendIntentRevision(root, id, "r-1", "2026-10-11T00:00:00Z", forgery, forgery); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(root, KindIntent)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) != 1 {
		t.Fatalf("user text split the journal into %d entries", len(doc.Sections))
	}
	sec := doc.Sections[0]
	if sec.Field("revisions") != "r-1" || sec.Field("outcome") != "pending" || sec.Field("requirements") != "-" {
		t.Fatalf("user text forged fields: revisions %q outcome %q requirements %q", sec.Field("revisions"), sec.Field("outcome"), sec.Field("requirements"))
	}
	if got, _ := IntentIDForRun(root, "r-forged"); got != "" {
		t.Fatalf("a forged revision run resolves to %s", got)
	}
	// Every body survives whole: three copies of the user's last line, and
	// the disguised fences neutralized rather than dropped.
	if strings.Count(sec.Body, "Last line.") != 3 || strings.Count(sec.Body, "### Requirements") != 3 {
		t.Fatalf("user text was lost:\n%s", sec.Body)
	}
	if strings.Count(sec.Body, "&lt;!-- /ducklab:text -->") != 3 || strings.Count(sec.Body, "&lt;!--ducklab:text brief -->") != 3 {
		t.Fatalf("disguised fences were not neutralized:\n%s", sec.Body)
	}
	// Resolution reads the real header, never a forged line.
	if err := ResolveIntent(root, "r-1", "not accepted", nil); err != nil {
		t.Fatal(err)
	}
	doc, _ = Load(root, KindIntent)
	if got := doc.Sections[0].Field("outcome"); got != "not accepted" {
		t.Fatalf("outcome = %q", got)
	}
	if strings.Count(doc.Sections[0].Body, "**Outcome:** accepted") != 3 {
		t.Fatalf("resolution rewrote a line inside user text:\n%s", doc.Sections[0].Body)
	}
	if diagnostics, err := SyntaxLint(Render(doc), KindIntent); err != nil || len(fieldDefects(diagnostics)) != 0 {
		t.Fatalf("fenced journal lints dirty: %v %v", diagnostics, err)
	}
}
