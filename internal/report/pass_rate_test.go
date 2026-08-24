package report

import (
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

func TestDocumentAcceptanceAndBuildVerdictsDrivePassRate(t *testing.T) {
	scribeRuns := make([]*runlog.Run, 0, 8)
	for range 7 {
		scribeRuns = append(scribeRuns, &runlog.Run{
			Stage: "release", Verdict: "UNVERIFIED", Accepted: true,
			Spend:  map[string]runlog.DucklingSpend{"scribe": {Calls: 1}},
			Roster: map[string]string{"scribe": "scribe"},
		})
	}
	// A document replaced by its revision is neutral evidence.
	scribeRuns = append(scribeRuns, &runlog.Run{
		Stage: "release", Verdict: "UNVERIFIED", Resolution: "superseded",
		Spend:  map[string]runlog.DucklingSpend{"scribe": {Calls: 1}},
		Roster: map[string]string{"scribe": "scribe"},
	})

	row := findRow(Build(scribeRuns, Options{By: "duckling"}).Rows, "scribe")
	if row == nil || row.PassRate() != 100 || row.Effective() != 7 {
		t.Fatalf("scribe evidence = %+v, want 7 accepted releases at 100%%", row)
	}
	byRole := findRow(Build(scribeRuns, Options{By: "duckling_role"}).Rows, "scribe/scribe")
	if byRole == nil || byRole.PassRate() != 100 {
		t.Fatalf("scribe role evidence = %+v, want 100%%", byRole)
	}

	buildRuns := make([]*runlog.Run, 8)
	for i := range buildRuns {
		buildRuns[i] = &runlog.Run{
			Stage: "build", Verdict: "FAILED", Accepted: true,
			Spend: map[string]runlog.DucklingSpend{"implementer": {Calls: 1}},
		}
	}
	implementer := findRow(Build(buildRuns, Options{By: "duckling"}).Rows, "implementer")
	if implementer == nil || implementer.PassRate() != 0 {
		t.Fatalf("implementer evidence = %+v, want 0%% from 0/8 green gates", implementer)
	}
}
