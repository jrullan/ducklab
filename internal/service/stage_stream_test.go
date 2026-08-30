package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// A stage run streams whatever its launcher said: the bus fans deltas out to
// whoever watches, and a CLI-launched intake used to show the desktop no
// text and no thinking for its whole length.
func TestAStageRunStreamsRegardlessOfItsLauncher(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	run, err := s.StageStart(context.Background(), id, StageRequest{Stage: "intake", From: "a brief", Stream: false})
	if err != nil {
		t.Fatal(err)
	}
	if !run.Stream {
		t.Fatal("a stage run launched without the stream flag does not stream")
	}
}
