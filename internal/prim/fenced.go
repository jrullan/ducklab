package prim

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// fenceOpen matches a ```search / ```replace opening fence (3+ backticks or
// tildes). Fenced code blocks are the one format modest local models produce
// reliably, and the fence never collides with code the way ===/<<</>>> did.
var fenceOpen = regexp.MustCompile("^\\s*(`{3,}|~{3,})(search|replace)\\s*$")

// FencedEditFormat is the surgical edit contract handed to models.
const FencedEditFormat = "Make each change as a SEARCH/REPLACE edit using fenced blocks — this " +
	"exact format:\n\n" +
	"=== FILE: relative/path ===\n" +
	"```search\n" +
	"<a few lines of the EXACT current text, copied verbatim from the file shown above>\n" +
	"```\n" +
	"```replace\n" +
	"<the new text that replaces it>\n" +
	"```\n\n" +
	"Rules:\n" +
	"- Copy the search text VERBATIM from the file above (exact whitespace and indentation).\n" +
	"- Keep each search block small — just the lines around your change, enough to be unique.\n" +
	"- Use several === FILE: blocks and/or several search/replace pairs for several changes.\n" +
	"- To CREATE a new file: an empty ```search block and the full content in ```replace.\n" +
	"- Output ONLY these blocks — no prose, no line numbers, no other markers."

// HasFencedEdits reports whether output uses the fenced search/replace format
// (vs. whole-file blocks). Used to route ApplyEdits.
func HasFencedEdits(output string) bool {
	for _, l := range strings.Split(output, "\n") {
		if m := fenceOpen.FindStringSubmatch(l); m != nil && m[2] == "search" {
			return true
		}
	}
	return false
}

// ApplyEdits applies a model's changes, accepting either fenced search/replace
// edits (surgical, preferred) or whole === FILE: === file blocks (fallback).
// It routes by the format actually present so the model can use either.
func ApplyEdits(repo, output string) SearchReplaceResult {
	if HasFencedEdits(output) {
		return ApplyFencedEdits(repo, output)
	}
	n, err := ApplyFileBlocks(repo, output)
	if err != nil {
		return SearchReplaceResult{Rejected: []string{err.Error()}}
	}
	return SearchReplaceResult{Applied: n}
}

// ApplyFencedEdits parses ```search/```replace pairs grouped under
// "=== FILE: path ===" headers and applies them. Search text must match the
// file verbatim (mismatch is rejected, never guessed); an empty search creates
// a new file.
func ApplyFencedEdits(repo, output string) SearchReplaceResult {
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
		path := strings.TrimSpace(fileHeader.FindStringSubmatch(lines[h])[1])
		start := h + 1
		end := len(lines)
		if k+1 < len(hdr) {
			end = hdr[k+1]
		}
		applyFenced(repo, path, lines[start:end], &res)
	}
	if res.Applied == 0 && len(res.Rejected) == 0 {
		res.Rejected = append(res.Rejected, "no fenced search/replace edits found")
	}
	return res
}

func applyFenced(repo, path string, body []string, res *SearchReplaceResult) {
	p := filepath.Join(repo, filepath.FromSlash(path))
	i := 0
	for i < len(body) {
		m := fenceOpen.FindStringSubmatch(body[i])
		if m == nil || m[2] != "search" {
			i++
			continue
		}
		sFence := m[1]
		i++
		var search []string
		for i < len(body) && !fenceClosed(body[i], sFence) {
			search = append(search, body[i])
			i++
		}
		i++ // closing fence
		for i < len(body) && strings.TrimSpace(body[i]) == "" {
			i++
		}
		rm := fenceOpenAt(body, i)
		if rm == nil || rm[2] != "replace" {
			res.Rejected = append(res.Rejected, path+": ```search block not followed by ```replace")
			continue
		}
		rFence := rm[1]
		i++
		var replace []string
		for i < len(body) && !fenceClosed(body[i], rFence) {
			replace = append(replace, body[i])
			i++
		}
		i++ // closing fence

		searchText := strings.Join(search, "\n")
		replaceText := strings.Join(replace, "\n")
		if hasStructuralMarker(replaceText, path) {
			res.Rejected = append(res.Rejected, path+": replace contains ducklab/merge markers — refused")
			continue
		}

		data, err := os.ReadFile(p)
		missing := err != nil
		if strings.TrimSpace(searchText) == "" { // create
			if !missing {
				res.Rejected = append(res.Rejected, path+": empty search but file exists")
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
		if missing {
			res.Rejected = append(res.Rejected, path+": file not found")
			continue
		}
		content := string(data)
		if !strings.Contains(content, searchText) {
			head := searchText
			if len(head) > 60 {
				head = head[:60]
			}
			res.Rejected = append(res.Rejected, path+": search text not found (first 60 chars: "+head+")")
			continue
		}
		if err := os.WriteFile(p, []byte(strings.Replace(content, searchText, replaceText, 1)), 0o644); err != nil {
			res.Rejected = append(res.Rejected, path+": write failed: "+err.Error())
			continue
		}
		res.Applied++
	}
}

func fenceOpenAt(body []string, i int) []string {
	if i >= len(body) {
		return nil
	}
	return fenceOpen.FindStringSubmatch(body[i])
}

// fenceClosed reports whether line closes a fence opened with openFence — a run
// of the same fence char, at least as long as the opener, and nothing else.
func fenceClosed(line, openFence string) bool {
	t := strings.TrimSpace(line)
	if len(t) < len(openFence) {
		return false
	}
	ch := openFence[0]
	for i := 0; i < len(t); i++ {
		if t[i] != ch {
			return false
		}
	}
	return true
}
