package service

import (
	"context"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/bus"
)

// Every run kind sets Stream: true on its record, and exactly one of the six
// wired the callbacks that act on it — so a triage, a review, a release and a
// test-first all claimed to stream and emitted nothing. Their lanes sat on
// "thinking…" for the whole run, with no way to tell work from a hang.
func TestATriageStreamsWhatTheModelProduces(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	sub, unsubscribe := s.bus.Subscribe("test", func(bus.Event) bool { return true })
	defer unsubscribe()

	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "vertex drag never starts", Severity: "critical",
	}); err != nil {
		t.Fatal(err)
	}
	run, err := s.BugTriage(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	if !run.Stream {
		t.Fatal("the run does not claim to stream, so this proves nothing")
	}
	_, _ = s.waitForRun(context.Background(), run.ID)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-sub.Ch:
			if e.Type == "token_delta" && e.RunID == run.ID {
				if text, _ := e.Data["text"].(string); text != "" {
					return // what the model produced, while it was producing it
				}
			}
		case <-deadline:
			t.Fatal("the triage emitted no token_delta: the run said it streams and nothing did")
		}
	}
}
