package report

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

// The average says which model is expensive to run once; the total says where
// the project's money went. A cheap model called constantly can out-spend an
// expensive one used sparingly, and neither column reveals that alone.
func TestRenderShowsTheTotalBesideTheAverage(t *testing.T) {
	rep := Build([]*runlog.Run{
		{Verdict: "PASSED", Stage: "build", Roster: map[string]string{"implementer": "pato-sonnet"},
			Spend: map[string]runlog.DucklingSpend{"pato-sonnet": {Calls: 4, Tokens: 1000, CostUSD: 0.30}}},
		{Verdict: "PASSED", Stage: "build", Roster: map[string]string{"implementer": "pato-sonnet"},
			Spend: map[string]runlog.DucklingSpend{"pato-sonnet": {Calls: 4, Tokens: 1000, CostUSD: 0.60}}},
	}, Options{By: "duckling"})

	out := Render(rep)
	if !strings.Contains(out, "total_usd") {
		t.Fatalf("no total_usd column:\n%s", out)
	}
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "pato-sonnet") {
			line = l
		}
	}
	if !strings.Contains(line, "0.9000") || !strings.Contains(line, "0.4500") {
		t.Errorf("want total 0.9000 and average 0.4500 on the row:\n%s", line)
	}
}
