package engineapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// decisionNoteRefusal is what a decision that takes no note says when one is
// sent. Each names the door that does carry the words.
//
// B-524: these bodies were decoded into structs with no note field, so a note
// on accept, reject or abort was discarded by the JSON decoder and the call
// succeeded as if the words had been read.
func decisionNoteRefusal(action string) string {
	const only = "only resume carries a note (a paused build or test run, outside its gate)"
	switch action {
	case "accept":
		return "accept takes no note: " + only + ". Accept's message is recorded with the decision and changes nothing about what is accepted. " +
			"To change a triage proposal, accept the triage and edit the bug's proposal with bug_edit before promoting it; " +
			"to change a document draft, request changes with your words"
	case "reject":
		return "reject takes no note: " + only + ". Put your words in reason; it is recorded with the rejection"
	case "abort":
		return "abort takes no note: " + only + ". Put your words in reason; it is recorded with the abort"
	}
	return fmt.Sprintf("%s takes no note: %s", action, only)
}

// readDecisionBody returns an optional decision body's bytes, refusing a
// non-empty `note` before anything is decided. The body is read whole so the
// caller decodes it exactly as it did before.
func readDecisionBody(r *http.Request, action string) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var probe struct {
		Note *string `json:"note"`
	}
	if json.Unmarshal(raw, &probe) == nil && probe.Note != nil && strings.TrimSpace(*probe.Note) != "" {
		return nil, fmt.Errorf("%s", decisionNoteRefusal(action))
	}
	return raw, nil
}
