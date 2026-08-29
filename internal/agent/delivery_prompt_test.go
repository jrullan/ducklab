package agent

import (
	"strings"
	"testing"
)

// Document seats have no write tool by design; the brief must say how the
// artifact is delivered, or a small model asks — as one did, pausing an
// intake to ask which tool writes requirements (Neocapture, 2026-08-29).
func TestDocumentSeatsAreToldTheirReplyIsTheArtifact(t *testing.T) {
	for name, prompt := range map[string]string{"architect": architectPrompt, "scribe": scribePrompt} {
		if !strings.Contains(prompt, "final reply IS the document") || !strings.Contains(prompt, "no write tool") {
			t.Errorf("%s brief does not say the reply is the document:\n%s", name, prompt)
		}
	}
}
