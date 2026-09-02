package service

import "testing"

func TestBuildVerdictRequiresEveryAvailableSignal(t *testing.T) {
	for _, tc := range []struct {
		name, project, task string
		dissent             bool
		want                string
	}{
		{"all green", "PASSED", "green", false, "PASSED"},
		{"no task contract", "PASSED", "none", false, "PASSED"},
		{"task red", "PASSED", "red", false, "FAILED"},
		{"reviewer dissent", "PASSED", "green", true, "FAILED"},
		{"project red stays red", "FAILED", "green", false, "FAILED"},
		{"unverified stays unverified", "UNVERIFIED", "none", false, "UNVERIFIED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := adjudicateBuildVerdict(tc.project, tc.task, tc.dissent); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
