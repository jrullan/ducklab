package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Three searches in a row that find nothing are enough: the fourth is
// refused with directions instead of served. Different patterns dodged the
// identical-call brake for 21 searches on an empty tree.
func TestConsecutiveEmptySearchesAreStopped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register(&FSSearch{})
	ectx := &ExecContext{ProjectRoot: dir}
	ectx.BeginTurn()
	for i := 0; i < SearchMissLimit; i++ {
		res, _ := reg.Execute(context.Background(), ectx, "fs_search", json.RawMessage(fmt.Sprintf(`{"pattern":"REQ-003\\.%d","glob":"*.md"}`, i)))
		if res.IsError {
			t.Fatalf("search %d should simply miss: %s", i, res.Content)
		}
	}
	res, _ := reg.Execute(context.Background(), ectx, "fs_search", json.RawMessage(`{"pattern":"REQ-003\\.9","glob":"*.md"}`))
	if !res.IsError || !strings.Contains(res.Content, "STOP SEARCHING") {
		t.Fatalf("the fourth empty search was served: err=%v %.200s", res.IsError, res.Content)
	}
	// A hit resets the count.
	hit, _ := reg.Execute(context.Background(), ectx, "fs_search", json.RawMessage(`{"pattern":"hello","glob":"*.md"}`))
	if hit.IsError {
		t.Fatalf("a real search was refused: %s", hit.Content)
	}
}
