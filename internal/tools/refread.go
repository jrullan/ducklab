package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The full text behind a digest. In digest mode the prompt carries a bounded
// digest of each reference document; this tool is how a seat reaches the
// exact detail a digest elides — identifiers, rules, numbers. It reads ONLY
// the files the run was launched with: references live outside the project
// root on purpose (a wiki is the commonest home of adoption context), and
// this tool is the one bridge, not a general escape from the fs sandbox.

// RefRead reads one of the run's reference documents, in portions sized to
// the seat.
type RefRead struct{}

func (t *RefRead) Name() string   { return "ref_read" }
func (t *RefRead) Mutating() bool { return false }

func (t *RefRead) Description() string {
	return "Read the full text of one of this run's reference documents (the ones shown as digests in your prompt). Large documents return in portions: the result names the offset to continue from."
}

func (t *RefRead) Schema() interface{} {
	return NewSchema().
		AddString("path", "The reference document's path, as shown in the prompt's digest heading", true).
		AddInt("offset", "Character offset to continue from (default 0)", false)
}

func (t *RefRead) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var in struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Path) == "" {
		return ErrorResult("ref_read needs a path"), nil
	}
	if len(ectx.RefPaths) == 0 {
		return ErrorResult("this run has no reference documents"), nil
	}
	// Exact path first, basename second: the digest heading shows the full
	// path, but a small model often repeats just the file name.
	target := ""
	for _, p := range ectx.RefPaths {
		if p == in.Path {
			target = p
			break
		}
	}
	if target == "" {
		for _, p := range ectx.RefPaths {
			if filepath.Base(p) == filepath.Base(in.Path) {
				target = p
				break
			}
		}
	}
	if target == "" {
		return ErrorResult("not one of this run's references: %s\navailable:\n%s",
			in.Path, strings.Join(ectx.RefPaths, "\n")), nil
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return ErrorResult("read %s: %v", target, err), nil
	}
	text := string(raw)
	if in.Offset < 0 {
		in.Offset = 0
	}
	if in.Offset >= len(text) {
		return ErrorResult("offset %d is past the end (%d chars)", in.Offset, len(text)), nil
	}
	// Self-bounded BEFORE the central cap so the continuation line survives:
	// a blind truncation would cut the very sentence that says how to go on.
	limit := resultCapFor(ectx.SeatContextTokens) - 200
	if limit < 1_000 {
		limit = 1_000
	}
	end := in.Offset + limit
	if end > len(text) {
		end = len(text)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s (chars %d–%d of %d)\n\n%s", target, in.Offset, end, len(text), text[in.Offset:end])
	if end < len(text) {
		fmt.Fprintf(&b, "\n\n[continues — call ref_read again with offset=%d]", end)
	}
	if ectx.OnRefRead != nil {
		ectx.OnRefRead(target)
	}
	return &Result{Content: b.String()}, nil
}
