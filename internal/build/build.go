// Package build holds the version the binaries report.
//
// It lives in one place because it used to live in three — cmd/ducklab,
// cmd/ducklab-engine and internal/cli each declared their own constant, all
// three still said 0.1.0 at v0.4, and the engine's version-skew check compares
// client against server. Three independently-editable copies of the number two
// programs use to decide whether they are compatible is a bug waiting for a
// release, not a style problem.
package build

import "strings"

// Version is the release these binaries belong to.
var Version = "dev"

// Semver returns Version without the tag's v prefix or describe suffix noise
// kept intact — "v0.5.0-24-gf9294c1" reports as "0.5.0-24-gf9294c1". The tag
// convention carries a v; semver comparison does not, and the skew check
// splitting on "." turned the mismatch into "0" != "v0" — every client locked
// out by a letter.
func Semver() string { return strings.TrimPrefix(Version, "v") }

// These values are stamped by the build (see Makefile). Defaults keep source
// builds useful while making an installed binary's origin explicit.
var Branch = "dev"
var Commit = "unknown"

// Provenance is the source branch and commit used to build this binary.
func Provenance() string { return Branch + "@" + Commit }

// Dirty reports whether this binary was built from a working tree that
// differed from the commit it is stamped with.
//
// The stamp used to be a lie by construction: ldflags read `git rev-parse
// HEAD` while the compiler read the working tree, so a binary built over a
// stale checkout wore a clean sha. Measured cost of that lie: an engine
// stamped with a fix's commit served the pre-fix code, "reproduced" the very
// bug the commit fixed, and an evening went to chasing a phantom regression.
// The Makefile now stamps -dirty into Version and Commit whenever the tree
// differs from HEAD; this is the one place that reads the marker.
func Dirty() bool {
	return strings.HasSuffix(Version, "-dirty") || strings.HasSuffix(Commit, "-dirty")
}
