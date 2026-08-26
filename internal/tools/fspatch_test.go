package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func patchIn(t *testing.T, body, args string) (*Result, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "index.html")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := (&FSPatch{}).Execute(context.Background(),
		&ExecContext{ProjectRoot: root}, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(after)
}

// A patch that changes nothing reported SUCCESS.
//
// Zero edits meant zero loop iterations, the original bytes were written back,
// and the answer was "patched index.html (0 edits)". Measured on a real run: a
// model sent an argument shape this tool did not recognise, was told seven times
// that it had edited a 612-line file, and stopped satisfied. The reviewer found
// an untouched tree. The task was recorded as attempted and nothing had happened.
func TestAPatchThatChangesNothingIsAnError(t *testing.T) {
	res, after := patchIn(t, "hello\n", `{"path":"index.html","edits":[]}`)
	if !res.IsError {
		t.Errorf("zero edits reported success: %q", res.Content)
	}
	// And the model must be able to find out what was wrong with its call.
	if !strings.Contains(res.Content, "search") {
		t.Errorf("the error does not name the expected shape: %q", res.Content)
	}
	if after != "hello\n" {
		t.Errorf("the file was touched anyway: %q", after)
	}
}

// The exact call that produced the silent no-op: one edit, flat, with old_str
// and new_str. This is the shape models emit from habit.
func TestTheFlatOldStrShapeApplies(t *testing.T) {
	res, after := patchIn(t,
		"canvas { display: block; }\n</style>\n",
		`{"path":"index.html","old_str":"</style>","new_str":".edge-input { position: absolute; }\n</style>"}`)
	if res.IsError {
		t.Fatalf("the flat shape was refused: %q", res.Content)
	}
	if !strings.Contains(after, ".edge-input") {
		t.Errorf("the edit did not land: %q", after)
	}
}

// Every spelling of the same idea reaches the same place.
func TestEverySpellingOfAnEditWorks(t *testing.T) {
	for _, args := range []string{
		`{"path":"index.html","edits":[{"search":"old","replace":"new"}]}`,
		`{"path":"index.html","edits":[{"old_str":"old","new_str":"new"}]}`,
		`{"path":"index.html","edits":[{"old_string":"old","new_string":"new"}]}`,
		`{"path":"index.html","search":"old","replace":"new"}`,
	} {
		res, after := patchIn(t, "the old thing\n", args)
		if res.IsError {
			t.Errorf("%s -> %q", args, res.Content)
			continue
		}
		if after != "the new thing\n" {
			t.Errorf("%s left %q", args, after)
		}
	}
}

// An edit carrying a replacement but nothing to find cannot be applied, and
// saying "matches 0 times" would send the reader looking at the file.
func TestAnEditWithNothingToFindIsAnError(t *testing.T) {
	res, after := patchIn(t, "hello\n", `{"path":"index.html","edits":[{"replace":"x"}]}`)
	if !res.IsError {
		t.Errorf("an edit with no search text was accepted: %q", res.Content)
	}
	if after != "hello\n" {
		t.Errorf("the file was touched: %q", after)
	}
}

// Deleting a block is a real edit, so an empty replacement must still apply.
func TestAnEmptyReplacementDeletes(t *testing.T) {
	res, after := patchIn(t, "keep\nDROP\nkeep\n",
		`{"path":"index.html","edits":[{"search":"DROP\n","replace":""}]}`)
	if res.IsError {
		t.Fatalf("a deletion was refused: %q", res.Content)
	}
	if after != "keep\nkeep\n" {
		t.Errorf("after = %q", after)
	}
}

// The count it reports must be the edits it actually made.
func TestTheReportedCountIsWhatWasApplied(t *testing.T) {
	res, _ := patchIn(t, "a b\n", `{"path":"index.html","old_str":"a","new_str":"z"}`)
	if !strings.Contains(res.Content, "(1 edits)") {
		t.Errorf("content = %q", res.Content)
	}
}

// The still-exact-once rule: a search that matches twice is ambiguous and the
// model must narrow it, not have one occurrence picked for it.
func TestAnAmbiguousSearchIsStillRefused(t *testing.T) {
	res, after := patchIn(t, "x\nx\n", `{"path":"index.html","old_str":"x","new_str":"y"}`)
	if !res.IsError {
		t.Errorf("an ambiguous search was applied: %q", res.Content)
	}
	if after != "x\nx\n" {
		t.Errorf("the file was touched: %q", after)
	}
}

// A miss must TEACH. "matches 0 times" was measured producing a fail→read→fail
// loop — six failed patches on one file in one run — because the error said
// nothing about HOW the search differed from the file. These tests pin each
// diagnosis branch to the failure mode it repairs.

// The commonest self-inflicted miss: the model copied its own fs_read output,
// line-number prefixes included.
func TestAMissWithLineNumberPrefixesIsDiagnosed(t *testing.T) {
	res, after := patchIn(t, "func a() {\n\treturn\n}\n",
		`{"path":"index.html","old_str":"   1\tfunc a() {\n   2\t\treturn","new_str":"x"}`)
	if !res.IsError {
		t.Fatalf("a prefixed search was applied: %q", res.Content)
	}
	if !strings.Contains(res.Content, "line-number prefixes") {
		t.Errorf("the miss was not diagnosed as copied prefixes: %q", res.Content)
	}
	if after != "func a() {\n\treturn\n}\n" {
		t.Errorf("the file was touched: %q", after)
	}
}

// The second commonest: the text is right, the whitespace is not — a model
// re-typing a tab-indented file with spaces. The repair is the line number.
func TestAMissByWhitespaceNamesTheLine(t *testing.T) {
	res, _ := patchIn(t, "func main() {\n\tdoThing()\n}\n",
		`{"path":"index.html","old_str":"func main() {\n    doThing()\n}","new_str":"x"}`)
	if !res.IsError {
		t.Fatalf("a whitespace-drifted search was applied: %q", res.Content)
	}
	if !strings.Contains(res.Content, "line 1") || !strings.Contains(res.Content, "whitespace") {
		t.Errorf("the miss was not located: %q", res.Content)
	}
	if !strings.Contains(res.Content, "tabs") {
		t.Errorf("the file's indent style was not named: %q", res.Content)
	}
}

// fs_patch is all-or-nothing, and the error must SAY so: a model told only
// "edit 1 missed" concludes edit 0 landed and composes its next call against
// a file state that does not exist.
func TestAMissStatesThatNothingWasApplied(t *testing.T) {
	res, after := patchIn(t, "one\ntwo\nthree\n",
		`{"path":"index.html","edits":[{"search":"one","replace":"uno"},{"search":"missing","replace":"x"}]}`)
	if !res.IsError {
		t.Fatalf("a partial patch was applied: %q", res.Content)
	}
	if !strings.Contains(res.Content, "No edits from this call were applied") {
		t.Errorf("the all-or-nothing contract is not stated: %q", res.Content)
	}
	if after != "one\ntwo\nthree\n" {
		t.Errorf("edits landed despite the error: %q", after)
	}
}

// An ambiguous search names WHERE it matched, so the model can extend it
// instead of guessing.
func TestAnAmbiguousSearchNamesItsLines(t *testing.T) {
	res, _ := patchIn(t, "x\ny\nx\n", `{"path":"index.html","old_str":"x","new_str":"z"}`)
	if !res.IsError {
		t.Fatalf("an ambiguous search was applied: %q", res.Content)
	}
	if !strings.Contains(res.Content, "lines 1, 3") {
		t.Errorf("the match locations are not named: %q", res.Content)
	}
}

// search == replace is a no-op the model mistook for an edit; reporting it as
// "patched (1 edits)" tells it the work is done when nothing changed.
func TestIdenticalSearchAndReplaceIsRefused(t *testing.T) {
	res, after := patchIn(t, "hello\n", `{"path":"index.html","old_str":"hello","new_str":"hello"}`)
	if !res.IsError {
		t.Fatalf("an identical search/replace was reported as an edit: %q", res.Content)
	}
	if after != "hello\n" {
		t.Errorf("the file changed: %q", after)
	}
}

// When only the tail of a search has drifted, its first line anchors the
// diagnosis: re-read THERE, not the whole file.
func TestADriftedTailIsAnchoredToItsFirstLine(t *testing.T) {
	res, _ := patchIn(t, "alpha unique\nbeta\ngamma\n",
		`{"path":"index.html","old_str":"alpha unique\nCHANGED","new_str":"x"}`)
	if !res.IsError {
		t.Fatalf("a drifted search was applied: %q", res.Content)
	}
	if !strings.Contains(res.Content, "first line matches line 1") {
		t.Errorf("the drift was not anchored: %q", res.Content)
	}
}

func TestFSPatchRefusalsEndTheTurnOnTheFifthRefusalPerFile(t *testing.T) {
	const maxRefusalsBeforeTurnEnd = 5

	root := t.TempDir()
	for _, name := range []string{"target.go", "other.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package target\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ectx := &ExecContext{ProjectRoot: root}
	registry := NewRegistry()
	patch := func(path, search string) *Result {
		t.Helper()
		args, err := json.Marshal(map[string]interface{}{
			"path":  path,
			"edits": []map[string]string{{"search": search, "replace": "replacement"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := registry.Execute(context.Background(), ectx, "fs_patch", args)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	// Five failed patches engage the existing brake; they are not refusals.
	for i := 1; i <= FSPatchFailLimit; i++ {
		if result := patch("target.go", "missing-"+strconv.Itoa(i)); !result.IsError || strings.Contains(result.Content, "REFUSED") {
			t.Fatalf("failure %d = %#v; want a non-refusal error", i, result)
		}
	}

	// The first four knocks on the brake retain its existing rewrite remedy.
	for refusal := 1; refusal < maxRefusalsBeforeTurnEnd; refusal++ {
		result := patch("target.go", "refused-"+strconv.Itoa(refusal))
		if !result.IsError || !strings.Contains(result.Content, "REFUSED") {
			t.Fatalf("refusal %d = %#v; want REFUSED error", refusal, result)
		}
		if !strings.Contains(result.Content, "fs_write_lines") || !strings.Contains(result.Content, "fs_write") {
			t.Errorf("refusal %d lacks rewrite remedy: %q", refusal, result.Content)
		}
		if strings.Contains(result.Content, "end your reply") {
			t.Errorf("refusal %d ended the turn early: %q", refusal, result.Content)
		}
	}

	// Refusals are per file: another file starts at its own first refusal.
	for i := 1; i <= FSPatchFailLimit; i++ {
		patch("other.go", "other-missing-"+strconv.Itoa(i))
	}
	if other := patch("other.go", "other-refused"); !other.IsError || !strings.Contains(other.Content, "REFUSED") || strings.Contains(other.Content, "end your reply") {
		t.Fatalf("other.go inherited target.go's refusal count: %#v", other)
	}

	last := patch("target.go", "terminal-refusal")
	if !last.IsError || !strings.Contains(last.Content, "REFUSED") {
		t.Fatalf("fifth refusal = %#v; want terminal refusal", last)
	}
	if !strings.Contains(last.Content, "fs_write_lines") || !strings.Contains(last.Content, "fs_write") {
		t.Errorf("terminal refusal lacks rewrite remedy: %q", last.Content)
	}
	// Match the gate brake's explicit instruction that makes this the last tool
	// result of the turn, rather than another invitation to vary the patch.
	if !strings.Contains(last.Content, "end your reply") {
		t.Errorf("terminal refusal does not end the turn: %q", last.Content)
	}
}

func TestFSReadOfBrakedFileResetsItsPatchFailureStreak(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package target\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ectx := &ExecContext{ProjectRoot: root}
	registry := NewRegistry()
	patch := func(search, replace string) *Result {
		t.Helper()
		args, err := json.Marshal(map[string]interface{}{
			"path":  "target.go",
			"edits": []map[string]string{{"search": search, "replace": replace}},
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := registry.Execute(context.Background(), ectx, "fs_patch", args)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	// The fifth failed patch engages the brake; the following call is refused.
	for i := 0; i < FSPatchFailLimit; i++ {
		if result := patch("missing-"+strconv.Itoa(i), "replacement"); !result.IsError || strings.Contains(result.Content, "REFUSED") {
			t.Fatalf("failure %d = %#v; want a non-refusal error", i+1, result)
		}
	}
	if refused := patch("package target", "package changed"); !refused.IsError || !strings.Contains(refused.Content, "REFUSED") {
		t.Fatalf("braked patch = %#v; want REFUSED", refused)
	}

	// Reading the braked path is the prescribed recovery and reopens fs_patch.
	read, err := registry.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"target.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if read.IsError {
		t.Fatalf("fs_read = %#v", read)
	}
	if recovered := patch("package target", "package changed"); recovered.IsError {
		t.Fatalf("matching patch remained braked after fs_read: %q", recovered.Content)
	}

	// The reset is only a fresh window: five new failures engage the brake again.
	for i := 0; i < FSPatchFailLimit; i++ {
		if result := patch("again-missing-"+strconv.Itoa(i), "replacement"); !result.IsError || strings.Contains(result.Content, "REFUSED") {
			t.Fatalf("new failure %d = %#v; want a non-refusal error", i+1, result)
		}
	}
	if refused := patch("package changed", "package target"); !refused.IsError || !strings.Contains(refused.Content, "REFUSED") {
		t.Fatalf("brake did not re-engage after new failures: %#v", refused)
	}
}

func TestFSPatchFailureStreakBrakesByFileAndReportsHealth(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "target.go")
	if err := os.WriteFile(path, []byte("package target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.go"), []byte("package other\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var reports []struct {
		reason string
		data   map[string]interface{}
	}
	ectx := &ExecContext{
		ProjectRoot: root,
		OnDistress: func(reason string, data map[string]interface{}) {
			reports = append(reports, struct {
				reason string
				data   map[string]interface{}
			}{reason: reason, data: data})
		},
	}
	registry := NewRegistry()
	failingPatch := func(search string) *Result {
		args, err := json.Marshal(map[string]interface{}{
			"path":  "target.go",
			"edits": []map[string]string{{"search": search, "replace": "replacement"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := registry.Execute(context.Background(), ectx, "fs_patch", args)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	// Different bad searches must still form one per-file streak.
	for i := 1; i <= 4; i++ {
		if result := failingPatch("missing-" + strconv.Itoa(i)); !result.IsError {
			t.Fatalf("failure %d unexpectedly succeeded: %q", i, result.Content)
		}
	}
	if result := failingPatch("missing-5"); !result.IsError {
		t.Fatalf("the fifth failure unexpectedly succeeded: %q", result.Content)
	}
	// The fifth failure is permitted; the next attempt is the one refused.
	refused := failingPatch("a sixth, different bad search")
	if !refused.IsError {
		t.Fatalf("sixth failure was not refused: %q", refused.Content)
	}
	if !strings.Contains(refused.Content, "fs_patch has failed 5 times on this file") ||
		!strings.Contains(refused.Content, "fs_write_lines") ||
		!strings.Contains(refused.Content, "fs_write") {
		t.Errorf("refusal lacks the ranged-rewrite remedy: %q", refused.Content)
	}

	if len(reports) == 0 {
		t.Fatal("failure streak was not reported to the health surface")
	}
	foundCount := false
	foundFile := false
	for _, report := range reports {
		if count, ok := report.data["count"].(int); ok && count == 5 {
			foundCount = true
		}
		if path, ok := report.data["path"].(string); ok && path == "target.go" {
			foundFile = true
		}
	}
	if !foundCount || !foundFile {
		t.Errorf("health reports = %#v; want count 5 and target.go", reports)
	}

	// A streak belongs to one file, not to the whole fs_patch tool.
	otherArgs, err := json.Marshal(map[string]interface{}{
		"path":  "other.go",
		"edits": []map[string]string{{"search": "also-missing", "replace": "replacement"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := registry.Execute(context.Background(), ectx, "fs_patch", otherArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !other.IsError || strings.Contains(other.Content, "REFUSED") {
		t.Fatalf("a different file inherited the target.go brake: %#v", other)
	}
}
