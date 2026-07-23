package prim

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var identRe = regexp.MustCompile(`\b[a-zA-Z_][a-zA-Z0-9_]{2,}\b`)

// commonWords are identifiers too generic to point at a specific file.
var commonWords = map[string]bool{
	"the": true, "and": true, "for": true, "not": true, "you": true,
	"are": true, "this": true, "that": true, "with": true, "from": true,
	"have": true, "will": true, "can": true, "all": true, "new": true,
	"add": true, "file": true, "test": true, "code": true, "change": true,
	"should": true, "must": true, "each": true, "value": true, "values": true,
	"color": true, "colors": true, "search": true, "replace": true,
	"function": true, "module": true, "line": true, "lines": true,
}

// RepoListing returns the tracked file list (capped), for orientation.
func RepoListing(repo string) string {
	_, out := Shell("git ls-files | head -100", repo)
	return out
}

// RelevantFiles gathers the full contents of files a requirement points at:
// those named by path, test files, and any source file containing an
// identifier mentioned in the requirement. For small repos it falls back to
// every tracked source file. Source is prioritized over tests within a byte
// budget so the solver always sees the code it must edit (the single biggest
// lesson from the legacy pilot: listing-only prompts starve the model).
func RelevantFiles(requirement, repo string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 12000
	}
	_, listing := Shell("git ls-files", repo)
	var files []string
	for _, l := range strings.Split(listing, "\n") {
		if s := strings.TrimSpace(l); s != "" {
			files = append(files, s)
		}
	}

	picked := map[string]bool{}
	var order []string
	add := func(f string) {
		if !picked[f] {
			picked[f] = true
			order = append(order, f)
		}
	}

	for _, f := range files {
		if strings.Contains(requirement, f) {
			add(f)
		}
	}
	testCount := 0
	hasTest := false
	for _, f := range order {
		if strings.HasPrefix(f, "test") || strings.Contains(f, "_test.") {
			hasTest = true
		}
	}
	if !hasTest {
		for _, f := range files {
			if (strings.HasPrefix(f, "test") || strings.Contains(f, "_test.")) && testCount < 2 {
				add(f)
				testCount++
			}
		}
	}

	// identifier match against source files
	idents := map[string]bool{}
	for _, m := range identRe.FindAllString(requirement, -1) {
		lc := strings.ToLower(m)
		if !commonWords[lc] {
			idents[m] = true
		}
	}
	srcExt := func(f string) bool {
		for _, e := range []string{
			".go", ".py", ".js", ".ts", ".jsx", ".tsx",
			".html", ".htm", ".css", ".vue", ".svelte", ".rb", ".rs",
			".java", ".c", ".h", ".cpp", ".sh", ".md",
		} {
			if strings.HasSuffix(f, e) {
				return true
			}
		}
		return false
	}
	matchedSrc := false
	for _, f := range files {
		if picked[f] || !srcExt(f) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		content := string(data)
		for id := range idents {
			if strings.Contains(content, id) {
				add(f)
				matchedSrc = true
				break
			}
		}
	}

	// small-repo fallback: include all source files
	if !matchedSrc {
		var src []string
		for _, f := range files {
			if srcExt(f) {
				src = append(src, f)
			}
		}
		if len(src) <= 30 {
			for _, f := range src {
				add(f)
			}
		}
	}

	// source before tests
	sort.SliceStable(order, func(i, j int) bool {
		ti := strings.HasPrefix(order[i], "test") || strings.Contains(order[i], "_test.")
		tj := strings.HasPrefix(order[j], "test") || strings.Contains(order[j], "_test.")
		if ti != tj {
			return !ti
		}
		return order[i] < order[j]
	})

	var chunks []string
	budget := maxChars
	for _, f := range order {
		data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		block := "=== " + f + " ===\n" + string(data)
		if len(block) > budget {
			if budget <= 0 {
				break
			}
			block = block[:budget] + "\n... (truncated)"
		}
		chunks = append(chunks, block)
		budget -= len(block)
		if budget <= 0 {
			break
		}
	}
	return strings.Join(chunks, "\n\n")
}
