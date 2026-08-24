package report

import (
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

// A manual landing is accepted work, even though the old reject-only door left
// its historical verdict FAILED. It must not poison the evidence used to seat
// the duckling that did the work.
func TestLandedResolutionDoesNotLowerPassRate(t *testing.T) {
	runs := []*runlog.Run{
		{
			Mode:       "solo",
			Stage:      "build",
			Verdict:    "PASSED",
			Spend:      map[string]runlog.DucklingSpend{"terra": {Calls: 1}},
			Resolution: "accepted",
		},
		{
			Mode:       "solo",
			Stage:      "build",
			Verdict:    "FAILED",
			Resolution: "landed",
			NoChanges:  true,
			Spend:      map[string]runlog.DucklingSpend{"terra": {Calls: 1}},
		},
	}

	rep := Build(runs, Options{By: "duckling"})
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %+v, want one terra row", rep.Rows)
	}
	row := rep.Rows[0]
	if row.Key != "terra" {
		t.Fatalf("row key = %q, want terra", row.Key)
	}
	if row.PassRate() != 100 {
		t.Errorf("landed run lowered terra pass rate to %.1f%%, want 100%%", row.PassRate())
	}
	if row.Failed != 0 {
		t.Errorf("landed run counted as failed: %+v", row)
	}
	if row.NoChangePasses != 1 {
		t.Errorf("landed no-change run was not excluded from the pass-rate denominator: %+v", row)
	}
}
