package stage

import (
	"strings"
	"testing"
)

func TestTaskBodyContractMatchesFlatAcceptanceSliceChecker(t *testing.T) {
	if strings.Contains(TaskBodyContract, "indented sub-bullets") {
		t.Fatalf("task contract still authorizes nested acceptance bullets:\n%s", TaskBodyContract)
	}
	for _, want := range []string{"flat list", "never in nested bullets", "task prose"} {
		if !strings.Contains(TaskBodyContract, want) {
			t.Fatalf("task contract lacks %q:\n%s", want, TaskBodyContract)
		}
	}
}
