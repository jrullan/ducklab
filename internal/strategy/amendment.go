package strategy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
)

// B-518 (TI-36X r-20261010-121543-eett): an amendment of REQ-008 asked to fix
// one example; the architect deleted the section's whole fraction paragraph.
// The in-loop critic was shown the human request and the post-merge candidate
// but never the section's previous text, so the deletion was invisible to it
// and it approved in five seconds. Only the post-composition reviewer, which
// is given base and candidate, saw it — after the loop had already ended.
//
// AmendmentGuard is how a stage that edits an approved document tells the
// scheduler what "before" was. The stage owns the document grammar (parsing,
// merging, ids); the scheduler owns when the checks run and what the seats are
// told. Nil means a first draft: there is no before, and nothing changes.
type AmendmentGuard struct {
	// Delta renders, for a candidate, every touched section's previous text
	// beside its candidate text, for a document critic. Empty means nothing
	// was touched.
	Delta func(candidate string) string
	// Removals is the deterministic pre-review check: content present in a
	// touched section of the approved base and absent from the candidate,
	// which the request does not authorize. Justifications are the
	// architect's quotes of the request, verified by the stage. Nil skips the
	// check (a route where the request is not the authority over removals).
	Removals func(candidate string, justified []RemovalJustification) RemovalReport
}

// RemovalJustification is an architect's claim that the human request
// authorizes removing content from one section. It must quote the request
// verbatim; the stage verifies the quote, the critic judges whether the quoted
// words really authorize the removal.
type RemovalJustification struct {
	Section string `json:"section"`
	Quote   string `json:"quote"`
}

// RemovalReport is the outcome of the deterministic removal check.
type RemovalReport struct {
	// Findings are unauthorized removals, each naming its section first.
	Findings []string
	// Justified are removals the architect justified with a verified quote of
	// the request. They do not block; the critic is shown them to verify.
	Justified []string
}

// maxRemovalRepairs bounds the architect turns spent on removal findings in
// one round. Restoring a passage is a small edit; a seat that twice neither
// restores nor justifies it will not on a third try, and the critic then
// receives the findings as blocking engine evidence instead.
const maxRemovalRepairs = 2

// removalJustificationRe reads `Removal authorized (REQ-008): "quoted words"`.
// The line lives outside every section, so the merger never persists it; it is
// also stripped from the reply before anything stores the text.
var removalJustificationRe = regexp.MustCompile(`(?mi)^[ \t>*_-]*removal authorized[*_]*\s*\(\s*([A-Za-z]+-\d+)\s*\)\s*[*_]*:\s*[*_]*\s*["“'](.+?)["”']\s*[*_]*[ \t]*$\n?`)

// ParseRemovalJustifications extracts the architect's removal justifications.
func ParseRemovalJustifications(text string) []RemovalJustification {
	var out []RemovalJustification
	for _, m := range removalJustificationRe.FindAllStringSubmatch(text, -1) {
		out = append(out, RemovalJustification{Section: strings.ToUpper(strings.TrimSpace(m[1])), Quote: strings.TrimSpace(m[2])})
	}
	return out
}

// StripRemovalJustifications removes justification lines from a reply so they
// can never reach a stored document.
func StripRemovalJustifications(text string) string {
	return removalJustificationRe.ReplaceAllString(text, "")
}

func mergeJustifications(have, add []RemovalJustification) []RemovalJustification {
	for _, next := range add {
		dup := false
		for _, old := range have {
			if strings.EqualFold(old.Section, next.Section) && old.Quote == next.Quote {
				dup = true
				break
			}
		}
		if !dup {
			have = append(have, next)
		}
	}
	return have
}

// removalRepairNote is the architect's instruction after the deterministic
// check. It names the exact words, because "you deleted something" sends a
// small seat re-reading the whole section to guess what.
func removalRepairNote(findings []string) string {
	var b strings.Builder
	b.WriteString("## Removal check — repair or justify before review\n\n")
	b.WriteString("Ducklab compared your candidate with the APPROVED text of every section it touches. " +
		"The passages below were in the approved section and are absent from your candidate (rewording is fine; " +
		"this check tolerates it). The request does not ask for their removal.\n\n")
	for _, f := range findings {
		b.WriteString("- " + f + "\n")
	}
	b.WriteString("\nFor EACH passage, do one of:\n\n" +
		"1. Restore it in its section (you may reword it), and re-emit that section in full under its existing id.\n" +
		"2. Only if the human request explicitly asks for that removal, keep it removed and put one line BEFORE your first section, " +
		"quoting the request word for word:\n\n" +
		"   Removal authorized (REQ-008): \"exact words copied from the request\"\n\n" +
		"   Ducklab verifies the quote against the request; a paraphrase is rejected. The line is never stored in the document.\n\n" +
		"Change nothing else.\n")
	return b.String()
}

// amendmentCriticContext is what a document critic is told about the base the
// amendment edits: the per-section before/after, the justified removals, and
// the removals the architect left unresolved.
func amendmentCriticContext(guard *AmendmentGuard, candidate string, justified, unresolved []string) string {
	if guard == nil {
		return ""
	}
	var b strings.Builder
	if guard.Delta != nil {
		if delta := strings.TrimSpace(guard.Delta(candidate)); delta != "" {
			b.WriteString("\n\n" + delta)
		}
	}
	if len(justified) > 0 {
		b.WriteString("\n\n## Removals the architect justified — verify each quote\n\n" +
			"The architect kept these removals and quoted the request as authority. The quote is verbatim; judge whether those words really ask for this removal. If they do not, request changes.\n\n")
		for _, j := range justified {
			b.WriteString("- " + j + "\n")
		}
	}
	if len(unresolved) > 0 {
		b.WriteString("\n\n## Engine-detected unauthorized removals — blocking\n\n" +
			"These passages were removed from the approved text, the request does not authorize it, and the architect neither restored nor justified them. " +
			"They are deterministic findings and block approval; Ducklab adds them to your verdict.\n\n")
		for _, f := range unresolved {
			b.WriteString("- " + f + "\n")
		}
	}
	return b.String()
}

// lowerVerdictForRemovals turns unresolved removals into blocking findings on a
// critic's verdict, like an unassigned coverage slot lowers a manifest critic.
func lowerVerdictForRemovals(v *agent.Verdict, unresolved []string) bool {
	if v == nil || len(unresolved) == 0 {
		return false
	}
	v.Verdict = "request-changes"
	for _, f := range unresolved {
		file := "*"
		if i := strings.IndexByte(f, ':'); i > 0 {
			file = strings.TrimSpace(f[:i])
		}
		v.Findings = append(v.Findings, agent.Finding{
			Severity:  "major",
			File:      file,
			Invariant: "An amendment keeps approved content its request does not remove",
			Issue:     f,
			Fix:       "restore the passage in its section, or quote the words of the request that authorize removing it",
		})
	}
	return true
}

func removalSignature(findings []string) string {
	return fmt.Sprint(len(findings)) + "\n" + strings.Join(findings, "\n")
}
