package prim

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var fileHeader = regexp.MustCompile(`^===\s*FILE:\s*(.+?)\s*===\s*$`)

// FilePaths returns the distinct file paths named by "=== FILE: <path> ==="
// headers in text, in first-seen order.
func FilePaths(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if m := fileHeader.FindStringSubmatch(line); m != nil {
			p := strings.TrimSpace(m[1])
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// ApplyFileBlocks parses "=== FILE: <path> ===" blocks out of a model's output
// and writes each one, whole, into repo. It returns the number of files written
// or an error when the output contains no file blocks — an empty or truncated
// completion must fail loudly, not silently write nothing.
func ApplyFileBlocks(repo, output string) (int, error) {
	var current string
	var buf []string
	written := 0

	flush := func() error {
		if current == "" {
			return nil
		}
		p := filepath.Join(repo, filepath.FromSlash(current))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		body := strings.Join(buf, "\n") + "\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
		written++
		return nil
	}

	for _, line := range strings.Split(output, "\n") {
		if m := fileHeader.FindStringSubmatch(line); m != nil {
			if err := flush(); err != nil {
				return written, err
			}
			current = strings.TrimSpace(m[1])
			buf = buf[:0]
			continue
		}
		if current != "" {
			buf = append(buf, line)
		}
	}
	if err := flush(); err != nil {
		return written, err
	}
	if written == 0 {
		return 0, fmt.Errorf("output contained no === FILE: === blocks")
	}
	return written, nil
}
