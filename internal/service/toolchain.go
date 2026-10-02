package service

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

// The stack an architect chooses has a toolchain, and the plan declares it
// (**Toolchain:** per milestone). Nobody installs anything ahead of time:
// the first build that needs a tool checks the machine and, when one is
// missing, asks the person to install it — at the moment it matters, with
// the exact names.

// declaredToolchain returns the tools the plan declares for the milestone
// that holds taskID — or, when the task cannot be placed, for the whole plan.
// Names are the binaries as invoked; a parenthesised hint after a name is kept
// for the message and ignored for the check.
func declaredToolchain(plan *artifact.Document, taskID string) []string {
	if plan == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(field string) {
		for _, item := range strings.Split(field, ",") {
			item = strings.TrimSpace(strings.Trim(item, "`"))
			if item != "" && !seen[item] {
				seen[item] = true
				out = append(out, item)
			}
		}
	}
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

// capabilityAvailable checks declared commands and pkg-config modules.
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
		return exec.Command("pkg-config", append(args, m[1])...).Run() == nil
	}
	bin := binaryOf(clean)
	if bin == "" {
		return true
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// equivalentCommand describes a known compatible command found on PATH.
func equivalentCommand(item string) string {
	if binaryOf(item) == "python" {
		if _, err := exec.LookPath("python3"); err == nil {
			return "python3"
		}
	}
	return ""
}

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

func capabilityStructureFindings(projectRoot string, plan *artifact.Document) []string {
	if plan == nil {
		return nil
	}
	modules := installedPkgConfigModules()
	var out []string
	seen := map[string]bool{}
	registry := capability.DefaultRegistry()
	for _, sec := range plan.Sections {
		tasks := sec.Children
		if strings.HasPrefix(strings.ToUpper(sec.ID), "T-") {
			tasks = []artifact.Section{sec}
		}
		for _, task := range tasks {
			for _, finding := range registry.InspectPlanTask(capability.PlanTaskContext{ID: task.ID, Body: task.Body, Verification: task.Field("verification"), ProjectRoot: projectRoot}) {
				out = append(out, fmt.Sprintf("%s plan contract (%s/%s): %s", task.ID, finding.Capability, finding.Name, finding.Detail))
			}
		}
		for _, item := range strings.Split(sec.Field("toolchain"), ",") {
			item = strings.TrimSpace(strings.Trim(item, "`"))
			if item == "" || capabilityAvailable(item) {
				continue
			}
			if equivalent := equivalentCommand(item); equivalent != "" {
				out = append(out, fmt.Sprintf("%s declares %s, but it is not on PATH; %s is available (install python-is-python3, or change the plan to cmd:%s)", sec.ID, item, equivalent, equivalent))
			}
			m := pkgConfigCapability.FindStringSubmatch(item)
			if m == nil {
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
	// Project commands are the local evidence that a binary name is wrong, not
	// merely absent. Keep this advisory: a plan may deliberately provision a tool.
	if projectRoot != "" {
		if cfg, err := config.LoadProject(projectRoot + "/.ducklab/project.toml"); err == nil {
			command := strings.Fields(cfg.Run.Command)
			if len(command) > 0 {
				for _, item := range declaredToolchain(plan, "") {
					if eq := equivalentCommand(item); eq != "" && command[0] == eq {
						out = append(out, fmt.Sprintf("plan declares %s while [run].command uses %s; revise the declared command or install python-is-python3", item, eq))
					}
				}
			}
		}
	}
	return out
}

// wontRequirementStructureFindings prevents active specs from implementing an excluded requirement.
func wontRequirementStructureFindings(projectRoot string, spec *artifact.Document) []string {
	if spec == nil {
		return nil
	}
	requirements, err := artifact.Load(projectRoot, artifact.KindRequirements)
	if err != nil || requirements == nil {
		return nil
	}
	wont := map[string]bool{}
	for _, req := range requirements.Sections {
		if strings.EqualFold(strings.TrimSpace(req.Field("priority")), "wont") {
			wont[req.ID] = true
		}
	}
	var findings []string
	for _, sec := range spec.Sections {
		if strings.EqualFold(strings.TrimSpace(sec.Field("priority")), "wont") {
			continue
		}
		for _, reqID := range sec.Implements {
			if wont[reqID] {
				findings = append(findings, fmt.Sprintf("%s actively implements %s, but %s has Priority: wont — remove it from this active Implements line or record the exclusion in a spec section marked Priority: wont", sec.ID, reqID, reqID))
			}
		}
	}
	return findings
}

func installedPkgConfigModules() []string {
	out, err := exec.Command("pkg-config", "--list-all").Output()
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
	wantKey := capabilityKey(want)
	for _, candidate := range modules {
		if wantKey != "" && capabilityKey(candidate) == wantKey {
			return candidate
		}
	}
	return ""
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

func toolchainQuestion(taskID string, missing []string, recheck bool) *tools.PendingQuestion {
	details := make([]string, 0, len(missing))
	for _, item := range missing {
		detail := item
		if eq := equivalentCommand(item); eq != "" {
			detail += fmt.Sprintf(" is still not on PATH; %s is available (install python-is-python3, or change the plan to cmd:%s)", eq, eq)
		}
		details = append(details, detail)
	}
	prefix := "The plan declares environment capabilities this machine does not have"
	if recheck {
		prefix = "The toolchain was re-checked after your answer and"
	}
	return &tools.PendingQuestion{ID: "toolchain-" + taskID, Question: fmt.Sprintf("%s: %s. Install them and continue, or change the plan.", prefix, strings.Join(details, "; ")), Options: []string{"Installed — continue", "Change the plan (revise it) instead"}}
}

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
func taskArtifactFiles(projectRoot, taskID, field string) []string {
	value := taskField(projectRoot, taskID, field)
	var files []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if strings.HasPrefix(strings.ToLower(item), "file:") {
			if path := strings.TrimSpace(item[len("file:"):]); path != "" {
				files = append(files, path)
			}
		}
	}
	return files
}
func taskDependencyProducedFiles(projectRoot, taskID string) []string {
	plan, err := artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil {
		return nil
	}
	tasks := map[string]artifact.Section{}
	for _, milestone := range plan.Sections {
		for _, task := range milestone.Children {
			tasks[strings.ToUpper(task.ID)] = task
		}
	}
	seenTasks, seenFiles := map[string]bool{}, map[string]bool{}
	var files []string
	var visit func(string)
	visit = func(id string) {
		id = strings.ToUpper(strings.TrimSpace(id))
		if id == "" || seenTasks[id] {
			return
		}
		seenTasks[id] = true
		task, ok := tasks[id]
		if !ok {
			return
		}
		for _, dep := range splitList(task.Field("depends on")) {
			visit(dep)
		}
		for _, file := range artifactFiles(task.Field("produces")) {
			if !seenFiles[file] {
				seenFiles[file] = true
				files = append(files, file)
			}
		}
	}
	root, ok := tasks[strings.ToUpper(taskID)]
	if !ok {
		return nil
	}
	for _, dep := range splitList(root.Field("depends on")) {
		visit(dep)
	}
	sort.Strings(files)
	return files
}
func artifactFiles(value string) []string {
	var files []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(strings.Trim(item, "`"))
		if strings.HasPrefix(strings.ToLower(item), "file:") {
			if file := strings.TrimSpace(item[len("file:"):]); file != "" {
				files = append(files, file)
			}
		}
	}
	return files
}

var acceptanceProbeLine = regexp.MustCompile("^(?:[-*]\\s+|[0-9]+[.)]\\s+)(?:[^`]*)`([^`]+)`\\s*$")

func taskAcceptanceProbes(projectRoot, taskID string) []string {
	plan, err := artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil {
		return nil
	}
	var body string
	for _, milestone := range plan.Sections {
		for _, task := range milestone.Children {
			if strings.EqualFold(task.ID, taskID) {
				body = task.Body
			}
		}
	}
	if body == "" {
		return nil
	}
	in := false
	var probes []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		switch {
		case strings.EqualFold(strings.TrimSpace(trimmed), "**Acceptance probes:**"):
			in = true
			continue
		case in && (strings.HasPrefix(strings.TrimSpace(trimmed), "**") || strings.HasPrefix(trimmed, "#")):
			in = false
		}
		if in {
			if m := acceptanceProbeLine.FindStringSubmatch(trimmed); m != nil {
				probes = append(probes, strings.TrimSpace(m[1]))
			}
		}
	}
	return probes
}
