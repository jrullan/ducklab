package engineapi

import "testing"

// A stamped tag version ("v0.5.0-24-gabc") and a bare client version
// ("0.5.0") are the same major. The v is tag convention, not version.
func TestVersionSkewIgnoresTheTagPrefix(t *testing.T) {
	for _, pair := range [][2]string{
		{"0.5.0", "v0.5.0-24-gf9294c1"},
		{"v0.5.0", "0.5.0"},
		{"0.4.0", "v0.5.0"}, // same major 0 — minor drift is allowed
	} {
		c := compatibilityMajor(pair[0])
		s := compatibilityMajor(pair[1])
		if c != s {
			t.Errorf("majors differ for %q vs %q: %q != %q", pair[0], pair[1], c, s)
		}
	}
}

func TestVersionSkewTreatsNonSemverBuildStampsAsDevelopmentMajor(t *testing.T) {
	for _, version := range []string{
		"dev",
		"unknown",
		"neocapture-t006-freeze-2026-09-03-8-gecf1611",
	} {
		if got := compatibilityMajor(version); got != "0" {
			t.Errorf("compatibilityMajor(%q) = %q, want 0", version, got)
		}
	}
	if got := compatibilityMajor("v12.3.4-5-gabc"); got != "12" {
		t.Fatalf("release major = %q, want 12", got)
	}
}
