package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// executeTriage wrote turn_start and turn_end around nothing at all. The lane
// showed a participant with an empty bubble, and the model's reasoning — which
// IS the content of a triage — never left the process.
func TestATriageRecordsWhatTheModelSaid(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "vertex drag never starts", Severity: "critical",
	}); err != nil {
		t.Fatal(err)
	}
	run, err := s.BugTriage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// A triage ends paused at its human gate, which waitForRun reports as an
	// error. The run is finished either way; what matters is what it wrote.
	_, _ = s.waitForRun(context.Background(), run.ID)

	data, err := os.ReadFile(filepath.Join(dir, ".ducklab", "runs", run.ID, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &e) == nil {
			kinds[e.Type]++
		}
	}
	if kinds["turn_start"] == 0 {
		t.Fatalf("the triager never ran: %v", kinds)
	}
	if kinds["message"] == 0 {
		t.Errorf("the turn recorded nothing it said: %v", kinds)
	}
}
