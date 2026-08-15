package agent

import (
	"errors"
	"strings"
)

// repetitionDetector watches assembled stream deltas for a short phrase that
// repeats enough times to be a generation loop. It deliberately works on
// words rather than bytes so chunk boundaries do not hide a loop.
type repetitionDetector struct {
	text string
	seen int
	loop string
}

func newRepetitionDetector() *repetitionDetector { return &repetitionDetector{} }

func (d *repetitionDetector) Add(delta string) bool {
	d.text += delta
	words := strings.Fields(d.text)
	if len(words) < 12 {
		return false
	}
	for n := 3; n <= 8 && n*3 <= len(words); n++ {
		last := strings.Join(words[len(words)-n:], " ")
		prev := strings.Join(words[len(words)-2*n:len(words)-n], " ")
		if last == prev {
			d.seen++
			d.loop = last
			return d.seen >= 2
		}
	}
	d.seen = 0
	return false
}

// Loop returns the repeated phrase, if one has been detected.
func (d *repetitionDetector) Loop() string { return d.loop }

// ErrRepetitionLoop identifies a stream cut caused by repeated output.
var ErrRepetitionLoop = errors.New("repetition loop detected")
