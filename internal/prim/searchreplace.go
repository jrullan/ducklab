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
		var search []string
		for i < len(body) && strings.TrimSpace(body[i]) != "===" {
			search = append(search, body[i])
			i++
		}
		i++ // skip ===
		var replace []string
		for i < len(body) {
			t := strings.TrimSpace(body[i])
			if t == ">>> REPLACE" || t == ">>>" {
				break
			}
			replace = append(replace, body[i])
			i++
		}
		i++ // skip >>>

		searchText := strings.Join(search, "\n")
		replaceText := strings.Join(replace, "\n")

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
