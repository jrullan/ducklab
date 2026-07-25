package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

func testExecContext(t *testing.T) *ExecContext {
	root := t.TempDir()
	return &ExecContext{
		ProjectRoot:  root,
		RunID:        "test-run",
		Autonomy:     config.AutonomyGuarded,
		UnsafeWrites: false,
		ShellPolicy: config.ShellPolicy{
			Mode:          "guarded",
			Deny:          []string{"rm -rf /", "shutdown"},
			AllowPrefixes: []string{"go ", "ls", "cat", "echo "},
			TimeoutS:      10,
		},
	}
}

func TestPathJail(t *testing.T) {
	root := t.TempDir()
	// Create a file inside root
	inside := filepath.Join(root, "inside.txt")
	os.WriteFile(inside, []byte("test"), 0o644)

	// Valid paths
	tests := []struct {
		path    string
		wantErr bool
	}{
		{"inside.txt", false},
		{".", false},
		{"subdir/../inside.txt", false},
		{inside, false},
	}
	for _, tt := range tests {
		_, err := PathJail(root, tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("PathJail(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
		}
	}

	// Invalid paths (escape root)
	escapes := []string{
		"../outside.txt",
		"/etc/passwd",
		filepath.Join(root, "..", "outside.txt"),
	}
	for _, path := range escapes {
		_, err := PathJail(root, path)
		if err == nil {
			t.Errorf("PathJail(%q) should fail", path)
		}
	}
}

func TestWriteGuardJail(t *testing.T) {
	ectx := testExecContext(t)
	guard := WriteGuard(ectx, "../outside.txt", []byte("test"), true)
	if guard == nil {
		t.Error("WriteGuard should fail for escaping path")
	} else if !strings.Contains(guard.Content, "jail") {
		t.Errorf("guard should mention jail: %s", guard.Content)
	}
}

func TestWriteGuardDenylist(t *testing.T) {
	ectx := testExecContext(t)
	guard := WriteGuard(ectx, ".git/config", []byte("test"), true)
	if guard == nil {
		t.Error("WriteGuard should fail for .git path")
	}
}

func TestWriteGuardMarker(t *testing.T) {
	ectx := testExecContext(t)
	content := []byte("line1\n<<<<<<< HEAD\nline2\n")
	guard := WriteGuard(ectx, "test.txt", content, true)
	if guard == nil {
		t.Error("WriteGuard should fail for conflict marker")
	} else if !strings.Contains(guard.Content, "marker guard") {
		t.Errorf("guard should mention marker guard: %s", guard.Content)
	}
}

func TestWriteGuardDucklabMarker(t *testing.T) {
	ectx := testExecContext(t)
	content := []byte("line1\n```ducklab\n{\"tool\":\"test\"}\n```\n")
	guard := WriteGuard(ectx, "test.txt", content, true)
	if guard == nil {
		t.Error("WriteGuard should fail for ducklab marker")
	}
}

func TestWriteGuardTruncation(t *testing.T) {
	ectx := testExecContext(t)
	// Create a file > 200 bytes
	bigContent := strings.Repeat("x", 2000)
	os.WriteFile(filepath.Join(ectx.ProjectRoot, "big.txt"), []byte(bigContent), 0o644)

	// Try to shrink it to < 40%
	smallContent := strings.Repeat("y", 100)
	guard := WriteGuard(ectx, "big.txt", []byte(smallContent), true)
	if guard == nil {
		t.Error("WriteGuard should fail for truncation")
	} else if !strings.Contains(guard.Content, "truncation guard") {
		t.Errorf("guard should mention truncation guard: %s", guard.Content)
	}
}

func TestWriteGuardBinary(t *testing.T) {
	ectx := testExecContext(t)
	content := []byte{'a', 'b', 0, 'c'}
	guard := WriteGuard(ectx, "test.txt", content, true)
	if guard == nil {
		t.Error("WriteGuard should fail for NUL bytes")
	}
}

func TestWriteGuardSize(t *testing.T) {
	ectx := testExecContext(t)
	content := make([]byte, 1024*1024+1)
	guard := WriteGuard(ectx, "test.txt", content, true)
	if guard == nil {
		t.Error("WriteGuard should fail for > 1MB")
	}
}

func TestWriteGuardUnsafeWrites(t *testing.T) {
	ectx := testExecContext(t)
	ectx.UnsafeWrites = true
	content := []byte("line1\n<<<<<<< HEAD\nline2\n")
	guard := WriteGuard(ectx, "test.txt", content, true)
	if guard != nil {
		t.Errorf("WriteGuard with UnsafeWrites should allow markers: %s", guard.Content)
	}
}

func TestShellPolicyCheck(t *testing.T) {
	ectx := testExecContext(t)

	// Allowed command
	if guard := ShellPolicyCheck(ectx, "go test ./..."); guard != nil {
		t.Errorf("go test should be allowed: %s", guard.Content)
	}

	// Denied command
	if guard := ShellPolicyCheck(ectx, "rm -rf /"); guard == nil {
		t.Error("rm -rf / should be denied")
	}

	// Not in allowlist
	if guard := ShellPolicyCheck(ectx, "curl http://example.com"); guard == nil {
		t.Error("curl should not be allowed in guarded mode")
	}

	// Free mode
	ectx.ShellPolicy.Mode = "free"
	if guard := ShellPolicyCheck(ectx, "curl http://example.com"); guard != nil {
		t.Errorf("curl should be allowed in free mode: %s", guard.Content)
	}

	// Off mode
	ectx.ShellPolicy.Mode = "off"
	if guard := ShellPolicyCheck(ectx, "ls"); guard == nil {
		t.Error("shell should be disabled in off mode")
	}
}

func TestScrubEnv(t *testing.T) {
	os.Setenv("TEST_API_KEY", "secret")
	os.Setenv("DUCKLAB_TEST", "secret")
	defer os.Unsetenv("TEST_API_KEY")
	defer os.Unsetenv("DUCKLAB_TEST")

	providers := map[config.ProviderID]config.Provider{
		"test": {APIKeyEnv: "TEST_API_KEY"},
	}
	env := ScrubEnv(providers)
	for _, e := range env {
		if strings.HasPrefix(e, "TEST_API_KEY=") {
			t.Error("TEST_API_KEY should be scrubbed")
		}
		if strings.HasPrefix(e, "DUCKLAB_TEST=") {
			t.Error("DUCKLAB_TEST should be scrubbed")
		}
	}
}

func TestFSWrite(t *testing.T) {
	ectx := testExecContext(t)
	tool := &FSWrite{}
	args, _ := json.Marshal(map[string]string{
		"path":    "test.txt",
		"content": "hello world",
	})
	result, err := tool.Execute(context.Background(), ectx, args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("FSWrite failed: %s", result.Content)
	}
	// Verify file was written
	data, err := os.ReadFile(filepath.Join(ectx.ProjectRoot, "test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("content = %q, want %q", data, "hello world")
	}
}

func TestFSRead(t *testing.T) {
	ectx := testExecContext(t)
	os.WriteFile(filepath.Join(ectx.ProjectRoot, "test.txt"), []byte("line1\nline2\nline3"), 0o644)
	tool := &FSRead{}
	args, _ := json.Marshal(map[string]interface{}{
		"path":  "test.txt",
		"start": 2,
		"end":   3,
	})
	result, err := tool.Execute(context.Background(), ectx, args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("FSRead failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "line2") {
		t.Errorf("should contain line2: %s", result.Content)
	}
}

func TestFSPatch(t *testing.T) {
	ectx := testExecContext(t)
	os.WriteFile(filepath.Join(ectx.ProjectRoot, "test.txt"), []byte("hello world"), 0o644)
	tool := &FSPatch{}
	args, _ := json.Marshal(map[string]interface{}{
		"path": "test.txt",
		"edits": []map[string]string{
			{"search": "world", "replace": "ducklab"},
		},
	})
	result, err := tool.Execute(context.Background(), ectx, args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("FSPatch failed: %s", result.Content)
	}
	data, _ := os.ReadFile(filepath.Join(ectx.ProjectRoot, "test.txt"))
	if string(data) != "hello ducklab" {
		t.Errorf("content = %q, want %q", data, "hello ducklab")
	}
}

func TestFSPatchMultipleMatches(t *testing.T) {
	ectx := testExecContext(t)
	os.WriteFile(filepath.Join(ectx.ProjectRoot, "test.txt"), []byte("hello hello hello"), 0o644)
	tool := &FSPatch{}
	args, _ := json.Marshal(map[string]interface{}{
		"path": "test.txt",
		"edits": []map[string]string{
			{"search": "hello", "replace": "bye"},
		},
	})
	result, err := tool.Execute(context.Background(), ectx, args)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("FSPatch should fail when search matches multiple times")
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	expected := []string{"fs_list", "fs_read", "fs_search", "fs_write", "fs_patch", "fs_delete", "shell", "verify_run", "git_status", "git_diff", "git_log"}
	for _, name := range expected {
		if _, err := r.Get(name); err != nil {
			t.Errorf("tool %q not registered: %v", name, err)
		}
	}
	if _, err := r.Get("nonexistent"); err == nil {
		t.Error("Get(\"nonexistent\") should fail")
	}
}
