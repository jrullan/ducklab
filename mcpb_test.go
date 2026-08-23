package ducklab_test

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type mcpbManifest struct {
	ManifestVersion string `json:"manifest_version"`
	Server          struct {
		Type       string `json:"type"`
		EntryPoint string `json:"entry_point"`
		MCPConfig  struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcp_config"`
	} `json:"server"`
}

func TestMCPBBundleIsInstallableLinuxAMD64StdioServer(t *testing.T) {
	repo := projectRoot(t)
	bundle := filepath.Join(repo, "dist", "ducklab-mcp.mcpb")
	_ = os.Remove(bundle)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(bundle)) })

	cmd := exec.Command("make", "mcpb")
	cmd.Dir = repo
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make mcpb failed: %v\n%s", err, output)
	}

	zr, err := zip.OpenReader(bundle)
	if err != nil {
		t.Fatalf("open MCPB bundle: %v", err)
	}
	defer zr.Close()

	files := make(map[string]*zip.File, len(zr.File))
	for _, file := range zr.File {
		if strings.HasPrefix(file.Name, "/") || strings.Contains(file.Name, "../") {
			t.Fatalf("bundle contains unsafe path %q", file.Name)
		}
		files[file.Name] = file
	}
	manifestFile := files["manifest.json"]
	if manifestFile == nil {
		t.Fatal("bundle is missing root manifest.json")
	}
	manifestBytes := readZipFile(t, manifestFile)
	var manifest mcpbManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse manifest.json: %v", err)
	}
	if manifest.ManifestVersion == "" {
		t.Error("manifest_version is required")
	}
	if manifest.Server.Type != "stdio" {
		t.Errorf("server.type = %q, want stdio", manifest.Server.Type)
	}
	if manifest.Server.EntryPoint == "" || filepath.IsAbs(manifest.Server.EntryPoint) || strings.Contains(manifest.Server.EntryPoint, "..") {
		t.Fatalf("server.entry_point must be a safe bundle-relative binary path, got %q", manifest.Server.EntryPoint)
	}
	binaryFile := files[manifest.Server.EntryPoint]
	if binaryFile == nil {
		t.Fatalf("entry_point %q is not in the bundle", manifest.Server.EntryPoint)
	}
	if binaryFile.Mode()&0111 == 0 {
		t.Errorf("entry_point %q is not executable in the bundle (mode %v)", manifest.Server.EntryPoint, binaryFile.Mode())
	}
	assertLinuxAMD64ELF(t, readZipFile(t, binaryFile))

	if manifest.Server.MCPConfig.Command == "" {
		t.Fatal("server.mcp_config.command is required")
	}
	if got := manifest.Server.MCPConfig.Args; len(got) != 2 || got[0] != "mcp" || got[1] != "serve" {
		t.Errorf("server.mcp_config.args = %q, want [mcp serve]", got)
	}

	extract := t.TempDir()
	for _, file := range zr.File {
		if file.FileInfo().IsDir() {
			continue
		}
		path := filepath.Join(extract, file.Name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, readZipFile(t, file), file.Mode()); err != nil {
			t.Fatal(err)
		}
	}
	program := strings.ReplaceAll(manifest.Server.MCPConfig.Command, "${__dirname}", extract)
	if !filepath.IsAbs(program) {
		program = filepath.Join(extract, program)
	}
	if filepath.Clean(program) != filepath.Join(extract, manifest.Server.EntryPoint) {
		t.Fatalf("mcp_config.command %q must execute entry_point %q", manifest.Server.MCPConfig.Command, manifest.Server.EntryPoint)
	}
	invoke := exec.Command(program, manifest.Server.MCPConfig.Args...)
	invoke.Env = append(os.Environ(), "XDG_STATE_HOME="+filepath.Join(extract, "state"))
	output, err := invoke.CombinedOutput()
	if err == nil {
		t.Fatal("declared stdio command unexpectedly succeeded without an engine")
	}
	var exitErr *exec.ExitError
	if !strings.Contains(string(output), "engine not running") || !asExitError(err, &exitErr) || exitErr.ExitCode() != 9 {
		t.Fatalf("declared command did not answer the mcp serve no-engine contract: err=%v output=%q", err, output)
	}
}

func TestReleaseFlowPublishesMCPBBundle(t *testing.T) {
	repo := projectRoot(t)
	workflows, err := filepath.Glob(filepath.Join(repo, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflowsYAML, err := filepath.Glob(filepath.Join(repo, ".github", "workflows", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	workflows = append(workflows, workflowsYAML...)
	for _, workflow := range workflows {
		contents, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatal(err)
		}
		// A release workflow must upload the artifact built by `make mcpb`, not
		// merely mention the format in a comment or a non-release job.
		text := string(contents)
		if strings.Contains(text, "release") && strings.Contains(text, "mcpb") && strings.Contains(text, "ducklab-mcp.mcpb") {
			return
		}
	}
	t.Fatal("no GitHub release workflow publishes ducklab-mcp.mcpb")
}

func TestRegistryMCPBURLNamesReleasedBundle(t *testing.T) {
	repo := projectRoot(t)
	contents, err := os.ReadFile(filepath.Join(repo, "docs", "mcp-registry", "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Version  string `json:"version"`
		Packages []struct {
			RegistryType string `json:"registryType"`
			Identifier   string `json:"identifier"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(contents, &registry); err != nil {
		t.Fatalf("parse registry server.json: %v", err)
	}
	want := "/releases/download/v" + registry.Version + "/ducklab-mcp.mcpb"
	for _, pkg := range registry.Packages {
		if pkg.RegistryType == "mcpb" {
			if strings.HasSuffix(pkg.Identifier, want) && !strings.Contains(pkg.Identifier, "vX.Y.Z") {
				return
			}
			t.Fatalf("MCPB identifier = %q, want a release artifact ending %q", pkg.Identifier, want)
		}
	}
	t.Fatal("registry server.json has no MCPB package")
}

func projectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func readZipFile(t *testing.T, file *zip.File) []byte {
	t.Helper()
	r, err := file.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	contents, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func assertLinuxAMD64ELF(t *testing.T, contents []byte) {
	t.Helper()
	if len(contents) < 20 || !bytes.Equal(contents[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		t.Fatal("entry_point is not an ELF executable")
	}
	if contents[4] != 2 || contents[5] != 1 {
		t.Fatalf("entry_point is not a 64-bit little-endian Linux executable (class=%d data=%d)", contents[4], contents[5])
	}
	if machine := binary.LittleEndian.Uint16(contents[18:20]); machine != 62 {
		t.Fatalf("entry_point ELF machine = %d, want AMD64 (62)", machine)
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	if exitErr, ok := err.(*exec.ExitError); ok {
		*target = exitErr
		return true
	}
	return false
}

