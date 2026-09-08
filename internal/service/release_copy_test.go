package service

import (
	"os"
	"path/filepath"
	"testing"
)

// B-292: a dist tree may carry symlinks; the bundle must carry the same links,
// not copies of their targets. Before the fix a link to a directory failed the
// copy outright and a link to a file was flattened into a copy.
func TestCopyDirectoryPreservesSymlinks(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(filepath.Join(source, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "assets", "app.js"), []byte("app"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("assets", "app.js"), filepath.Join(source, "latest.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("assets", filepath.Join(source, "static")); err != nil {
		t.Fatal(err)
	}
	if err := copyDirectory(source, target); err != nil {
		t.Fatal(err)
	}

	for _, link := range []struct{ name, want string }{
		{"latest.js", filepath.Join("assets", "app.js")},
		{"static", "assets"},
	} {
		info, err := os.Lstat(filepath.Join(target, link.name))
		if err != nil {
			t.Fatalf("%s: %v", link.name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s was copied as %v, want a symlink", link.name, info.Mode())
			continue
		}
		got, err := os.Readlink(filepath.Join(target, link.name))
		if err != nil {
			t.Fatal(err)
		}
		if got != link.want {
			t.Errorf("%s -> %q, want %q", link.name, got, link.want)
		}
	}
	got, err := os.ReadFile(filepath.Join(target, "assets", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "app" {
		t.Errorf("regular file copied as %q", got)
	}
	if info, err := os.Lstat(filepath.Join(target, "index.html")); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("index.html: err=%v mode=%v, want a regular file", err, info.Mode())
	}
}
