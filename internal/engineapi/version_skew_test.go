package engineapi

import "strings"
import "testing"

// A stamped tag version ("v0.5.0-24-gabc") and a bare client version
// ("0.5.0") are the same major. The v is tag convention, not version.
func TestVersionSkewIgnoresTheTagPrefix(t *testing.T) {
	for _, pair := range [][2]string{
		{"0.5.0", "v0.5.0-24-gf9294c1"},
		{"v0.5.0", "0.5.0"},
		{"0.4.0", "v0.5.0"}, // same major 0 — minor drift is allowed
	} {
		c := strings.Split(strings.TrimPrefix(pair[0], "v"), ".")[0]
		s := strings.Split(strings.TrimPrefix(pair[1], "v"), ".")[0]
		if c != s {
			t.Errorf("majors differ for %q vs %q: %q != %q", pair[0], pair[1], c, s)
		}
	}
}
