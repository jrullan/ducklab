package cli

import (
	"encoding/json"
	"testing"
)

// B-520: --ref stays a plain (background) path; --ref-scope sends the role.
func TestCLIStageRefsMarkScope(t *testing.T) {
	data, err := json.Marshal(cliStageRefs([]string{"notes.md"}, []string{"guide.md"}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `["notes.md",{"path":"guide.md","role":"scope"}]`; got != want {
		t.Errorf("refs = %s, want %s", got, want)
	}
	if cliStageRefs(nil, nil) != nil {
		t.Error("no refs must send no refs field")
	}
}
