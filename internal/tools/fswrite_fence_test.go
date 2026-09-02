package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFSWriteRefusesMarkdownFencesInSourceFiles(t *testing.T) {
	root := t.TempDir()
	ectx := &ExecContext{ProjectRoot: root}
	args, _ := json.Marshal(fsWriteArgs{Path: "src/main.c", Content: "int main(void) { return 0; }\n```\n"})
	res, err := (&FSWrite{}).Execute(context.Background(), ectx, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "line 2") || !strings.Contains(res.Content, "raw file content") {
		t.Fatalf("fenced source was not refused with a repair: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "src/main.c")); !os.IsNotExist(err) {
		t.Fatalf("refused source write still created the file: %v", err)
	}
}

func TestFSWriteAllowsFencesInMarkdownAndInlineSourceStrings(t *testing.T) {
	for _, tc := range []struct {
		path, content string
	}{
		{"README.md", "```c\nint main(void);\n```\n"},
		{"src/main.js", "const fence = '```';\n"},
	} {
		root := t.TempDir()
		args, _ := json.Marshal(fsWriteArgs{Path: tc.path, Content: tc.content})
		res, err := (&FSWrite{}).Execute(context.Background(), &ExecContext{ProjectRoot: root}, args)
		if err != nil || res.IsError {
			t.Fatalf("%s was refused: err=%v result=%+v", tc.path, err, res)
		}
	}
}
