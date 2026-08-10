package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jrullan/ducklab/internal/skill"
	"os"
	"path/filepath"
	"strings"
)

// FSList lists directory contents.
type FSList struct{}

// Name returns the tool name.
func (t *FSList) Name() string   { return "fs_list" }
func (t *FSList) Mutating() bool { return false }

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
		// Skip .git and common ignored dirs — as DIRECTORIES, not string
		// prefixes: the prefix version also hid .gitignore and .gitattributes
		// from every listing (and would hide node_modules-lock.json).
		if underDir(rel, ".git") || underDir(rel, "node_modules") {
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
func (t *FSRead) Name() string   { return "fs_read" }
func (t *FSRead) Mutating() bool { return false }

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
	// A range that reads backwards is a mistake in the call, not an empty file.
	// Returning "" would tell the model those lines are blank and send it
	// looking at the wrong thing; this tells it what to fix.
	if a.Start > 0 && a.End > 0 && a.Start > a.End {
		return ErrorResult("start line %d is after end line %d", a.Start, a.End), nil
	}
	total := len(strings.Split(content, "\n"))
	from := a.Start
	if from < 1 {
		from = 1
	}
	if a.Start > 0 || a.End > 0 {
		content = TruncateLines(content, a.Start, a.End)
	}

	// Capped in lines, by this tool, before the generic byte cap can reach it.
	//
	// A 57 KB file does not fit the 32 KB result ceiling, so a plain read came
	// back as "[10278 bytes truncated; showing the last 32768]" — tail-biased,
	// which is right for a failing test's output and wrong for source, and
	// stated in bytes while this tool speaks lines. A model given that has no
	// way to work out which lines it holds, so it guesses windows. Measured: an
	// implementer spent all 24 of its turns on 15 reads and 6 searches of one
	// file and never wrote a line.
	body, shown, truncated := numberedWithin(content, from, MaxToolResultBytes-512)
	if truncated {
		next := from + shown
		body += fmt.Sprintf("\n[showing lines %d-%d of %d. Read the rest with "+
			`{"path":%q,"start":%d,"end":%d}]`+"\n",
			from, from+shown-1, total, a.Path, next, next+shown-1)
	}
	return SuccessResult("%s", body), nil
}

// numberedWithin renders content with its real line numbers, stopping at a line
// boundary before a byte budget, and reports how many lines it kept.
//
// The budget is measured on the rendered text, not the raw: the line-number
// prefix is six bytes a line, and a cap that ignores it overshoots by exactly
// the amount that matters on a file with thousands of them.
//
// Head-biased, unlike the general result cap: a file is read from the top, and a
// reader handed the end of one cannot tell what came before it.
func numberedWithin(content string, from, maxBytes int) (string, int, bool) {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	kept := 0
	for i, l := range lines {
		rendered := fmt.Sprintf("%4d\t%s\n", from+i, l)
		if b.Len()+len(rendered) > maxBytes {
			return b.String(), kept, true
		}
		b.WriteString(rendered)
		kept++
	}
	return b.String(), kept, false
}

// FSSearch searches file contents.
type FSSearch struct{}

// Name returns the tool name.
func (t *FSSearch) Name() string   { return "fs_search" }
func (t *FSSearch) Mutating() bool { return false }

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
		if underDir(rel, ".git") {
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
func (t *FSWrite) Name() string   { return "fs_write" }
func (t *FSWrite) Mutating() bool { return true }

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
	msg := fmt.Sprintf("wrote %s (%d bytes)", a.Path, len(a.Content))
	if note := skillLayoutNote(a.Path); note != "" {
		msg += "\n" + note
	}
	if note := skillManifestNote(a.Path, a.Content); note != "" {
		msg += "\n" + note
	}
	return SuccessResult("%s", msg), nil
}

// skillManifestNote validates a SKILL.md as it is written.
//
// Validation already ran at the human gate, which is too late for the model:
// three runs in a row wrote a skill that would not load and each got back
// "wrote 143 bytes". Saying it here costs one parse and lands while the turn
// is still open.
//
// The write is not refused. The skills directory is deliberately unprivileged
// (05 §7.1), and a half-written manifest on the way to a good one is normal.
func skillManifestNote(path, content string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(path, "./")))
	rest, ok := strings.CutPrefix(clean, ".ducklab/skills/")
	if !ok {
		return ""
	}
	dir, file, found := strings.Cut(rest, "/")
	if !found || file != "SKILL.md" {
		return ""
	}
	sk, err := skill.Parse(content)
	if err != nil {
		return fmt.Sprintf("note: this skill will not load: %v", err)
	}
	if sk.Name == "" {
		sk.Name = dir
	}
	// Dir is left empty on purpose: the entry-exists check would fail for a
	// run.sh the model is about to write next, and warning about that would be
	// noise, not help.
	if problems := skill.Validate(sk); len(problems) > 0 {
		return "note: this skill will not load — " + strings.Join(problems, "; ")
	}
	return ""
}

// skillLayoutNote warns about a file written straight into the skills
// directory.
//
// A skill is a directory with a SKILL.md in it, and nothing tells a model
// that. The first duckling asked to write one wrote
// `.ducklab/skills/naming-convention.md`, which is not a skill and never
// becomes one — `skill list` then said "no skills" and the model had no way to
// connect the two.
//
// Said at the write, because that is the only moment the model can still fix
// it. The write itself is allowed: the skills directory is not privileged, and
// refusing a write there would be a new rule where a sentence will do.
func skillLayoutNote(path string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(path, "./")))
	const prefix = ".ducklab/skills/"
	rest, ok := strings.CutPrefix(clean, prefix)
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return "" // outside the skills directory, or properly inside a skill
	}
	name := strings.TrimSuffix(rest, filepath.Ext(rest))
	return fmt.Sprintf("note: this is not a skill. A skill is a directory with a manifest: "+
		"%s%s/SKILL.md, with YAML frontmatter (name, description, version) and the "+
		"instructions as the body. Add an `entry: run.sh` only if it needs to execute something.",
		prefix, name)
}

// FSPatch patches a file with search/replace edits.
type FSPatch struct{}

// Name returns the tool name.
func (t *FSPatch) Name() string   { return "fs_patch" }
func (t *FSPatch) Mutating() bool { return true }

// Description returns the tool description.
func (t *FSPatch) Description() string {
	return "Apply search/replace edits to a file. Each search must match exactly once. " +
		"Shape: " + fsPatchShapeHint
}

// fsPatchShapeHint is the canonical shape, stated in one place so the schema,
// the description and every error message agree.
const fsPatchShapeHint = `{"path":"f.go","edits":[{"search":"old text","replace":"new text"}]}` +
	` (old_str/new_str and old_string/new_string are accepted too, and a single edit may be written flat)`

// Schema returns the argument schema.
func (t *FSPatch) Schema() interface{} {
	return NewSchema().
		AddString("path", "File path to patch", true).
		AddArray("edits", "Search/replace pairs. Each object needs the text to find "+
			"(search, or old_str, or old_string) and its replacement (replace, or "+
			"new_str, or new_string). An empty list is an error, not a no-op.",
			&Property{Type: "object"}, true)
}

// fsPatchEdit is one search/replace pair.
//
// The aliases are not decoration. Models emit this shape from habit under
// several names, and a run measured here sent `old_str`/`new_str` seven times
// against a 612-line file: the keys were ignored, `edits` decoded empty, and the
// tool answered "patched index.html (0 edits)" as a success each time. The model
// reasonably concluded the task was done, the reviewer correctly found nothing,
// and the work was never written.
//
// Accepting a synonym is not papering over a mismatch. The tool's job is to
// apply the edit; which of four spellings the model used for "the old text" is
// not information anyone needs.
type fsPatchEdit struct {
	Search  string `json:"search"`
	Replace string `json:"replace"`

	OldStr    string `json:"old_str"`
	NewStr    string `json:"new_str"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// from returns the text to find, whichever key carried it.
func (e fsPatchEdit) from() string {
	return firstNonEmpty(e.Search, e.OldStr, e.OldString)
}

// to returns the replacement. Empty is legitimate — deleting a block is an edit
// — so it is only ever read once `from` is known to be present.
func (e fsPatchEdit) to() string {
	return firstNonEmpty(e.Replace, e.NewStr, e.NewString)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

type fsPatchArgs struct {
	Path  string        `json:"path"`
	Edits []fsPatchEdit `json:"edits"`

	// A single edit written flat, without the wrapping array. This is the shape
	// that produced the silent no-op: none of these keys exist on the canonical
	// form, so the whole call decoded to a path and nothing to do.
	fsPatchEdit
}

// edits returns the edits to apply, from either shape.
func (a fsPatchArgs) edits() []fsPatchEdit {
	if len(a.Edits) > 0 {
		return a.Edits
	}
	if a.fsPatchEdit.from() != "" {
		return []fsPatchEdit{a.fsPatchEdit}
	}
	return nil
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
	edits := a.edits()
	// A patch that changes nothing is not a patch.
	//
	// This used to fall through: zero edits meant zero loop iterations, the
	// original bytes were written back, and the tool answered "patched
	// index.html (0 edits)" as a SUCCESS. A model that sent an argument shape
	// this tool did not recognise was told seven times that it had edited the
	// file. It stopped, satisfied; the reviewer found an untouched tree; the
	// task was recorded as attempted and nothing had happened.
	//
	// The message names the shape, because the model has no other way to find
	// out what was wrong with the call it just made.
	if len(edits) == 0 {
		return ErrorResult("no edits to apply. Send %s", fsPatchShapeHint), nil
	}
	for i, edit := range edits {
		search := edit.from()
		if search == "" {
			return ErrorResult("edit %d has no text to find. Send %s", i, fsPatchShapeHint), nil
		}
		count := strings.Count(content, search)
		if count != 1 {
			return ErrorResult("edit %d: search string matches %d times (must be exactly 1)", i, count), nil
		}
		content = strings.Replace(content, search, edit.to(), 1)
	}
	// Write guard on the result
	if guard := WriteGuard(ectx, a.Path, []byte(content), false); guard != nil {
		return guard, nil
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		return ErrorResult("write: %v", err), nil
	}
	// The same notes fs_write carries. A model fixing one problem in a
	// manifest needs to hear about the next one, and it usually fixes by
	// patching.
	msg := fmt.Sprintf("patched %s (%d edits)", a.Path, len(edits))
	if note := skillManifestNote(a.Path, content); note != "" {
		msg += "\n" + note
	}
	return SuccessResult("%s", msg), nil
}

// FSDelete deletes a file.
type FSDelete struct{}

// Name returns the tool name.
func (t *FSDelete) Name() string   { return "fs_delete" }
func (t *FSDelete) Mutating() bool { return true }

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

// underDir reports whether rel is the named directory or inside it — a path
// boundary test, where strings.HasPrefix is a spelling test. ".gitignore"
// begins with ".git" and lives in no directory of that name.
func underDir(rel, dir string) bool {
	return rel == dir || strings.HasPrefix(rel, dir+string(filepath.Separator))
}
