package service

import (
	"fmt"
	"os/exec"
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

// binaryOf strips an install hint: "meson (apt: meson)" -> "meson".
func binaryOf(item string) string {
	if i := strings.Index(item, "("); i > 0 {
		item = item[:i]
	}
	return strings.TrimSpace(item)
}

// missingTools reports which declared tools are not on PATH.
func missingTools(declared []string) []string {
	var missing []string
	for _, item := range declared {
		bin := binaryOf(item)
		if bin == "" {
			continue
		}
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, item)
		}
	}
	sort.Strings(missing)
	return missing
}

// toolchainQuestion is the pause a build stops on when the plan's toolchain
// is not on the machine.
func toolchainQuestion(taskID string, missing []string) *tools.PendingQuestion {
	return &tools.PendingQuestion{
		ID:       "toolchain-" + taskID,
		Question: fmt.Sprintf("The plan declares a toolchain this machine does not have: %s. Install it and continue, or change the plan.", strings.Join(missing, ", ")),
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
