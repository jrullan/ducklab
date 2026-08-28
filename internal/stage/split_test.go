package stage

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

func TestMergeSplitRequiresDisjointNonEmptyLanes(t *testing.T) {
	current := &artifact.Document{Sections: []artifact.Section{
		{ID: "M-001", Title: "Core", Children: []artifact.Section{
			{ID: "T-001", Title: "Combined", Body: "**Owns:** internal/combined\n\nDo both.", Owns: []string{"internal/combined"}},
		}},
	}}
	parts := []artifact.Section{
		{ID: "T-900", Title: "One", Body: "**Owns:** internal/a\n\nDo one.", Owns: []string{"internal/a"}},
		{ID: "T-901", Title: "Two", Body: "**Owns:** internal/a/sub\n\nDo two.", Owns: []string{"internal/a/sub"}},
	}
	if _, err := mergeSplit(current, "T-001", parts); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlapping split lanes error = %v", err)
	}
	parts[1] = artifact.Section{ID: "T-901", Title: "Two", Body: "**Owns:** internal/b\n\nDo two.", Owns: []string{"internal/b"}}
	out, err := mergeSplit(current, "T-001", parts)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Sections[0].Children) != 2 || out.Section("T-001") != nil {
		t.Fatalf("split did not replace target: %+v", out.Sections[0].Children)
	}
}
