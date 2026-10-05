package service

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// TI-36X T-005 (B-498): the writer offered four options and the person typed
// "Use option 2". The answer was recorded as typed, the replayed prompt said
// "A: Use option 2" with no options beside it, and half of the decision
// (statusRow) was lost. An answer that selects an offered option is recorded
// as that option's full text; anything else stays exactly as typed.

// optionLeadIns are the words a person puts before an option reference when
// the whole answer is only a choice. An allowlist, not "any short prefix":
// "not option 2" and "anything but option 2" must never select option 2.
var optionLeadIns = []string{
	"", "use", "pick", "choose", "select", "take", "go with", "going with",
	"let's go with", "lets go with", "i'll go with", "i'll take", "i choose",
	"i pick", "i prefer", "prefer", "answer", "my answer is",
	"usa", "usar", "elijo", "escojo", "elige", "escoge", "prefiero",
	"vamos con", "me quedo con", "respuesta",
}

// optionWords name the option itself, in English and Spanish.
var optionWords = []string{
	"option", "opción", "opcion", "choice", "alternative", "alternativa", "number", "número", "numero", "no.", "#",
}

var (
	// "2", "2.", "(2)", "#2", "B", "b)" once lead-in and option word are gone.
	optionRefPattern = regexp.MustCompile(`^[#(]?\s*([0-9]+|[a-z])\s*[).:]?$`)
	// "A) text", "(b) text", "c. text", "d: text" — an option that carries its
	// own letter. Options are lettered only when every one carries the next.
	optionLetterLabel = regexp.MustCompile(`^\(?([A-Za-z])[).:]\s+`)
	// "1. text", "2) text" — a leading number label, ignored when comparing an
	// echoed answer with the option it echoes.
	optionNumberLabel = regexp.MustCompile(`^\(?[0-9]+[).:]\s+`)
	// "Option 2: text", "opción B - text".
	optionEchoLabel = regexp.MustCompile(`(?i)^(?:option|opción|opcion)\s*#?\s*([0-9]+|[a-z])\s*[:.)\-–—]\s*(.+)$`)
)

// resolveOptionAnswer maps an answer that selects one of the offered options
// to that option's full text. It returns the 1-based option number, or 0 when
// the answer is not unambiguously one option: no options, a number outside
// the list, a letter when the options are not lettered, text that matches
// two options, or anything with more to say than the choice itself.
func resolveOptionAnswer(answer string, options []string) (string, int) {
	if len(options) == 0 {
		return answer, 0
	}
	if n := optionByReference(answer, options); n > 0 {
		return options[n-1], n
	}
	if n := optionByEcho(answer, options); n > 0 {
		return options[n-1], n
	}
	return answer, 0
}

// optionByReference handles "2", "option 2", "Use option 2", "opción 2",
// "la 2", and a letter when the options are lettered.
func optionByReference(answer string, options []string) int {
	text := strings.ToLower(strings.Join(strings.Fields(answer), " "))
	text = strings.TrimRight(text, ".!¡ ")
	text = strings.TrimSpace(strings.TrimPrefix(text, "¡"))
	for _, lead := range optionLeadIns {
		rest, ok := cutWords(text, lead)
		if !ok {
			continue
		}
		// "the option 2", "la opción 2", "el 2".
		for _, article := range []string{"", "the", "la", "el"} {
			afterArticle, ok := cutWords(rest, article)
			if !ok {
				continue
			}
			for _, word := range append([]string{""}, optionWords...) {
				ref, ok := cutWords(afterArticle, word)
				if !ok {
					continue
				}
				if n := optionNumberFor(ref, options); n > 0 {
					return n
				}
			}
		}
	}
	return 0
}

// cutWords removes prefix from text when it is a whole-word prefix; the empty
// prefix always matches.
func cutWords(text, prefix string) (string, bool) {
	if prefix == "" {
		return text, true
	}
	if text == prefix {
		return "", true
	}
	if strings.HasPrefix(text, prefix+" ") {
		return strings.TrimSpace(text[len(prefix)+1:]), true
	}
	// "#2" and "no.2" carry no space after the option word.
	if (prefix == "#" || prefix == "no.") && strings.HasPrefix(text, prefix) {
		return strings.TrimSpace(text[len(prefix):]), true
	}
	return "", false
}

// optionNumberFor reads a bare reference. A number must name an offered
// option; a letter counts only when the options are lettered.
func optionNumberFor(ref string, options []string) int {
	m := optionRefPattern.FindStringSubmatch(ref)
	if m == nil {
		return 0
	}
	if n, err := strconv.Atoi(m[1]); err == nil {
		if n >= 1 && n <= len(options) {
			return n
		}
		return 0
	}
	letters := optionLetters(options)
	if n, ok := letters[m[1]]; ok {
		return n
	}
	return 0
}

// optionLetters maps "a", "b", … to option numbers when every option opens
// with its letter in order; otherwise nil.
func optionLetters(options []string) map[string]int {
	out := map[string]int{}
	for i, option := range options {
		m := optionLetterLabel.FindStringSubmatch(strings.TrimSpace(option))
		if m == nil || strings.ToLower(m[1]) != string(rune('a'+i)) {
			return nil
		}
		out[strings.ToLower(m[1])] = i + 1
	}
	return out
}

// optionByEcho handles an answer that repeats an option — copied whole, with
// its label, or cut short at the end — when exactly one option fits.
func optionByEcho(answer string, options []string) int {
	// "Option 2: <text>" — the label and the text must name the same option;
	// a label that disagrees with the text it carries is not a choice.
	if m := optionEchoLabel.FindStringSubmatch(strings.TrimSpace(answer)); m != nil {
		labelled := optionNumberFor(strings.ToLower(m[1]), options)
		if labelled == 0 || echoedOption(m[2], options) != labelled {
			return 0
		}
		return labelled
	}
	return echoedOption(answer, options)
}

func echoedOption(answer string, options []string) int {
	got := echoKey(answer)
	if got == "" {
		return 0
	}
	exact, prefix := 0, 0
	exactCount, prefixCount := 0, 0
	for i, option := range options {
		want := echoKey(option)
		if want == "" {
			continue
		}
		if got == want {
			exact, exactCount = i+1, exactCount+1
			continue
		}
		// A copy cut short: long enough to mean something and most of the
		// option, so "Use named imports" does not pick a longer option that
		// merely starts the same way.
		if len([]rune(got)) >= 12 && strings.HasPrefix(want, got) && len(got)*10 >= len(want)*6 {
			prefix, prefixCount = i+1, prefixCount+1
		}
	}
	switch {
	case exactCount == 1:
		return exact
	case exactCount == 0 && prefixCount == 1:
		return prefix
	}
	return 0
}

// echoKey normalizes text for echo comparison: no leading label, lower case,
// letters and digits only, single spaces.
func echoKey(text string) string {
	text = optionLetterLabel.ReplaceAllString(strings.TrimSpace(text), "")
	text = optionNumberLabel.ReplaceAllString(text, "")
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
			continue
		}
		space = true
	}
	return b.String()
}
