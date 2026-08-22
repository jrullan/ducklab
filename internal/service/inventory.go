package service

import (
	"path/filepath"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
)

// inventoryUnaccounted performs the adoption coverage check using lexical matching.
func inventoryUnaccounted(items []agent.InventoryItem, proposed *artifact.Document) []agent.InventoryItem {
	if proposed == nil {
		return append([]agent.InventoryItem(nil), items...)
	}
	// Coverage is deliberately lexical and limited to section bodies. Titles,
	// frontmatter, and the engine's preamble are not evidence that a capability
	// was documented; an explicit "out of scope" line is still in a body and
	// therefore counts naturally.
	var body strings.Builder
	// The preamble may contain an explicit out-of-scope declaration. Section
	// titles are intentionally excluded, since merely naming a surface as a
	// heading is not coverage.
	body.WriteString("\n")
	body.WriteString(proposed.Preamble)
	// Explicit out-of-scope declarations may be document preamble text rather
	// than a section body; retain only those lines, not arbitrary headings.
	for _, line := range strings.Split(proposed.Raw, "\n") {
		if strings.Contains(strings.ToLower(line), "out of scope") {
			body.WriteString("\n")
			body.WriteString(line)
		}
	}
	for _, sec := range proposed.Sections {
		body.WriteString("\n")
		body.WriteString(sec.Body)
		for _, child := range sec.Children {
			body.WriteString("\n")
			body.WriteString(child.Body)
		}
	}
	text := body.String()
	var out []agent.InventoryItem
	for _, item := range items {
		if !strings.Contains(text, item.Name) && !strings.Contains(text, filepath.Base(item.EvidencePath)) {
			out = append(out, item)
		}
	}
	return out
}
