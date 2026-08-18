package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/bench"
	"github.com/jrullan/ducklab/internal/report"
	"github.com/jrullan/ducklab/internal/runlog"
)

func TestScorecardEvidenceUsesDucklingReportAndLeavesNoEvidenceAbsent(t *testing.T) {
	s := writableService(t, "measured", "unused")
	projectID, _ := projectWithConfig(t, s, "scorecard-evidence")
	s.runs["r-scorecard"] = &runState{run: &runlog.Run{ID: "r-scorecard", ProjectID: projectID, Stage: "build", Verdict: "PASSED", WallclockMs: 2_000, Spend: map[string]runlog.DucklingSpend{"measured": {Calls: 1, Tokens: 1_200, CostUSD: 0.75}}}}
	rep, err := s.Report(context.Background(), projectID, report.Options{By: "duckling"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 1 || rep.Rows[0].Key != "measured" || rep.Rows[0].Runs != 1 || rep.Rows[0].PassRate() != 100 || rep.Rows[0].AvgCost() != 0.75 {
		t.Fatalf("measured evidence = %+v", rep.Rows)
	}
}

func TestIsLocalHostClassifiesPrivateAndPublicEndpoints(t *testing.T) {
	for _, tc := range []struct {
		url   string
		local bool
	}{
		{"http://127.0.0.1:1", true}, {"http://localhost", true}, {"http://[::1]", true}, {"http://10.2.3.4", true}, {"http://172.16.0.1", true}, {"http://172.31.255.254", true}, {"http://192.168.1.1", true}, {"http://duck.local", true}, {"https://api.example.com/v1", false},
	} {
		if got := IsLocalHost(tc.url); got != tc.local {
			t.Errorf("IsLocalHost(%q) = %v, want %v", tc.url, got, tc.local)
		}
	}
}

func TestScorecardsLatestBenchPerSuite(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "ducklab", "bench", "std")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(stamp, started, verdict string) {
		data, _ := json.Marshal(bench.Result{Suite: "std", SuiteVersion: 1, StartedAt: started, Cells: []bench.Cell{{Duckling: "pato-uno", Verdict: verdict}}})
		if err := os.WriteFile(filepath.Join(root, stamp+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("old", "2026-01-01T00:00:00Z", "FAILED")
	write("new", "2026-01-02T00:00:00Z", "PASSED")
	s := serviceWithDucklings(t, "pato-uno", "unused")
	t.Setenv("XDG_DATA_HOME", dir)
	if got := loadLatestBench(); len(got) == 0 {
		t.Fatalf("bench files not loaded from %s", root)
	}
	cards, err := s.Scorecards(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found, absent bool
	for _, c := range cards {
		if c.ID == "pato-uno" {
			found = c.Bench["std"].Verdict == "PASSED"
		}
		if c.ID == "unused" {
			absent = c.Bench == nil
		}
	}
	if !found || !absent {
		t.Fatalf("latest/absent bench = %+v", cards)
	}
}
