package stage

import (
	"os"
	"path/filepath"
	"strings"
)

// Greenfield reports whether a project tree has nothing to read: no source,
// no documents — only ducklab's own state and git's dotfiles. An intake on
// such a tree has no reason to explore, and a small model that is not told
// so spends its first minutes listing directories and re-reading absent
// artifacts (Neocapture, 2026-08-29: 8 tool calls before a single line of
// requirements).
func Greenfield(projectRoot string) bool {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if name == ".ducklab" || name == ".git" {
			continue
		}
		if !e.IsDir() && strings.HasPrefix(name, ".git") {
			// .gitignore, .gitattributes: git's furniture, not the product.
			continue
		}
		if e.IsDir() {
			if empty, _ := dirEmpty(filepath.Join(projectRoot, name)); empty {
				continue
			}
		}
		return false
	}
	return true
}

func dirEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}
