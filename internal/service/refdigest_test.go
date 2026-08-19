package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mode decision is the feature's contract: a corpus that crowds the
// architect's window digests; one that fits stays inline, and an undeclared
// window never silently digests.
func TestRefsNeedDigest(t *testing.T) {
	cases := []struct {
		chars, ctx int
		want       bool
	}{
		{256_000, 32_768, true},   // MiEmpresa's corpus on a local seat
		{256_000, 200_000, false}, // the same corpus on a cloud seat: 32%
		{40_000, 32_768, false},   // 10k tokens on 32k: inline
		{60_000, 32_768, true},    // 15k tokens on 32k: 45%
		{256_000, 0, false},       // undeclared window: inline, preflight warns
	}
	for _, c := range cases {
		if got := refsNeedDigest(c.chars, c.ctx); got != c.want {
			t.Errorf("refsNeedDigest(%d, %d) = %v, want %v", c.chars, c.ctx, got, c.want)
		}
	}
}

func TestSplitRefChunksSnapsToNewlines(t *testing.T) {
	text := strings.Repeat("line of prose\n", 1000)
	chunks := splitRefChunks(text, 5_000)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	var total int
	for i, c := range chunks {
		total += len(c)
		if len(c) > 5_000 {
			t.Errorf("chunk %d exceeds size: %d", i, len(c))
		}
		if i < len(chunks)-1 && !strings.HasSuffix(c, "\n") {
			t.Errorf("chunk %d does not end on a line boundary", i)
		}
	}
	if total != len(text) {
		t.Errorf("chunks lose text: %d of %d chars", total, len(text))
	}
}

// A digest is paid for once per document version per model: the second run
// of the same corpus must make zero model calls.
func TestDigestReferencesCachesByContent(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "spec.md")
	os.WriteFile(doc, []byte(strings.Repeat("the RBAC permission miempresa.users.manage\n", 100)), 0o644)
	cacheDir := filepath.Join(dir, "cache")

	calls := 0
	summarize := func(ctx context.Context, prompt string) (string, error) {
		calls++
		return "digest: RBAC permissions", nil
	}
	first, err := digestReferences(context.Background(), []string{doc}, 10_000, "m1", cacheDir, summarize, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 || first[0].Cached {
		t.Fatalf("first pass should call the model: calls=%d cached=%v", calls, first[0].Cached)
	}
	calls = 0
	second, err := digestReferences(context.Background(), []string{doc}, 10_000, "m1", cacheDir, summarize, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || !second[0].Cached {
		t.Errorf("second pass should be cache-only: calls=%d cached=%v", calls, second[0].Cached)
	}
	// A different model must not reuse another model's digest.
	calls = 0
	if _, err := digestReferences(context.Background(), []string{doc}, 10_000, "m2", cacheDir, summarize, nil); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Error("a different model reused a cached digest")
	}
}

// The digest-mode section must tell the seat the full text is reachable —
// the digest is the floor, the tool is the ceiling.
func TestRenderRefDigestsNamesTheTool(t *testing.T) {
	digests := []refDigest{{Path: "/w/rbac.md", Chars: 33_578, DigestChars: 20, digest: "module-first permissions"}}
	got := renderRefDigests(digests, 256_000, "spec")
	for _, want := range []string{"ref_read", "module-first permissions", "/w/rbac.md", "full text: 33578 chars", "USE them"} {
		if !strings.Contains(got, want) {
			t.Errorf("digest section lacks %q", want)
		}
	}
}

// Unread accounting is digest-mode-only: an inline run's references were in
// the prompt, there was nothing left to open.
func TestUnreadRefsOnlyInDigestMode(t *testing.T) {
	rs := &runState{}
	rs.armRefs([]string{"/w/a.md", "/w/b.md"}, "inline")
	if got := rs.unreadRefs(); got != nil {
		t.Errorf("inline mode reported unread refs: %v", got)
	}
	rs.armRefs([]string{"/w/a.md", "/w/b.md"}, "digest")
	rs.markRefRead("/w/a.md")
	got := rs.unreadRefs()
	if len(got) != 1 || got[0] != "/w/b.md" {
		t.Errorf("unread = %v, want [/w/b.md]", got)
	}
}
