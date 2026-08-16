package build

import (
	"os"
	"strings"
	"testing"
)

func TestMakeBuildStampsGitDerivedVersion(t *testing.T) {
	data, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(data)
	if !strings.Contains(makefile, "git describe --tags --always") {
		t.Fatal("Makefile build does not derive Version from git describe --tags --always")
	}
	if !strings.Contains(makefile, "internal/build.Version=") {
		t.Fatal("Makefile build does not stamp internal/build.Version")
	}
	if strings.Count(makefile, "internal/build.Version=") < 3 {
		t.Fatal("all shipped Go binaries must receive the same git-derived version")
	}
}

func TestVersionIsNotTheFormerHandMaintainedRelease(t *testing.T) {
	if Version == "0.4.0" {
		t.Fatal("build.Version still contains the former hardcoded release")
	}
}
