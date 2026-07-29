package service

import (
	"strings"
	"testing"
)

// Both components arrive from a client. A stamp of "../../../etc/passwd" must
// not read anything outside the bench directory.
func TestBenchGetRefusesToEscapeItsDirectory(t *testing.T) {
	s := &Service{}
	for _, c := range []struct{ suite, stamp string }{
		{"std", "../../../etc/passwd"},
		{"../..", "x"},
		{"std", "sub/dir"},
		{"std/../..", "x"},
	} {
		if _, err := s.BenchGet(c.suite, c.stamp); err == nil {
			t.Errorf("BenchGet(%q, %q) was allowed", c.suite, c.stamp)
		} else if !strings.Contains(err.Error(), "bad suite or stamp") {
			t.Errorf("BenchGet(%q, %q) failed for the wrong reason: %v", c.suite, c.stamp, err)
		}
	}
}
