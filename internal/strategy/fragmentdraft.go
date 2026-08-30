package strategy

import (
	"regexp"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
)

var fragmentH2 = regexp.MustCompile(`(?m)^##\s+([A-Za-z]+-\d+)(?:\s|$)`)

type fragmentBlock struct {
	id   string
	text string
}

// materializeFragment folds an architect's partial H2 patch onto the
// candidate accumulated so far. It intentionally operates on Markdown blocks
// rather than an artifact kind: plan amendments use both M- and T- headings,
// while requirements and specs use REQ-/SPEC-. The stage's real parser still
// validates and merges the final result into the approved base document.
func materializeFragment(base, patch *agent.Outcome) *agent.Outcome {
	if patch == nil {
		return base
	}
	patches := fragmentBlocks(patch.Text)
	if base == nil || len(fragmentBlocks(base.Text)) == 0 {
		return patch
	}
	// A prose-only stand-pat response is not a deletion of every section the
	// architect previously emitted.
	if len(patches) == 0 {
		return base
	}

	blocks := fragmentBlocks(base.Text)
	for _, next := range patches {
		replaced := false
		// -900 is the repeatable placeholder for new sections. Multiple such
		// blocks are distinct additions and must never collapse into one.
		placeholder := strings.HasSuffix(strings.ToUpper(next.id), "-900")
		if !placeholder {
			for i := range blocks {
				if strings.EqualFold(blocks[i].id, next.id) {
					blocks[i] = next
					replaced = true
					break
				}
			}
		}
		if !replaced {
			blocks = append(blocks, next)
		}
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
			id:   text[loc[2]:loc[3]],
			text: strings.TrimSpace(text[loc[0]:end]),
		})
	}
	return blocks
}
