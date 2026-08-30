package service

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/tools"
)

// The stack an architect chooses has a toolchain, and the plan declares it
// (**Toolchain:** per milestone). Nobody installs anything ahead of time:
// the first build that needs a tool checks the machine and, when one is
// missing, asks the person to install it — at the moment it matters, with
// the exact names. The first Neocapture builds ran with meson.build in the
// tree and no meson on the machine, and the gate stayed "none" for the
// whole run (benchmark run 5).

// declaredToolchain returns the tools the plan declares for the milestone
// that holds taskID — or, when the task cannot be placed, for the whole
// plan. Names are the binaries as invoked; a parenthesised hint after a
// name is kept for the message and ignored for the check.
func declaredToolchain(plan *artifact.Document, taskID string) []string {
	if plan == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(field string) {
		for _, item := range strings.Split(field, ",") {
			item = strings.TrimSpace(strings.Trim(item, "`"))
			if item == "" {
				continue
			}
			if !seen[item] {
				seen[item] = true
				out = append(out, item)
			}
		}
	}
	// The task's own milestone first: a task is a child of its milestone.
	for i := range plan.Sections {
		sec := &plan.Sections[i]
		for _, child := range sec.Children {
			if child.ID == taskID {
				add(sec.Field("toolchain"))
				if len(out) > 0 {
					return out
				}
			}
		}
	}
	for _, sec := range plan.Sections {
		add(sec.Field("toolchain"))
	}
	return out
}

// binaryOf strips an install hint and the optional cmd: capability prefix.
// Bare names remain supported so existing plans do not become unreadable.
func binaryOf(item string) string {
	if i := strings.Index(item, "("); i > 0 {
		item = item[:i]
	}
	item = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(item), "cmd:"))
	if i := strings.IndexAny(item, "<>="); i > 0 {
		item = item[:i]
	}
	return strings.TrimSpace(item)
}

var pkgConfigCapability = regexp.MustCompile(`^pkg-config:([^<>= ]+)(?:>=([^ ]+))?$`)

// capabilityAvailable checks the two environment facts a plan can declare:
// commands on PATH and pkg-config modules (optionally at a minimum version).
// Installation remains a human action; this only makes the preflight honest.
func capabilityAvailable(item string) bool {
	clean := strings.TrimSpace(strings.Trim(item, "`"))
	if m := pkgConfigCapability.FindStringSubmatch(clean); m != nil {
		if _, err := exec.LookPath("pkg-config"); err != nil {
			return false
		}
		args := []string{"--exists"}
		if m[2] != "" {
			args = []string{"--atleast-version=" + m[2]}
		}
		args = append(args, m[1])
		return exec.Command("pkg-config", args...).Run() == nil
	}
	bin := binaryOf(clean)
	if bin == "" {
		return true
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// missingTools reports which declared environment capabilities are absent.
func missingTools(declared []string) []string {
	var missing []string
	for _, item := range declared {
		if !capabilityAvailable(item) {
			missing = append(missing, item)
		}
	}
	sort.Strings(missing)
	return missing
}

func capabilityStructureFindings(plan *artifact.Document) []string {
	if plan == nil {
		return nil
	}
	modules := installedPkgConfigModules()
	var out []string
	seen := map[string]bool{}
	for _, sec := range plan.Sections {
		for _, item := range strings.Split(sec.Field("toolchain"), ",") {
			item = strings.TrimSpace(strings.Trim(item, "`"))
			m := pkgConfigCapability.FindStringSubmatch(item)
			if m == nil || capabilityAvailable(item) {
				continue
			}
			if suggestion := closestCapability(m[1], modules); suggestion != "" && suggestion != m[1] {
				key := m[1] + "|" + suggestion
				if !seen[key] {
					seen[key] = true
					out = append(out, fmt.Sprintf("%s declares %s, which is not resolvable; installed pkg-config metadata suggests pkg-config:%s — use the module name, not the OS package name", sec.ID, item, suggestion))
				}
			}
		}
	}
	return out
}

func installedPkgConfigModules() []string {
	cmd := exec.Command("pkg-config", "--list-all")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var modules []string
	s := bufio.NewScanner(strings.NewReader(string(out)))
	for s.Scan() {
		if fields := strings.Fields(s.Text()); len(fields) > 0 {
			modules = append(modules, fields[0])
		}
	}
	return modules
}

func closestCapability(want string, modules []string) string {
	best, bestDistance := "", 1<<30
	for _, candidate := range modules {
		d := editDistance(strings.ToLower(want), strings.ToLower(candidate))
		if normalized := editDistance(capabilityKey(want), capabilityKey(candidate)); normalized < d {
			d = normalized
		}
		if d < bestDistance {
			best, bestDistance = candidate, d
		}
	}
	limit := 3
	if len(want) > 10 {
		limit = 5
	}
	if bestDistance > limit {
		return ""
	}
	return best
}

func capabilityKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "lib")
	return strings.NewReplacer("-", "", "_", "", ".", "").Replace(s)
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// toolchainQuestion is the pause a build stops on when the plan's toolchain
// is not on the machine.
func toolchainQuestion(taskID string, missing []string) *tools.PendingQuestion {
	return &tools.PendingQuestion{
		ID:       "toolchain-" + taskID,
		Question: fmt.Sprintf("The plan declares environment capabilities this machine does not have: %s. Install them and continue, or change the plan.", strings.Join(missing, ", ")),
		Options:  []string{"Installed — continue", "Change the plan (revise it) instead"},
	}
}

// missingToolchainFor loads the plan and reports the declared tools this
// task's milestone needs that are not on PATH.
func (s *Service) missingToolchainFor(docsRoot, taskID string) []string {
	plan, err := artifact.Load(docsRoot, artifact.KindPlan)
	if err != nil {
		return nil
	}
	return missingTools(declaredToolchain(plan, taskID))
}

func taskField(projectRoot, taskID, field string) string {
	plan, err := artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil {
		return ""
	}
	for _, milestone := range plan.Sections {
		for _, task := range milestone.Children {
			if strings.EqualFold(task.ID, taskID) {
				return strings.TrimSpace(task.Field(field))
			}
		}
	}
	return ""
}

// taskVerificationCommand accepts the plan's deliberately narrow syntax: the
// command is the first backtick-delimited value. Prose after it explains the
// assertion to a person but is never interpreted by a shell.
func taskVerificationCommand(projectRoot, taskID string) string {
	value := taskField(projectRoot, taskID, "verification")
	if value == "" {
		return ""
	}
	if start := strings.Index(value, "`"); start >= 0 {
		if end := strings.Index(value[start+1:], "`"); end >= 0 {
			return strings.TrimSpace(value[start+1 : start+1+end])
		}
	}
	return ""
}
