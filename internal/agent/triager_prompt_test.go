package agent

import (
	"strings"
	"testing"
)

func TestTriagerRequiresRepositoryRelativeLanePaths(t *testing.T) {
	for _, phrase := range []string{
		"Every path in suspected_files and owns must be repository-relative",
		"never return a bare filename",
		"Inspect the project tree and choose the exact path",
	} {
		if !strings.Contains(triagerPrompt, phrase) {
			t.Errorf("triager prompt omitted %q", phrase)
		}
	}
}
