package daemon

import (
	"reflect"
	"testing"
)

// B-550: the engine never inherits the agent token of whoever started it.
func TestWithoutEnvDropsOnlyThatVariable(t *testing.T) {
	got := withoutEnv([]string{"A=1", "DUCKLAB_TOKEN=dlk_x", "DUCKLAB_TOKENS=keep", "B=2"}, "DUCKLAB_TOKEN")
	want := []string{"A=1", "DUCKLAB_TOKENS=keep", "B=2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("withoutEnv = %v, want %v", got, want)
	}
}
