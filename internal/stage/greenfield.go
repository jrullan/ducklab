package stage

import (
	"os"
	"path/filepath"
	"strings"
)

// greenfieldDocumentNotice tells every seat of a document council on an
// empty tree that there is no code to consult. The spec reviewer of a
// greenfield project ran 21 fs_search calls over nothing (Neocapture,
// 2026-08-30).
const greenfieldDocumentNotice = "## The project has no code yet\n\n" +
	"The tree holds no source: there is nothing to list, read or search there, and every " +
	"such call returns nothing. The documents you need — the approved ones and the draft " +
	"under review — are in this prompt. Work from them; do not search the tree.\n\n"

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
