package agent

import "strings"

// Separating an inline reasoning block from the answer, as it arrives.
//
// stripThinking handles the assembled response, and it works: the recorded
// message comes out clean. But the streamed deltas were routed to the answer
// untouched, so a model that inlines its thinking filled the transcript lane
// with its own deliberation and bare `</think>` markers while the run was live —
// and the thinking section stayed empty, because stripThinking DELETES the block
// rather than handing it over.
//
// Endpoints that put reasoning in its own field were unaffected, which is why
// this looked like it worked: one duckling on OpenRouter separated it server
// side and a local vLLM did not.

const (
	openTag  = "<think>"
	closeTag = "</think>"
)

// thinkSplitter routes streamed text to the answer or to the reasoning as the
// chunks arrive. One per model call: it carries state across chunk boundaries,
// because a marker can be split between two of them.
type thinkSplitter struct {
	inThink bool
	// pending holds a tail that might be the start of a marker, or a line-alone
	// closing tag whose newline has not arrived yet. Emitting it would put a
	// half-written `</thi` on screen and, worse, decide its meaning too early.
	pending string
}

// Feed returns what to show as the answer and what to show as reasoning.
func (s *thinkSplitter) Feed(chunk string) (string, string) {
	var answer, reasoning strings.Builder
	buf := s.pending + chunk
	s.pending = ""

	for {
		if s.inThink {
			i := strings.Index(buf, closeTag)
			if i < 0 {
				break
			}
			reasoning.WriteString(buf[:i])
			buf = buf[i+len(closeTag):]
			s.inThink = false
			continue
		}

		open := strings.Index(buf, openTag)
		// A closing tag with no opening one: some servers open the block in the
		// chat template, so the reply begins inside it and only the closer ever
		// appears. It counts only when alone on its line — prose that merely
		// mentions the tag, documentation about this parser for one, must not
		// have the answer around it eaten.
		closeAt, closeEnd, closeOK := lineAloneClose(buf)

		switch {
		case open < 0 && !closeOK:
			// Nothing decidable yet.
		case closeOK && (open < 0 || closeAt < open):
			reasoning.WriteString(buf[:closeAt])
			buf = buf[closeEnd:]
			continue
		default:
			answer.WriteString(buf[:open])
			buf = buf[open+len(openTag):]
			s.inThink = true
			continue
		}
		break
	}

	keep := heldBack(buf, s.inThink)
	emit := buf[:len(buf)-keep]
	s.pending = buf[len(buf)-keep:]
	if s.inThink {
		reasoning.WriteString(emit)
	} else {
		answer.WriteString(emit)
	}
	return answer.String(), reasoning.String()
}

// Flush releases what was held back once the stream is over. A partial marker
// that never completed was only ever text.
func (s *thinkSplitter) Flush() (string, string) {
	rest := s.pending
	s.pending = ""
	if s.inThink {
		return "", rest
	}
	return rest, ""
}

// lineAloneClose finds a closing tag that is the only thing on its line, and
// reports where the line begins and where the following one does.
func lineAloneClose(buf string) (start, end int, ok bool) {
	from := 0
	for {
		i := strings.Index(buf[from:], closeTag)
		if i < 0 {
			return 0, 0, false
		}
		i += from
		lineStart := strings.LastIndexByte(buf[:i], '\n') + 1
		if strings.TrimSpace(buf[lineStart:i]) != "" {
			from = i + len(closeTag)
			continue
		}
		nl := strings.IndexByte(buf[i:], '\n')
		if nl < 0 {
			// The line has not ended, so whether the tag is alone on it is not
			// yet knowable. Wait rather than guess.
			return 0, 0, false
		}
		// And nothing after it either. Checking only what came before let
		// "</think> and more prose" pass as a line-alone tag, which would have
		// taken the prose before it for reasoning.
		if strings.TrimSpace(buf[i+len(closeTag):i+nl]) != "" {
			from = i + len(closeTag)
			continue
		}
		return lineStart, i + nl + 1, true
	}
}

// heldBack reports how many trailing bytes must wait for more input.
//
// Two reasons to wait: the tail could be the first bytes of a marker, or it
// could be a complete closing tag whose line has not ended — and outside a
// think block, whether that tag is alone on its line decides whether the text
// before it was reasoning or answer.
func heldBack(buf string, inThink bool) int {
	if !inThink {
		if i := strings.LastIndex(buf, closeTag); i >= 0 {
			lineStart := strings.LastIndexByte(buf[:i], '\n') + 1
			if strings.TrimSpace(buf[lineStart:i]) == "" {
				return len(buf) - lineStart
			}
		}
	}
	for n := len(closeTag) - 1; n > 0; n-- {
		if n > len(buf) {
			continue
		}
		tail := buf[len(buf)-n:]
		if strings.HasPrefix(closeTag, tail) || strings.HasPrefix(openTag, tail) {
			return n
		}
	}
	return 0
}

// splitThinking separates an assembled response into answer and reasoning.
//
// Same rules as the streaming splitter, so a turn reads the same whether its
// endpoint streamed or not.
func splitThinking(content string) (answer, reasoning string) {
	var s thinkSplitter
	a, r := s.Feed(content)
	af, rf := s.Flush()
	return strings.TrimSpace(a + af), strings.TrimSpace(r + rf)
}

// joinReasoning appends an inline block to whatever the endpoint had already
// separated. A response can carry both: a server that splits most of the
// reasoning out can still leave a stray block in the content.
func joinReasoning(existing, more string) string {
	if existing == "" {
		return more
	}
	if more == "" {
		return existing
	}
	return existing + "\n" + more
}
