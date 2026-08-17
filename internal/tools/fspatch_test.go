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
			"path": "target.go",
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
		"path": "other.go",
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
