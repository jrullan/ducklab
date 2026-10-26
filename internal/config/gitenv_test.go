package config

import (
	"testing"

	"github.com/jrullan/ducklab/internal/gittest"
)

// TestMain makes every git process these tests start ignore the developer's
// global and system git config (B-526): init.defaultBranch, hooks, signing
// or templates on one machine must not decide whether a test passes.
func TestMain(m *testing.M) {
	gittest.Main(m.Run)
}
