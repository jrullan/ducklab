package xplat

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfigDir(t *testing.T) {
	dir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(dir, "ducklab") {
		t.Errorf("config dir should end in ducklab: %s", dir)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("config dir should be absolute: %s", dir)
	}
}

func TestDataDir(t *testing.T) {
	dir, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(dir, "ducklab") {
		t.Errorf("data dir should end in ducklab: %s", dir)
	}
}

func TestStateDir(t *testing.T) {
	dir, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(dir, "ducklab") {
		t.Errorf("state dir should end in ducklab: %s", dir)
	}
}

func TestCurrentOS(t *testing.T) {
	os := CurrentOS()
	switch runtime.GOOS {
	case "linux":
		if os != Linux {
			t.Errorf("expected linux, got %s", os)
		}
	case "darwin":
		if os != Darwin {
			t.Errorf("expected darwin, got %s", os)
		}
	case "windows":
		if os != Windows {
			t.Errorf("expected windows, got %s", os)
		}
	}
}

func TestAtomicWrite(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	data := []byte("hello world")
	if err := AtomicWrite(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
	// Verify no .tmp file remains
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmp file should not exist")
	}
}

func TestAtomicWriteNested(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "a", "b", "c", "test.txt")
	data := []byte("nested")
	if err := AtomicWrite(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"/abs/path", "/abs/path"},
		{"~", home},
		{"~/foo", filepath.Join(home, "foo")},
		{"~\\foo", filepath.Join(home, "foo")},
	}
	for _, tt := range tests {
		got, err := ExpandHome(tt.in)
		if err != nil {
			t.Errorf("ExpandHome(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
