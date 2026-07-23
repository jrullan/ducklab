package prim

import (
	"os"
	"path/filepath"
	"strings"
)

// SearchReplaceResult reports the outcome of applying a batch of edits.
type SearchReplaceResult struct {
	Applied  int
	Rejected []string
}

// ApplySearchReplace applies changes grouped under "=== FILE: path ===" headers.
// A file segment can take either form:
//
//	Edit an existing file (surgical, cannot destroy unrelated code):
//	  === FILE: path ===
//	  <<< SEARCH
//	  <exact current text>
//	  ===
//	  <replacement text>
//	  >>> REPLACE
//
//	Create a new file (models emit whole files naturally for new files):
//	  === FILE: path ===
//	  <full file content>
//
// A full-file block targeting an existing file is rejected — editing an existing
// file must go through SEARCH/REPLACE, so a whole-file dump can never silently
// overwrite real code. SEARCH text that isn't found verbatim is rejected too.
func ApplySearchReplace(repo, output string) SearchReplaceResult {
	res := SearchReplaceResult{}
	lines := strings.Split(output, "\n")

	var hdr []int
	for i, l := range lines {
		if fileHeader.MatchString(l) {
			hdr = append(hdr, i)
		}
	}
	if len(hdr) == 0 {
		res.Rejected = append(res.Rejected, "no === FILE: === blocks found in output")
		return res
	}

	for k, h := range hdr {
		m := fileHeader.FindStringSubmatch(lines[h])
		path := strings.TrimSpace(m[1])
		start := h + 1
		end := len(lines)
		if k+1 < len(hdr) {
			end = hdr[k+1]
		}
		body := lines[start:end]
		if hasLine(body, "<<< SEARCH") {
			applySRBlocks(repo, path, body, &res)
		} else {
			applyFullFile(repo, path, body, &res)
		}
	}
	if res.Applied == 0 && len(res.Rejected) == 0 {
		res.Rejected = append(res.Rejected, "no applicable changes found")
	}
	return res
}

func hasLine(body []string, want string) bool {
	for _, l := range body {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}

// srDivider matches the SEARCH→REPLACE separator: canonical "===" or the
// git-conflict-style "=======".
func srDivider(l string) bool {
	t := strings.TrimSpace(l)
	return t == "===" || t == "======="
}

// srTerminator matches the end of a REPLACE section, tolerating the variants
// models emit (and the case where ">>> REPLACE" is used as the separator too).
func srTerminator(l string) bool {
	t := strings.TrimSpace(l)
	return t == ">>> REPLACE" || t == ">>>" || t == ">>>>>>> REPLACE"
}

// srBlockStart matches the beginning of the next block, so an unterminated
// section stops cleanly instead of swallowing the following block.
func srBlockStart(l string) bool {
	return strings.TrimSpace(l) == "<<< SEARCH" || fileHeader.MatchString(l)
}

// applyFullFile writes a whole new file. It refuses to clobber an existing file.
func applyFullFile(repo, path string, body []string, res *SearchReplaceResult) {
	content := strings.Join(trimBlankEdges(body), "\n")
	if strings.TrimSpace(content) == "" {
		return // header with no content — nothing to do
	}
	p := filepath.Join(repo, filepath.FromSlash(path))
	if _, err := os.Stat(p); err == nil {
		res.Rejected = append(res.Rejected,
			path+": whole-file block but file exists — use a SEARCH/REPLACE block to edit it")
		return
	}
	if hasStructuralMarker(content) {
		res.Rejected = append(res.Rejected,
			path+": content contains ducklab/merge markers — refused (would corrupt the file)")
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		res.Rejected = append(res.Rejected, path+": mkdir failed: "+err.Error())
		return
	}
	if err := os.WriteFile(p, []byte(content+"\n"), 0o644); err != nil {
		res.Rejected = append(res.Rejected, path+": create failed: "+err.Error())
		return
	}
	res.Applied++
}

// hasStructuralMarker reports whether text contains, on its own line, one of
// ducklab's SEARCH/REPLACE markers or a git conflict marker. Writing such a
// line into a file corrupts it (and breaks future edits) — the earth3.html
// "<<< SEARCH injected into JavaScript" bug. Real source never has these as
// standalone lines, so refusing to write them is safe.
func hasStructuralMarker(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if t == "<<< SEARCH" || t == ">>> REPLACE" || fileHeader.MatchString(l) ||
			strings.HasPrefix(t, "<<<<<<<") || strings.HasPrefix(t, ">>>>>>>") {
			return true
		}
	}
	return false
}

// applySRBlocks applies one or more SEARCH/REPLACE blocks within a file segment.
// An empty SEARCH also creates the file (Aider convention), so both new-file
// styles work.
func applySRBlocks(repo, path string, body []string, res *SearchReplaceResult) {
	p := filepath.Join(repo, filepath.FromSlash(path))
	i := 0
	for i < len(body) {
		if strings.TrimSpace(body[i]) != "<<< SEARCH" {
			i++
			continue
		}
		i++
		// Collect SEARCH until a divider. Canonically that's a bare "===", but
		// models often drop it and use ">>> REPLACE" (git-conflict / Aider style)
		// as the separator — accept both, and a git-style "=======" too.
		var search []string
		for i < len(body) && !srDivider(body[i]) && !srTerminator(body[i]) && !srBlockStart(body[i]) {
			search = append(search, body[i])
			i++
		}
		// Consume the divider (but not a new block start — that belongs to the
		// next iteration).
		if i < len(body) && (srDivider(body[i]) || srTerminator(body[i])) {
			i++
		}
		var replace []string
		for i < len(body) && !srTerminator(body[i]) && !srBlockStart(body[i]) {
			replace = append(replace, body[i])
			i++
		}
		if i < len(body) && srTerminator(body[i]) {
			i++ // consume the >>> REPLACE terminator
		}

		searchText := strings.Join(search, "\n")
		replaceText := strings.Join(replace, "\n")

		// Never write ducklab/merge markers into a file (self-corruption guard).
		if hasStructuralMarker(replaceText) {
			res.Rejected = append(res.Rejected,
				path+": REPLACE contains ducklab/merge markers — refused (would corrupt the file)")
			continue
		}

		data, err := os.ReadFile(p)
		fileMissing := err != nil

		if strings.TrimSpace(searchText) == "" { // create
			if !fileMissing {
				res.Rejected = append(res.Rejected,
					path+": empty SEARCH but file exists — use a real SEARCH block to edit")
				continue
			}
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte(replaceText+"\n"), 0o644); err != nil {
				res.Rejected = append(res.Rejected, path+": create failed: "+err.Error())
				continue
			}
			res.Applied++
			continue
		}
		if fileMissing {
			res.Rejected = append(res.Rejected, path+": file not found")
			continue
		}
		content := string(data)
		if !strings.Contains(content, searchText) {
			head := searchText
			if len(head) > 60 {
				head = head[:60]
			}
			res.Rejected = append(res.Rejected, path+": SEARCH text not found (first 60 chars: "+head+")")
			continue
		}
		if err := os.WriteFile(p, []byte(strings.Replace(content, searchText, replaceText, 1)), 0o644); err != nil {
			res.Rejected = append(res.Rejected, path+": write failed: "+err.Error())
			continue
		}
		res.Applied++
	}
}

// trimBlankEdges drops leading and trailing all-blank lines from a segment.
func trimBlankEdges(body []string) []string {
	s, e := 0, len(body)
	for s < e && strings.TrimSpace(body[s]) == "" {
		s++
	}
	for e > s && strings.TrimSpace(body[e-1]) == "" {
		e--
	}
	return body[s:e]
}
