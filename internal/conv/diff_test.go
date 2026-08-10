package conv

import (
	"strings"
	"testing"
)

func fileSection(path string, bodyLines int, line string) string {
	var b strings.Builder
	b.WriteString("diff --git a/" + path + " b/" + path + "\n")
	b.WriteString("--- a/" + path + "\n+++ b/" + path + "\n@@ -1 +1 @@\n")
	for i := 0; i < bodyLines; i++ {
		b.WriteString("+" + line + "\n")
	}
	return b.String()
}

// An ordinary diff must pass through byte-identical: compaction exists for
// the pathological case, not as a new rendering of the normal one.
func TestASmallDiffPassesThroughUntouched(t *testing.T) {
	diff := fileSection("a.go", 20, "return a + b") + fileSection("b.go", 10, "want := 6")
	if got := CompactDiff(diff); got != diff {
		t.Errorf("a small diff was rewritten:\n%q\nvs\n%q", got, diff)
	}
}

// T-067's shape: a tracked build artifact rode the reviewer's prompt at
// 644KB — 98% of the diff — and was re-read on every call of the loop. The
// oversized file collapses to an honest stub; the real source files beside
// it survive intact.
func TestAGiantFileCollapsesAndItsSiblingsSurvive(t *testing.T) {
	source := fileSection("frontend/src/DashboardPage.jsx", 30, "const lbs = kg * 2.20462;")
	bundle := fileSection("frontend/dist/assets/index-K61J8f-2.js", 2000, strings.Repeat("x", 400))
	got := CompactDiff(source + bundle)

	if !strings.Contains(got, "const lbs = kg * 2.20462;") {
		t.Error("the real source diff was lost")
	}
	if strings.Contains(got, strings.Repeat("x", 400)) {
		t.Error("the bundle's body still rides the prompt")
	}
	// The stub says which file, how big, and what to do about it.
	if !strings.Contains(got, "frontend/dist/assets/index-K61J8f-2.js") {
		t.Error("the stub does not name the file")
	}
	if !strings.Contains(got, "+2000/-0") {
		t.Errorf("the stub does not carry the counts: %q", got[len(got)-300:])
	}
	if !strings.Contains(got, "tools") {
		t.Error("the stub does not tell the reviewer how to see the file anyway")
	}
	// The header line survives so the file's presence in the change is fact,
	// not rumor.
	if !strings.Contains(got, "diff --git a/frontend/dist/assets/index-K61J8f-2.js") {
		t.Error("the file's diff header was dropped")
	}
}

// A producer that marks no file boundaries still cannot smuggle a megabyte
// into a prompt.
func TestAnUnmarkedBlobIsStubbedWhole(t *testing.T) {
	blob := strings.Repeat("+"+strings.Repeat("y", 200)+"\n", 500)
	got := CompactDiff(blob)
	if len(got) > 1000 {
		t.Errorf("an unmarked blob rode through at %d bytes", len(got))
	}
	if !strings.Contains(got, "omitted") {
		t.Errorf("no honest stub: %q", got)
	}
}
