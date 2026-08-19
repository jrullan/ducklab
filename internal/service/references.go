package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Reference documents for a document stage.
//
// An adopt reads the code as its primary truth, but a project that has lived
// a while usually has prose the code cannot carry — a wiki of module docs,
// decision history, a compliance report. Those live OUTSIDE the project
// root, where the ducklings' fs tools rightly cannot reach, so the one
// honest channel is the prompt: the person names paths, the engine loads
// them bounded, and the record says exactly what was included and what the
// caps dropped — silent truncation reads as "covered" when it was not.

const (
	refPerFileChars = 12_000
	refTotalChars   = 80_000
	refMaxFiles     = 40
)

// refFile is one loaded reference, for the record.
type refFile struct {
	Path      string `json:"path"`
	Chars     int    `json:"chars"`
	Truncated bool   `json:"truncated,omitempty"`
}

// loadReferences expands the named paths (files, or directories searched
// recursively for .md and .txt), loads them under the caps, and renders the
// section a stage prompt carries. dropped names what the caps excluded.
func loadReferences(paths []string) (rendered string, loaded []refFile, dropped []string, err error) {
	var files []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "~/") {
			if home, herr := os.UserHomeDir(); herr == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		info, serr := os.Stat(p)
		if serr != nil {
			return "", nil, nil, fmt.Errorf("reference %q: %w", p, serr)
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		werr := filepath.WalkDir(p, func(path string, d os.DirEntry, werr error) error {
			if werr != nil || d.IsDir() {
				return werr
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".md", ".txt":
				files = append(files, path)
			}
			return nil
		})
		if werr != nil {
			return "", nil, nil, fmt.Errorf("reference %q: %w", p, werr)
		}
	}
	sort.Strings(files)

	var b strings.Builder
	total := 0
	for _, f := range files {
		if len(loaded) >= refMaxFiles || total >= refTotalChars {
			dropped = append(dropped, f)
			continue
		}
		raw, rerr := os.ReadFile(f)
		if rerr != nil {
			dropped = append(dropped, f)
			continue
		}
		text := string(raw)
		truncated := false
		if len(text) > refPerFileChars {
			text = text[:refPerFileChars]
			truncated = true
		}
		if total+len(text) > refTotalChars {
			text = text[:refTotalChars-total]
			truncated = true
		}
		fmt.Fprintf(&b, "\n### Reference: %s\n\n%s\n", f, strings.TrimSpace(text))
		if truncated {
			b.WriteString("\n[truncated — the full document exceeds the reference budget]\n")
		}
		total += len(text)
		loaded = append(loaded, refFile{Path: f, Chars: len(text), Truncated: truncated})
	}
	if len(loaded) == 0 {
		return "", loaded, dropped, nil
	}
	head := "\n\n## Reference documents\n\n" +
		"Provided by the person as context. Where a reference and the code disagree, " +
		"the code is the truth for as-built claims — note the disagreement instead of copying the claim.\n"
	return head + b.String(), loaded, dropped, nil
}
