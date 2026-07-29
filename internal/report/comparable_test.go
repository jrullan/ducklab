package report

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

func stageRun(mode, stage, verdict string) *runlog.Run {
	return &runlog.Run{Mode: mode, Stage: stage, Verdict: verdict, StartedAt: "2026-07-29T12:00:00Z"}
}

// Reported from a real session, reading a real report:
//
//	solo baseline: 75.0% passed (n=4)
//	council:       0.0% passed  (-75.0 pts, n=4)
//
// The four council runs were intake, spec and plan. They write documents, have
// no executable gate, and end UNVERIFIED by design — so council's pass rate is
// zero forever, and "75 points worse than solo" is not a finding about council
// but a comparison of two different jobs.
func TestArtifactStagesAreNotComparedAgainstBuildModes(t *testing.T) {
	rep := Build([]*runlog.Run{
		stageRun("solo", "build", "PASSED"),
		stageRun("solo", "build", "PASSED"),
		stageRun("solo", "build", "PASSED"),
		stageRun("solo", "build", "FAILED"),
		stageRun("council", "intake", "UNVERIFIED"),
		stageRun("council", "spec", "UNVERIFIED"),
		stageRun("council", "plan", "UNVERIFIED"),
		stageRun("pair", "build", "PASSED"),
		stageRun("pair", "build", "PASSED"),
	}, Options{By: "mode"})

	for _, d := range rep.Deltas {
		if d.Key == "council" {
			t.Errorf("council was compared against a build baseline: %+v", d)
		}
	}
	// pair still is, because it builds.
	var sawPair bool
	for _, d := range rep.Deltas {
		if d.Key == "pair" {
			sawPair = true
		}
	}
	if !sawPair {
		t.Error("pair was dropped from the comparison; it builds code and belongs there")
	}

	// The row itself stays — council ran, and what it cost is worth seeing.
	if findRow(rep.Rows, "council") == nil {
		t.Error("council vanished from the table entirely")
	}

	// And the reason is stated, because a mode that silently disappears from
	// the comparison reads as a bug in the report.
	out := Render(rep)
	if !strings.Contains(out, "not compared") || !strings.Contains(out, "no gate to pass") {
		t.Errorf("the report does not say why council is absent:\n%s", out)
	}
}

// A mode that built once has a pass rate of 0% or 100% and nothing else. That
// is not a rate, and presenting it beside a four-run baseline gives it an
// authority it has not earned.
func TestASingleRunIsMarkedAsNotARate(t *testing.T) {
	rep := Build([]*runlog.Run{
		stageRun("solo", "build", "PASSED"),
		stageRun("solo", "build", "PASSED"),
		stageRun("solo", "build", "FAILED"),
		stageRun("tournament", "build", "FAILED"),
	}, Options{By: "mode"})

	var found bool
	for _, d := range rep.Deltas {
		if d.Key == "tournament" {
			found = true
			if !d.OneRun {
				t.Error("a delta from one run was not marked")
			}
		}
	}
	if !found {
		t.Fatal("tournament is missing from the comparison")
	}
	if out := Render(rep); !strings.Contains(out, "not a rate yet") {
		t.Errorf("the report does not say a single run is not a rate:\n%s", out)
	}
}

// Two runs is thin, but it is a rate: it can be 0, 50 or 100. Only one sample
// is qualitatively different, so only one sample is marked.
func TestTwoRunsAreNotMarked(t *testing.T) {
	rep := Build([]*runlog.Run{
		stageRun("solo", "build", "PASSED"),
		stageRun("solo", "build", "FAILED"),
		stageRun("pair", "build", "PASSED"),
		stageRun("pair", "build", "PASSED"),
	}, Options{By: "mode"})
	for _, d := range rep.Deltas {
		if d.Key == "pair" && d.OneRun {
			t.Error("a two-run delta was marked as not a rate")
		}
	}
}

// A run with no stage recorded predates the field and was a build run: every
// artifact stage has always written one. Treating it as uncomparable would
// erase the history the baseline is built from.
func TestARunWithNoStageCountsAsABuild(t *testing.T) {
	rep := Build([]*runlog.Run{
		stageRun("solo", "", "PASSED"),
		stageRun("pair", "", "PASSED"),
	}, Options{By: "mode"})
	if len(rep.Deltas) != 1 || rep.Deltas[0].Key != "pair" {
		t.Errorf("deltas = %+v", rep.Deltas)
	}
}
