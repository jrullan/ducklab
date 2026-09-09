package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// B-359: Fledge's implementer wrote the verification script successfully but
// could not run it: fs_write created 0644, while shell correctly refused chmod
// and git update-index. The filesystem tool must own the executable bit, and
// git must see it so a clean checkout reproduces the gate.
func TestFSWriteCreatesAnExecutableFileWhoseModeIsVersioned(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	tool := &FSWrite{}
	schema := tool.Schema().(*ToolSchema)
	if property, ok := schema.Properties["executable"]; !ok || property.Type != "boolean" {
		t.Fatalf("fs_write schema executable = %+v, present %v; want optional boolean", property, ok)
	}
	args, _ := json.Marshal(map[string]interface{}{
		"path": "scripts/require-tests.sh", "content": "#!/bin/sh\nexit 0\n", "executable": true,
	})
	result, err := tool.Execute(context.Background(), &ExecContext{ProjectRoot: root}, args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("fs_write executable failed: %s", result.Content)
	}
	path := filepath.Join(root, "scripts", "require-tests.sh")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("script mode = %04o, want 0755", got)
	}
	if out, err := exec.Command(path).CombinedOutput(); err != nil {
		t.Fatalf("script is not directly executable: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", root, "add", "scripts/require-tests.sh").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	summary, err := exec.Command("git", "-C", root, "diff", "--cached", "--summary").CombinedOutput()
	if err != nil {
		t.Fatalf("git diff: %v: %s", err, summary)
	}
	if !strings.Contains(string(summary), "create mode 100755 scripts/require-tests.sh") {
		t.Fatalf("git did not version the executable bit:\n%s", summary)
	}
}

func TestFSWritePreservesOrAddsExecutableBitOnExistingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "check.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": "check.sh", "content": "#!/bin/sh\nexit 0\n"})
	result, err := (&FSWrite{}).Execute(context.Background(), &ExecContext{ProjectRoot: root}, args)
	if err != nil || result.IsError {
		t.Fatalf("fs_write existing executable: result=%+v err=%v", result, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("existing executable mode = %04o, want 0755", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(map[string]interface{}{
		"path": "check.sh", "content": "#!/bin/sh\nexit 0\n", "executable": true,
	})
	result, err = (&FSWrite{}).Execute(context.Background(), &ExecContext{ProjectRoot: root}, args)
	if err != nil || result.IsError {
		t.Fatalf("fs_write promote existing script: result=%+v err=%v", result, err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("promoted script mode = %04o, want 0755", info.Mode().Perm())
	}
}

func TestFSWriteLeavesNewFilesNonExecutableByDefault(t *testing.T) {
	root := t.TempDir()
	args, _ := json.Marshal(map[string]string{
		"path": "script.sh", "content": "#!/bin/sh\nexit 0\n",
	})
	result, err := (&FSWrite{}).Execute(context.Background(), &ExecContext{ProjectRoot: root}, args)
	if err != nil || result.IsError {
		t.Fatalf("ordinary fs_write: result=%+v err=%v", result, err)
	}
	info, err := os.Stat(filepath.Join(root, "script.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("default file mode = %04o, want 0644", got)
	}
}

func TestShellChmodRefusalNamesTheFilesystemRemedy(t *testing.T) {
	ectx := &ExecContext{ShellPolicy: config.ShellPolicy{Mode: "guarded", AllowPrefixes: []string{"cargo "}}}
	result := ShellPolicyCheck(ectx, "chmod +x scripts/require-tests.sh")
	if result == nil || !result.IsError {
		t.Fatal("guarded shell unexpectedly allowed chmod")
	}
	for _, want := range []string{"fs_write", "executable", "true"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("chmod refusal omitted %q and left the model without a lawful remedy: %s", want, result.Content)
		}
	}
}
