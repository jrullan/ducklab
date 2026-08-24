package service

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

const autonomyFlipDiff = `diff --git a/.ducklab/project.toml b/.ducklab/project.toml
index 1111111..2222222 100644
--- a/.ducklab/project.toml
+++ b/.ducklab/project.toml
@@ -1 +1 @@
-autonomy = "guarded"
+autonomy = "yolo"
`

// A governance edit must be spoken before a reviewer decides, rather than
// buried in the diff. The same callout is the data the human-gate surface
// carries; it names the setting and both values so the person need not parse
// TOML or hunt through a large diff.
func TestAutonomyFlipIsCalledOutInReviewerAndHumanGatePayload(t *testing.T) {
	const callout = "this diff changes project autonomy from guarded to yolo"
	payload := reviewPrompt("T-116", autonomyFlipDiff)
	if !strings.Contains(payload, callout) {
		t.Errorf("reviewer payload omitted governance callout %q:\n%s", callout, payload)
	}

	// The paused run is the human-gate payload. Keep this reflective until the
	// field is introduced: it makes the behavioral requirement red today without
	// coupling the test to an analyzer helper or its placement.
	run := &runlog.Run{}
	field := reflect.ValueOf(run).Elem().FieldByName("GovernanceModified")
	if !field.IsValid() {
		t.Error("run record has no GovernanceModified field for the human gate")
		return
	}
	if field.Kind() != reflect.Bool {
		t.Fatalf("GovernanceModified kind = %s, want bool", field.Kind())
	}
	field.SetBool(true)
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"governance_modified":true`) {
		t.Fatalf("human-gate run payload = %s; want governance_modified", encoded)
	}
}
