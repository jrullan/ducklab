package strategy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
)

var fragmentH2 = regexp.MustCompile(`(?m)^##\s+([A-Za-z]+-\d+)(?:\s|$)`)

func fragmentPlaceholderForPrompt(prefix string) string {
	return strings.ToUpper(strings.TrimSpace(prefix)) + "-900"
}

type fragmentBlock struct {
	id    string
	title string
	text  string
}

// materializeFragment folds an architect's partial H2 patch onto the
// candidate accumulated so far. It intentionally operates on Markdown blocks
// rather than an artifact kind: plan amendments use both M- and T- headings,
// while requirements and specs use REQ-/SPEC-. The stage's real parser still
// validates and merges the final result into the approved base document.
func materializeFragment(base, patch *agent.Outcome, prefix string) *agent.Outcome {
	if patch == nil {
		return base
	}
	patches := fragmentBlocks(patch.Text)
	// A prose-only stand-pat response is not a deletion of every section the
	// architect previously emitted.
	if len(patches) == 0 {
		if base == nil {
			return patch
		}
		return base
	}

	var blocks []fragmentBlock
	if base != nil {
		blocks = fragmentBlocks(base.Text)
	}
	for _, next := range patches {
		// New sections enter as a repeatable -900 placeholder. Give them stable
		// temporary identities immediately, before a reviewer or another round
		// sees them. A later pass may obey the prompt (repeat -900) or a critic's
		// mistaken advice (emit -900..-908); title identity maps both shapes back
		// onto the same accumulated additions.
		match := -1
		if !isFragmentTemporaryID(next.id, prefix) {
			match = uniqueFragmentID(blocks, next.id)
		}
		if match < 0 {
			match = uniqueFragmentTitle(blocks, next.title)
		}
		if match >= 0 {
			next = rewriteFragmentBlockID(next, blocks[match].id)
			blocks[match] = next
			continue
		}
		if isFragmentTemporaryID(next.id, prefix) {
			next = rewriteFragmentBlockID(next, nextFragmentTemporaryID(blocks, prefix))
		}
		blocks = append(blocks, next)
	}

	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		texts = append(texts, strings.TrimSpace(block.text))
	}
	merged := *patch
	merged.Text = strings.Join(texts, "\n\n")
	merged.Parsed = nil // fragment architects deliberately use freeform.
	return &merged
}

func uniqueFragmentTitle(blocks []fragmentBlock, title string) int {
	want := strings.ToLower(strings.TrimSpace(title))
	if want == "" {
		return -1
	}
	match := -1
	for i := range blocks {
		if strings.ToLower(strings.TrimSpace(blocks[i].title)) != want {
			continue
		}
		if match >= 0 {
			return -1
		}
		match = i
	}
	return match
}

func uniqueFragmentID(blocks []fragmentBlock, id string) int {
	match := -1
	for i := range blocks {
		if !strings.EqualFold(blocks[i].id, id) {
			continue
		}
		if match >= 0 {
			return -1
		}
		match = i
	}
	return match
}

func isFragmentTemporaryID(id, prefix string) bool {
	up := strings.ToUpper(strings.TrimSpace(id))
	want := strings.ToUpper(strings.TrimSpace(prefix)) + "-"
	if !strings.HasPrefix(up, want) {
		return false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(up, want))
	return err == nil && n >= 900
}

func nextFragmentTemporaryID(blocks []fragmentBlock, prefix string) string {
	used := map[string]bool{}
	for _, block := range blocks {
		used[strings.ToUpper(block.id)] = true
	}
	for n := 900; ; n++ {
		id := fmt.Sprintf("%s-%03d", strings.ToUpper(prefix), n)
		if !used[id] {
			return id
		}
	}
}

func rewriteFragmentBlockID(block fragmentBlock, id string) fragmentBlock {
	lineEnd := strings.IndexByte(block.text, '\n')
	if lineEnd < 0 {
		lineEnd = len(block.text)
	}
	heading := block.text[:lineEnd]
	if loc := fragmentH2.FindStringSubmatchIndex(heading); loc != nil {
		heading = heading[:loc[2]] + id + heading[loc[3]:]
		block.text = heading + block.text[lineEnd:]
	}
	block.id = id
	return block
}

func fragmentBlocks(text string) []fragmentBlock {
	locs := fragmentH2.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}
	blocks := make([]fragmentBlock, 0, len(locs))
	for i, loc := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		blocks = append(blocks, fragmentBlock{
			id:    text[loc[2]:loc[3]],
			title: fragmentBlockTitle(text[loc[0]:end]),
			text:  strings.TrimSpace(text[loc[0]:end]),
		})
	}
	return blocks
}

func fragmentBlockTitle(text string) string {
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	loc := fragmentH2.FindStringSubmatchIndex(line)
	if loc == nil {
		return ""
	}
	rest := strings.TrimSpace(line[loc[1]:])
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(rest, "—"), "-"))
	return rest
}
