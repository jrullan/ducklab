package service

import "testing"

// B-376: each request_changes supersedes the preceding run. Its persisted
// request must therefore carry every earlier narrowing into the next round.
func TestRequestChangesRevisionChainPreservesEveryOperatorNote(t *testing.T) {
	second := appendRevision("do not touch the task table", "add the missing dependency")
	third := appendRevision(second, "keep the verification command unchanged")
	want := "do not touch the task table\n\nadd the missing dependency\n\nkeep the verification command unchanged"
	if third != want {
		t.Fatalf("revision chain = %q, want %q", third, want)
	}
}
