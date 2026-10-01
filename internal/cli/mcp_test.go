package cli

import (
	"bytes"
	"errors"
	"testing"

	"github.com/jrullan/ducklab/internal/engineclt"
)

// B-472: MCP is a first command on a clean install, so it goes through the
// same engine autostart discovery as every other client command.
func TestMCPServeUsesEngineAutostart(t *testing.T) {
	called := false
	code := mcpCmdWith("serve", false, bytes.NewReader(nil), &bytes.Buffer{},
		func(noAutostart bool) (*engineclt.Client, error) {
			called = true
			if noAutostart {
				t.Fatal("MCP disabled autostart")
			}
			return nil, errors.New("sentinel")
		})
	if !called || code != 9 {
		t.Fatalf("discover called = %v, code = %d", called, code)
	}
}
