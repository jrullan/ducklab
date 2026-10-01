package main

import (
	"strings"
	"testing"
)

// Review of #120: the drawer announced images while Browse… could only pick
// .md and .txt. The default filter must cover every reference type the
// engine accepts.
func TestTheReferenceFilterAcceptsEveryReferenceType(t *testing.T) {
	for _, ext := range []string{".md", ".txt", ".json", ".png", ".jpg", ".jpeg", ".webp", ".gif"} {
		if !strings.Contains(referenceFilePattern, "*"+ext) {
			t.Errorf("reference filter lacks %s: %s", ext, referenceFilePattern)
		}
	}
}
