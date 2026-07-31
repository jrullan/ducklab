package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func readIn(t *testing.T, body, args string) *Result {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := (&FSRead{}).Execute(context.Background(),
		&ExecContext{ProjectRoot: root}, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A slice was numbered from 1, so a model that asked for lines 830-905 was shown
// them as 1-76 and could not correlate one read with the next — or with a
// search result, or with a patch it wanted to place.
func TestASliceKeepsItsRealLineNumbers(t *testing.T) {
	body := ""
	for i := 1; i <= 200; i++ {
		body += "line " + strconv.Itoa(i) + "\n"
	}
	res := readIn(t, body, `{"path":"index.html","start":150,"end":152}`)
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !strings.Contains(res.Content, "150\tline 150") {
		t.Errorf("the slice does not carry its own line numbers:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "  1\tline 150") {
		t.Errorf("the slice restarted numbering at 1:\n%s", res.Content)
	}
}

// index.html is 57 KB against a 32 KB result ceiling, so a plain read came back
// tail-biased and measured in bytes while this tool speaks lines. A model given
// that guesses windows: one spent all 24 of its turns on 15 reads and 6 searches
// of a single file and never wrote a line.
func TestATruncatedReadSaysWhichLinesAndHowToGetTheRest(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 4000; i++ {
		b.WriteString("a fairly long line of source code number " + strconv.Itoa(i) + "\n")
	}
	res := readIn(t, b.String(), `{"path":"index.html"}`)
	if res.IsError {
		t.Fatal(res.Content)
	}
	// Head, not tail: a file is read from the top, and a reader handed the end
	// of one cannot tell what came before it.
	if !strings.Contains(res.Content, "   1\ta fairly long line of source code number 1") {
		t.Errorf("the read is not head-biased:\n%s", res.Content[:200])
	}
	if !strings.Contains(res.Content, "of 4001") {
		t.Errorf("the result does not say how long the file is:\n%s", tail(res.Content))
	}
	// The exact next call to make, in the tool's own vocabulary.
	if !strings.Contains(res.Content, `"start":`) || !strings.Contains(res.Content, `"path":"index.html"`) {
		t.Errorf("the result does not say how to get the rest:\n%s", tail(res.Content))
	}
}

// The whole point of the cap is that the result fits.
func TestATruncatedReadFitsTheCeiling(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 5000; i++ {
		b.WriteString(strings.Repeat("x", 60) + "\n")
	}
	res := readIn(t, b.String(), `{"path":"index.html"}`)
	if len(res.Content) > MaxToolResultBytes {
		t.Errorf("result is %d bytes against a ceiling of %d", len(res.Content), MaxToolResultBytes)
	}
}

// A file that fits says nothing about truncation, or every read would carry a
// footnote nobody needs.
func TestAWholeFileIsQuiet(t *testing.T) {
	res := readIn(t, "one\ntwo\nthree\n", `{"path":"index.html"}`)
	if strings.Contains(res.Content, "showing lines") {
		t.Errorf("a complete read claims to be truncated:\n%s", res.Content)
	}
}

func tail(s string) string {
	if len(s) < 300 {
		return s
	}
	return s[len(s)-300:]
}
