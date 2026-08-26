package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func searchIn(t *testing.T, root, args string) *Result {
	t.Helper()
	res, err := (&FSSearch{}).Execute(context.Background(),
		&ExecContext{ProjectRoot: root}, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// An invalid pattern used to be reported inside the RESULTS — one
// "path:invalid regex" line per file walked, as a success. A model that sent
// `count(` read a hundred of those and had no way to see its own pattern was
// the problem. The pattern is validated once, and the error teaches the fix.
func TestAnInvalidRegexIsAnErrorThatTeaches(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("count(x)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := searchIn(t, root, `{"pattern":"count("}`)
	if !res.IsError {
		t.Fatalf("an invalid regex was reported as results: %q", res.Content)
	}
	if !strings.Contains(res.Content, "regular expression") || !strings.Contains(res.Content, "escape") {
		t.Errorf("the error does not teach the repair: %q", res.Content)
	}
}

// A glob like 'internal/*.go' matched nothing when tested against base names
// only, and the resulting "no matches" read as "that code does not exist".
// The glob now matches the file name OR the project-relative path.
func TestAGlobMayNameARelativePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "a.go"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := searchIn(t, root, `{"pattern":"needle","glob":"internal/*.go"}`)
	if res.IsError {
		t.Fatalf("search failed: %q", res.Content)
	}
	if !strings.Contains(res.Content, "internal/a.go") {
		t.Errorf("a path glob found nothing: %q", res.Content)
	}
}
