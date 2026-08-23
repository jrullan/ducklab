package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func proofCmd(verb string, args []string, repo string) int {
	if verb != "verify" || len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: ducklab proof verify <receipt>")
		return 2
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return 1
	}
	var r struct {
		BaseSHA             string   `json:"base_sha"`
		HeadSHA             string   `json:"head_sha"`
		DiffSHA256          string   `json:"diff_sha256"`
		GateCommand         string   `json:"gate_command"`
		ExitCode            *int     `json:"exit_code"`
		DurationS           *float64 `json:"duration_s"`
		ReproductionVerdict string   `json:"reproduction_verdict"`
		AcceptedBy          string   `json:"accepted_by"`
		AcceptedAt          string   `json:"accepted_at"`
	}
	if json.Unmarshal(data, &r) != nil || r.BaseSHA == "" || r.HeadSHA == "" || r.DiffSHA256 == "" || r.GateCommand == "" || r.ExitCode == nil || r.DurationS == nil || r.ReproductionVerdict == "" || r.AcceptedBy == "" || r.AcceptedAt == "" || *r.ExitCode != 0 || r.ReproductionVerdict != "green" {
		return 1
	}
	if repo == "" {
		repo = "."
	}
	runGit := func(a ...string) ([]byte, error) { c := exec.Command("git", a...); c.Dir = repo; return c.Output() }
	head, err := runGit("rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != r.HeadSHA {
		return 1
	}
	base, err := runGit("rev-parse", r.HeadSHA+"^1")
	if err != nil || strings.TrimSpace(string(base)) != r.BaseSHA {
		return 1
	}
	c := exec.Command("git", "-c", "color.ui=false", "-c", "diff.external=", "diff", "--no-ext-diff", "--binary", r.BaseSHA, r.HeadSHA)
	c.Dir = repo
	diff, err := c.Output()
	if err != nil {
		return 1
	}
	sum := sha256.Sum256(diff)
	if hex.EncodeToString(sum[:]) != r.DiffSHA256 {
		return 1
	}
	return 0
}
