package stage

import "strings"

// revisionContextHeading opens the context a revision's prompt carries.
const revisionContextHeading = "## The request this revision continues (context, not the assignment)"

// withRevisionContext gives a revision's prompt the request it continues
// (B-521). A revision of an existing document updates by fragment, and its
// assignment — "the request" — is the revision note alone; the seed, which
// carries the original brief and the run's references, was dropped. The
// TI-36X revisions were relaunched with their guidebook and still never saw
// it, so a scope reference inherited into the revision's request stopped
// being scope where it mattered: in the prompt.
//
// The seed rides as context below the assignment, never inside it: the ask
// is what amendment guards authorize changes against, and widening it to the
// whole brief would let a revision rewrite what the person did not ask to
// change. Empty for a fresh brief, whose ask already is the seed.
func withRevisionContext(prompt string, p Params) string {
	seed := strings.TrimSpace(p.Seed)
	if strings.TrimSpace(p.Revision) == "" || seed == "" {
		return prompt
	}
	return strings.TrimRight(prompt, "\n") + "\n\n" + revisionContextHeading + "\n\n" +
		"The request above is what to change in this revision. Below is the original request and the " +
		"reference documents it was made with — read them to make the change right; do not treat them " +
		"as a new assignment.\n\n" + seed + "\n"
}
