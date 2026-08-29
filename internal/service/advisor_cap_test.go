package service

import (
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
)

// A seat that thinks inside its output cap needs the room its configuration
// grants; a suppressed or unconfigured seat keeps the one-shot floor.
func TestOneShotCapGivesAThinkingSeatItsConfiguredRoom(t *testing.T) {
	big, small := 20000, 500
	cases := []struct {
		name string
		d    *duckling.Duckling
		want int
	}{
		{"thinking seat with room", &duckling.Duckling{Params: config.SamplingParams{MaxTokens: &big}}, 20000},
		{"thinking seat below the floor", &duckling.Duckling{Params: config.SamplingParams{MaxTokens: &small}}, 2000},
		{"suppressed seat", &duckling.Duckling{Params: config.SamplingParams{MaxTokens: &big, DisableThinking: true}}, 2000},
		{"unconfigured seat", &duckling.Duckling{}, 2000},
		{"no seat", nil, 2000},
	}
	for _, c := range cases {
		if got := oneShotCap(c.d, 2000); got != c.want {
			t.Errorf("%s: oneShotCap = %d, want %d", c.name, got, c.want)
		}
	}
}
