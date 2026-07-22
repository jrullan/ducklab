package prim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SearchReplaceResult reports the outcome of applying a batch of surgical edits.
type SearchReplaceResult struct {
	Applied  int
	Rejected []string
}

// ApplySearchReplace parses and applies surgical edits in the form:
//
//	=== FILE: path/to/file ===
//	<<< SEARCH
//	<exact current text>
//	===
//	<replacement text>
//	>>> REPLACE
//
// A block whose SEARCH text is not found verbatim is rejected (never guessed),
// so by construction this cannot destroy code outside a matched block. The
// closing delimiter is tolerant: ">>>" or ">>> REPLACE".
func ApplySearchReplace(repo, output string) SearchReplaceResult {
	lines := strings.Split(output, "\n")
	res := SearchReplaceResult{}
	current := ""
	i := 0
	for i < len(lines) {
		line := lines[i]
		if m := fileHeader.FindStringSubmatch(line); m != nil {
			current = strings.TrimSpace(m[1])
			i++
			continue
		}
		if strings.TrimSpace(line) == "<<< SEARCH" && current != "" {
			i++
			var search []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != "===" {
				search = append(search, lines[i])
				i++
			}
			i++ // skip ===
			var replace []string
			for i < len(lines) {
				t := strings.TrimSpace(lines[i])
				if t == ">>> REPLACE" || t == ">>>" {
					break
				}
				replace = append(replace, lines[i])
				i++
			}
			i++ // skip >>>

			searchText := strings.Join(search, "\n")
			replaceText := strings.Join(replace, "\n")
			p := filepath.Join(repo, filepath.FromSlash(current))

			data, err := os.ReadFile(p)
			if err != nil {
				res.Rejected = append(res.Rejected, fmt.Sprintf("%s: file not found", current))
				continue
			}
			content := string(data)
			if !strings.Contains(content, searchText) {
				head := searchText
				if len(head) > 60 {
					head = head[:60]
				}
				res.Rejected = append(res.Rejected,
					fmt.Sprintf("%s: SEARCH text not found (first 60 chars: %q)", current, head))
				continue
			}
			content = strings.Replace(content, searchText, replaceText, 1)
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				res.Rejected = append(res.Rejected, fmt.Sprintf("%s: write failed: %v", current, err))
				continue
			}
			res.Applied++
			continue
		}
		i++
	}
	if res.Applied == 0 && len(res.Rejected) == 0 {
		res.Rejected = append(res.Rejected, "no SEARCH/REPLACE blocks found in output")
	}
	return res
}
