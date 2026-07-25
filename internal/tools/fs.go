package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FSList lists directory contents.
type FSList struct{}

// Name returns the tool name.
func (t *FSList) Name() string { return "fs_list" }

// Description returns the tool description.
func (t *FSList) Description() string {
	return "List files and directories. Respects .gitignore."
}

// Schema returns the argument schema.
func (t *FSList) Schema() interface{} {
	return NewSchema().
		AddString("path", "Directory path (default: project root)", false).
		AddInt("depth", "Maximum depth (default: 2)", false)
}

type fsListArgs struct {
	Path  string `json:"path"`
	Depth int    `json:"depth"`
}

// Execute runs the tool.
func (t *FSList) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a fsListArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	if a.Path == "" {
		a.Path = "."
	}
	if a.Depth <= 0 {
		a.Depth = 2
	}
	absPath, err := PathJail(ectx.ProjectRoot, a.Path)
	if err != nil {
		return ErrorResult("jail: %v", err), nil
	}
	entries, err := listDir(absPath, a.Depth, ectx.ProjectRoot)
	if err != nil {
		return ErrorResult("list: %v", err), nil
	}
	return SuccessResult("%s", strings.Join(entries, "\n")), nil
}

func listDir(root string, depth int, projectRoot string) ([]string, error) {
	var entries []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Check depth
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) > depth {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Check .gitignore (simplified: skip .git and common ignored dirs)
		if strings.HasPrefix(rel, ".git") || strings.HasPrefix(rel, "node_modules") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		marker := ""
		if info.IsDir() {
			marker = "/"
		} else if isBinary(path) {
			marker = " [bin]"
		}
		entries = append(entries, rel+marker)
		return nil
	})
	return entries, err
}

func isBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil {
		return false
	}
	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

// FSRead reads a file.
type FSRead struct{}

// Name returns the tool name.
func (t *FSRead) Name() string { return "fs_read" }

// Description returns the tool description.
func (t *FSRead) Description() string {
	return "Read a file with optional line range."
}

// Schema returns the argument schema.
func (t *FSRead) Schema() interface{} {
	return NewSchema().
		AddString("path", "File path to read", true).
		AddInt("start", "Start line (1-indexed, inclusive)", false).
		AddInt("end", "End line (1-indexed, inclusive)", false)
}

type fsReadArgs struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Execute runs the tool.
func (t *FSRead) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a fsReadArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	absPath, err := PathJail(ectx.ProjectRoot, a.Path)
	if err != nil {
		return ErrorResult("jail: %v", err), nil
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ErrorResult("read: %v", err), nil
	}
	content := string(data)
	if a.Start > 0 || a.End > 0 {
		content = TruncateLines(content, a.Start, a.End)
	}
	return SuccessResult("%s", LineNumbers(content, 1)), nil
}

// FSSearch searches file contents.
type FSSearch struct{}

// Name returns the tool name.
func (t *FSSearch) Name() string { return "fs_search" }

// Description returns the tool description.
func (t *FSSearch) Description() string {
	return "Search file contents with a regex pattern."
}

// Schema returns the argument schema.
func (t *FSSearch) Schema() interface{} {
	return NewSchema().
		AddString("pattern", "Regex pattern to search for", true).
		AddString("glob", "File glob to limit search (e.g. '*.go')", false).
		AddInt("max", "Maximum results (default: 100)", false)
}

type fsSearchArgs struct {
	Pattern string `json:"pattern"`
	Glob    string `json:"glob"`
	Max     int    `json:"max"`
}

// Execute runs the tool.
func (t *FSSearch) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a fsSearchArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	if a.Max <= 0 {
		a.Max = 100
	}
	var results []string
	err := filepath.Walk(ectx.ProjectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(ectx.ProjectRoot, path)
		if strings.HasPrefix(rel, ".git") {
			return nil
		}
		if a.Glob != "" && !GlobMatch(a.Glob, filepath.Base(path)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if isBinary(path) {
			return nil
		}
		matches := SearchInContent(a.Pattern, string(data), a.Max-len(results))
		for _, m := range matches {
			results = append(results, fmt.Sprintf("%s:%s", rel, m))
		}
		if len(results) >= a.Max {
			return fmt.Errorf("max results reached")
		}
		return nil
	})
	if err != nil && err.Error() != "max results reached" {
		return ErrorResult("search: %v", err), nil
	}
	if len(results) == 0 {
		return SuccessResult("no matches"), nil
	}
	return SuccessResult("%s", strings.Join(results, "\n")), nil
}

// FSWrite writes a file.
type FSWrite struct{}

// Name returns the tool name.
func (t *FSWrite) Name() string { return "fs_write" }

// Description returns the tool description.
func (t *FSWrite) Description() string {
	return "Write a file. Creates parent directories."
}

// Schema returns the argument schema.
func (t *FSWrite) Schema() interface{} {
	return NewSchema().
		AddString("path", "File path to write", true).
		AddString("content", "File content", true)
}

type fsWriteArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Execute runs the tool.
func (t *FSWrite) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a fsWriteArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	// Write guard
	if guard := WriteGuard(ectx, a.Path, []byte(a.Content), true); guard != nil {
		return guard, nil
	}
	absPath, err := PathJail(ectx.ProjectRoot, a.Path)
	if err != nil {
		return ErrorResult("jail: %v", err), nil
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return ErrorResult("mkdir: %v", err), nil
	}
	if err := os.WriteFile(absPath, []byte(a.Content), 0o644); err != nil {
		return ErrorResult("write: %v", err), nil
	}
	return SuccessResult("wrote %s (%d bytes)", a.Path, len(a.Content)), nil
}

// FSPatch patches a file with search/replace edits.
type FSPatch struct{}

// Name returns the tool name.
func (t *FSPatch) Name() string { return "fs_patch" }

// Description returns the tool description.
func (t *FSPatch) Description() string {
	return "Apply search/replace edits to a file. Each search must match exactly once."
}

// Schema returns the argument schema.
func (t *FSPatch) Schema() interface{} {
	return NewSchema().
		AddString("path", "File path to patch", true).
		AddArray("edits", "List of search/replace edits", &Property{
			Type: "object",
		}, true)
}

type fsPatchEdit struct {
	Search  string `json:"search"`
	Replace string `json:"replace"`
}

type fsPatchArgs struct {
	Path  string        `json:"path"`
	Edits []fsPatchEdit `json:"edits"`
}

// Execute runs the tool.
func (t *FSPatch) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a fsPatchArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	absPath, err := PathJail(ectx.ProjectRoot, a.Path)
	if err != nil {
		return ErrorResult("jail: %v", err), nil
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ErrorResult("read: %v", err), nil
	}
	content := string(data)
	for i, edit := range a.Edits {
		count := strings.Count(content, edit.Search)
		if count != 1 {
			return ErrorResult("edit %d: search string matches %d times (must be exactly 1)", i, count), nil
		}
		content = strings.Replace(content, edit.Search, edit.Replace, 1)
	}
	// Write guard on the result
	if guard := WriteGuard(ectx, a.Path, []byte(content), false); guard != nil {
		return guard, nil
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		return ErrorResult("write: %v", err), nil
	}
	return SuccessResult("patched %s (%d edits)", a.Path, len(a.Edits)), nil
}

// FSDelete deletes a file.
type FSDelete struct{}

// Name returns the tool name.
func (t *FSDelete) Name() string { return "fs_delete" }

// Description returns the tool description.
func (t *FSDelete) Description() string {
	return "Delete a file. Refuses directories unless recursive is true."
}

// Schema returns the argument schema.
func (t *FSDelete) Schema() interface{} {
	return NewSchema().
		AddString("path", "File path to delete", true).
		AddBool("recursive", "Allow deleting directories", false)
}

type fsDeleteArgs struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

// Execute runs the tool.
func (t *FSDelete) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a fsDeleteArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	absPath, err := PathJail(ectx.ProjectRoot, a.Path)
	if err != nil {
		return ErrorResult("jail: %v", err), nil
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return ErrorResult("stat: %v", err), nil
	}
	if info.IsDir() && !a.Recursive {
		return ErrorResult("refusing to delete directory without recursive=true"), nil
	}
	if err := os.RemoveAll(absPath); err != nil {
		return ErrorResult("delete: %v", err), nil
	}
	return SuccessResult("deleted %s", a.Path), nil
}
