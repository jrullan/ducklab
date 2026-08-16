package agent

import "strings"

// repetitionDetector identifies a short n-gram repeated enough times to be
// diagnostic, while allowing ordinary prose to contain repeated words.
type repetitionDetector struct {
 words    []string
 pending  string
 repeated string
}
func newRepetitionDetector() *repetitionDetector { return &repetitionDetector{} }
func (d *repetitionDetector) Add(s string) bool {
 if d.repeated != "" { return true }
 // Keep the final partial word for the next delta. This avoids re-tokenizing
 // the complete accumulated response on every streamed chunk.
 words := strings.Fields(d.pending + s)
 if len(words) == 0 { d.pending += s; return false }
 trailingSpace := len(s) > 0 && strings.ContainsAny(s[len(s)-1:], " \t\r\n")
 d.pending = ""
 if !trailingSpace {
  d.pending = words[len(words)-1]
  words = words[:len(words)-1]
 }
 d.words = append(d.words, words...)
 for n := 3; n <= 12 && n*3 <= len(d.words); n++ {
  a := strings.Join(d.words[len(d.words)-n:], " ")
  if strings.Join(d.words[len(d.words)-2*n:len(d.words)-n], " ") == a && strings.Join(d.words[len(d.words)-3*n:len(d.words)-2*n], " ") == a { d.repeated = a; return true }
 }
 return false
}
func (d *repetitionDetector) Repeated() string { return d.repeated }
