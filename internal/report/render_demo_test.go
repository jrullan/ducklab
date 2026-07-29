package report

import (
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

// Renders the exact shape of the calculator project's runs, so the corrected
// comparison can be read before an engine restart makes it visible.
func TestRenderTheReportedShape(t *testing.T) {
	r := func(mode, stage, verdict string) *runlog.Run {
		return &runlog.Run{Mode: mode, Stage: stage, Verdict: verdict, StartedAt: "2026-07-29T12:00:00Z"}
	}
	t.Log("\n" + Render(Build([]*runlog.Run{
		r("solo", "build", "PASSED"), r("solo", "build", "PASSED"),
		r("solo", "build", "PASSED"), r("solo", "build", "FAILED"),
		r("council", "intake", "UNVERIFIED"), r("council", "spec", "UNVERIFIED"),
		r("council", "spec", "FAILED"), r("council", "plan", "UNVERIFIED"),
		r("pair", "build", "PASSED"), r("pair", "build", "PASSED"),
		r("tournament", "build", "FAILED"),
	}, Options{By: "mode"})))
}
