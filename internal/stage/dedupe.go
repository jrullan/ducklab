package stage

import (
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
)

// dedupeSections drops what a model wrote twice: a second section with an
// id already used, or a section whose title and body repeat an earlier one
// under a new id. An extend council appended its fragment twice — REQ-010
// and REQ-011 identical, two "Out of scope" blocks — and the duplicate
// reached the approved document (benchmark run 3). The first occurrence
// wins; the record keeps what was dropped.
func dedupeSections(doc *artifact.Document) (dropped []string) {
	if doc == nil {
		return nil
	}
	seenID := map[string]bool{}
	seenText := map[string]string{}
	var kept []artifact.Section
	for _, s := range doc.Sections {
		key := strings.ToLower(strings.TrimSpace(s.Title)) + "\x00" + semanticSectionBody(s.Body)
		switch {
		case s.ID != "" && seenID[s.ID]:
			dropped = append(dropped, s.ID+" (id already used)")
			continue
		case seenText[key] != "":
			dropped = append(dropped, s.ID+" (same title and body as "+seenText[key]+")")
			continue
		}
		if s.ID != "" {
			seenID[s.ID] = true
		}
		seenText[key] = s.ID
		kept = append(kept, s)
	}
	if len(dropped) > 0 {
		doc.Sections = kept
		doc.Raw = artifact.Render(doc)
	}
	return dropped
}

func semanticSectionBody(body string) string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
