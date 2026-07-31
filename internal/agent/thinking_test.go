package agent

import (
	"strings"
	"testing"
)

// feed runs a splitter over chunks and returns everything it routed each way.
func feed(chunks ...string) (string, string) {
	var s thinkSplitter
	var a, r strings.Builder
	for _, c := range chunks {
		ans, rea := s.Feed(c)
		a.WriteString(ans)
		r.WriteString(rea)
	}
	ans, rea := s.Flush()
	a.WriteString(ans)
	r.WriteString(rea)
	return a.String(), r.String()
}

// Streamed deltas went to the answer untouched, so a model that inlines its
// thinking filled the transcript lane with its own deliberation and bare
// `</think>` markers for the whole run — while the thinking section stayed
// empty. Endpoints that separate reasoning server side hid the problem: the same
// build looked right on OpenRouter and wrong on a local vLLM.
func TestStreamedThinkingIsRoutedAwayFromTheAnswer(t *testing.T) {
	a, r := feed("<think>Let me read the spec.</think>Done: added the handler.")
	if a != "Done: added the handler." {
		t.Errorf("answer = %q", a)
	}
	if r != "Let me read the spec." {
		t.Errorf("reasoning = %q", r)
	}
}

// A marker split across two chunks is the normal case, not an edge case: chunk
// boundaries fall wherever the transport puts them.
func TestAMarkerSplitAcrossChunks(t *testing.T) {
	a, r := feed("<thi", "nk>deliberating</thin", "k>the answer")
	if a != "the answer" {
		t.Errorf("answer = %q — a split marker leaked", a)
	}
	if r != "deliberating" {
		t.Errorf("reasoning = %q", r)
	}
}

// Generation cut mid-thought: everything from the marker on is reasoning, and
// there is no answer to show.
func TestAnUnterminatedBlockIsAllThinking(t *testing.T) {
	a, r := feed("<think>still working it out")
	if a != "" {
		t.Errorf("answer = %q, want nothing", a)
	}
	if r != "still working it out" {
		t.Errorf("reasoning = %q", r)
	}
}

// Some servers open the block in the chat template, so the reply begins inside
// it and only the closing tag ever arrives.
func TestAClosingTagWithNoOpenerMakesEverythingBeforeItThinking(t *testing.T) {
	a, r := feed("Tests pass.\n</think>\n\n**Changed:** `add.go` — a + b.")
	if want := "\n**Changed:** `add.go` — a + b."; a != want {
		t.Errorf("answer = %q, want %q", a, want)
	}
	if strings.TrimSpace(r) != "Tests pass." {
		t.Errorf("reasoning = %q", r)
	}
}

// The tag must be alone on its line. Prose that merely mentions it — the
// documentation for this parser, say — must keep the answer around it. A rule
// that cut at any occurrence silently deleted the first half of real answers.
func TestATagMentionedInProseIsJustText(t *testing.T) {
	in := "I was thinking about the </think> tag in the parser docs."
	a, r := feed(in)
	if a != in {
		t.Errorf("answer = %q, want it untouched", a)
	}
	if r != "" {
		t.Errorf("prose was taken for reasoning: %q", r)
	}
}

// And that decision cannot be made until the line ends, so a tag arriving at a
// chunk boundary must wait rather than be guessed at.
func TestALineAloneTagIsNotDecidedBeforeItsLineEnds(t *testing.T) {
	a, r := feed("Tests pass.\n</think>", " and more prose\n")
	// The tag was not alone on its line after all.
	if !strings.Contains(a, "and more prose") {
		t.Errorf("answer = %q", a)
	}
	if r != "" {
		t.Errorf("reasoning = %q, want none", r)
	}
}

func TestOrdinaryTextIsUntouched(t *testing.T) {
	a, r := feed("**Changed:** add.go — a + b.")
	if a != "**Changed:** add.go — a + b." || r != "" {
		t.Errorf("answer = %q reasoning = %q", a, r)
	}
}

// The assembled response must read the same as the stream, or a turn would say
// different things depending on whether its endpoint could stream.
func TestSplitThinkingAgreesWithTheStream(t *testing.T) {
	for _, in := range []string{
		"<think>a</think>b",
		"reasoning\n</think>\nanswer",
		"plain answer",
		"<think>unterminated",
	} {
		wantA, wantR := feed(in)
		gotA, gotR := splitThinking(in)
		if strings.TrimSpace(wantA) != gotA || strings.TrimSpace(wantR) != gotR {
			t.Errorf("%q: stream gave (%q,%q), assembled gave (%q,%q)", in, wantA, wantR, gotA, gotR)
		}
	}
}

// The exact shape a local vLLM produced while T-012 ran: several think blocks
// across a turn, with the deliberation and the markers landing in the answer
// lane and the thinking section empty.
func TestTheShapeSeenOnALocalVLLM(t *testing.T) {
	a, r := feed(
		"<think>Let me start by understanding the task.\n\n",
		"Let me first read the relevant artifacts.\n</think>\n",
		"<think>\nLet me explore the project structure.\n</think>\n\n",
		"Now let me understand the current state. The canvas has:\n- Triangle rendering\n",
	)
	if strings.Contains(a, "</think>") || strings.Contains(a, "<think>") {
		t.Errorf("markers reached the answer lane: %q", a)
	}
	if !strings.Contains(a, "Now let me understand the current state") {
		t.Errorf("the answer was lost: %q", a)
	}
	if !strings.Contains(r, "understanding the task") || !strings.Contains(r, "explore the project structure") {
		t.Errorf("thinking did not reach the thinking section: %q", r)
	}
}
