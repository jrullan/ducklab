package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/skill"
	"github.com/jrullan/ducklab/internal/vcs"
)

// markPending flags skills this run wrote or changed but nobody has accepted.
//
// A skill only becomes usable after the human accepts the run that created it
// (05 §7.1). Without this a model can write its own instructions and follow
// them in the same turn, and the gate the spec puts between those two steps
// never happens.
//
// Only project skills: a global skill lives outside any repo and was put there
// by a person.
func markPending(ectx *ExecContext, skills []*skill.Skill) {
	if ectx.ProjectRoot == "" || ectx.AllowUnacceptedSkills {
		return
	}
	projectRoot := ectx.ProjectRoot
	g := vcs.New(projectRoot)
	for _, sk := range skills {
		if sk.Scope == skill.ScopeProject && !g.PathIsCommitted(sk.Dir) {
			sk.Pending = true
		}
	}
}

// pendingRefusal is what a model is told instead of the skill.
//
// It names the state and the fix, and carries none of the skill's own text:
// the body is the injection, so quoting it in the refusal would be the same
// leak by another route.
func pendingRefusal(name string) *Result {
	return ErrorResult("skill %q was written or changed by this run and is not usable yet. "+
		"A skill becomes available once a human accepts the run that wrote it.", name)
}

// SkillList lists the skills available to the project.
type SkillList struct{}

// Name returns the tool name.
func (t *SkillList) Name() string   { return "skill_list" }
func (t *SkillList) Mutating() bool { return false }

// Description returns the tool description.
func (t *SkillList) Description() string {
	return "List the skills available in this project. Each entry says when to use it."
}

// Schema returns the argument schema.
func (t *SkillList) Schema() interface{} { return NewSchema() }

// Execute runs the tool.
func (t *SkillList) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	all, problems := skill.List(ectx.ProjectRoot, ectx.GlobalSkillsDir)
	markPending(ectx, all)
	if len(all) == 0 && len(problems) == 0 {
		// Said plainly, so a model does not spend a turn trying different
		// spellings of a skill that was never there.
		return SuccessResult("No skills are available in this project."), nil
	}
	var b strings.Builder
	for _, sk := range all {
		kind := "read with skill_read"
		if sk.Runnable() {
			kind = "run with skill_run"
		}
		// Listed rather than hidden. A model that cannot see it retries under
		// other names; a model told it is pending knows to stop.
		if sk.Pending {
			kind = "pending acceptance — not usable in this run"
		}
		fmt.Fprintf(&b, "- %s (%s): %s\n", sk.Name, kind, strings.TrimSpace(sk.Description))
	}
	// Broken skills are named rather than silently missing: "the skill is not
	// in the list" and "the skill is broken" lead a model to different actions.
	for _, p := range problems {
		fmt.Fprintf(&b, "- (unavailable) %v\n", p)
	}
	return SuccessResult("%s", b.String()), nil
}

// SkillRead returns a skill's documentation.
type SkillRead struct{}

// Name returns the tool name.
func (t *SkillRead) Name() string   { return "skill_read" }
func (t *SkillRead) Mutating() bool { return false }

// Description returns the tool description.
func (t *SkillRead) Description() string {
	return "Read a skill's instructions. Use before skill_run, and for skills that are documentation only."
}

// Schema returns the argument schema.
func (t *SkillRead) Schema() interface{} {
	return NewSchema().AddString("name", "Skill name", true)
}

type skillNameArgs struct {
	Name string `json:"name"`
}

// Execute runs the tool.
func (t *SkillRead) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a skillNameArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	sk, err := skill.Find(ectx.ProjectRoot, ectx.GlobalSkillsDir, a.Name)
	if err != nil {
		return ErrorResult("%v", err), nil
	}
	markPending(ectx, []*skill.Skill{sk})
	if sk.Pending {
		return pendingRefusal(sk.Name), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n", sk.Name, strings.TrimSpace(sk.Description))
	if len(sk.Args) > 0 {
		b.WriteString("\nArguments:\n")
		for _, arg := range sk.Args {
			req := "optional"
			if arg.Required {
				req = "required"
			}
			fmt.Fprintf(&b, "- %s (%s, %s)\n", arg.Name, argType(arg.Type), req)
		}
	}
	if sk.Body != "" {
		b.WriteString("\n" + sk.Body + "\n")
	}
	if !sk.Runnable() {
		b.WriteString("\nThis skill is documentation. There is nothing to run.\n")
	}
	return SuccessResult("%s", b.String()), nil
}

func argType(t string) string {
	if t == "" {
		return "string"
	}
	return t
}

// SkillRun executes a skill's entry point.
type SkillRun struct{}

// Name returns the tool name.
func (t *SkillRun) Name() string { return "skill_run" }

// Mutating reports true: a skill runs a script, and a script can write.
func (t *SkillRun) Mutating() bool { return true }

// Description returns the tool description.
func (t *SkillRun) Description() string {
	return "Run a skill's entry point with arguments. Call skill_list first to see what exists."
}

// Schema returns the argument schema.
func (t *SkillRun) Schema() interface{} {
	return NewSchema().
		AddString("name", "Skill name", true).
		AddObject("args", "Arguments declared by the skill", false)
}

type skillRunArgs struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// Execute runs the tool.
//
// A skill is not a privilege escalation. It runs in the project root under the
// same timeout and denylist as the shell tool; see the note on the policy below
// for the one part that differs and why (05 §7).
func (t *SkillRun) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a skillRunArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	sk, err := skill.Find(ectx.ProjectRoot, ectx.GlobalSkillsDir, a.Name)
	if err != nil {
		return ErrorResult("%v", err), nil
	}
	markPending(ectx, []*skill.Skill{sk})
	if sk.Pending {
		return pendingRefusal(sk.Name), nil
	}
	if !sk.Runnable() {
		return ErrorResult("skill %q is documentation, not a script; read it with skill_read", sk.Name), nil
	}
	if problems := skill.Validate(sk); len(problems) > 0 {
		// Refusing to run an invalid skill is the point of validating at all.
		// A skill whose entry moved is a skill that will fail confusingly.
		return ErrorResult("skill %q is not valid: %s", sk.Name, strings.Join(problems, "; ")), nil
	}

	missing, unknown := checkSkillArgs(sk, a.Args)
	if len(missing) > 0 {
		return ErrorResult("skill %q needs %s", sk.Name, strings.Join(missing, ", ")), nil
	}
	if len(unknown) > 0 {
		// Named rather than dropped: a model that passed `file` where the
		// skill wants `path` otherwise sees the skill run and do nothing.
		return ErrorResult("skill %q does not take %s", sk.Name, strings.Join(unknown, ", ")), nil
	}

	cmd, env := skillCommand(sk, a.Args)
	// The shell policy applies, minus the allowlist (05 §7).
	//
	// "off" means off, and the denylist applies exactly as it does to a
	// command a model wrote. The allowlist is the one part that cannot: a
	// skill's command is an absolute path to a script, and no prefix list
	// contains one — applying it literally would make every skill unusable in
	// the default guarded mode.
	//
	// The allowlist exists because a model composes shell commands from
	// nothing. A skill is the opposite case: the script is a file a human read
	// and accepted before it could be called at all (05 §7.1). The model picks
	// which accepted script runs and supplies argument values, which are
	// quoted. That gate is the stronger one.
	skillPolicy := ectx.ShellPolicy
	if skillPolicy.Mode == "guarded" {
		skillPolicy.Mode = "free"
	}
	guardCtx := *ectx
	guardCtx.ShellPolicy = skillPolicy
	if guard := ShellPolicyCheck(&guardCtx, cmd); guard != nil {
		return guard, nil
	}

	timeoutS := sk.TimeoutS
	if timeoutS <= 0 {
		timeoutS = ectx.ShellPolicy.TimeoutS
	}
	if timeoutS <= 0 {
		timeoutS = 120
	}

	sub := *ectx
	sub.ShellEnv = append(os.Environ(), env...)
	output, exitCode, err := RunShell(ctx, &sub, cmd, timeoutS)
	if err != nil {
		return ErrorResult("skill %q: %v", sk.Name, err), nil
	}
	result := fmt.Sprintf("skill %s exit code: %d\n%s", sk.Name, exitCode, CapResult(output, MaxToolResultBytes))
	if exitCode != 0 {
		return &Result{Content: result, IsError: true}, nil
	}
	return SuccessResult("%s", result), nil
}

// checkSkillArgs compares what was passed against what was declared.
func checkSkillArgs(sk *skill.Skill, given map[string]interface{}) (missing, unknown []string) {
	declared := map[string]bool{}
	for _, arg := range sk.Args {
		declared[arg.Name] = true
		if arg.Required {
			if v, ok := given[arg.Name]; !ok || v == nil || fmt.Sprintf("%v", v) == "" {
				missing = append(missing, arg.Name)
			}
		}
	}
	for name := range given {
		if !declared[name] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	return missing, unknown
}

// skillCommand builds the command line and the environment for a skill.
//
// Arguments arrive twice, as the spec requires: as DUCKLAB_ARG_<NAME>
// environment variables and as positional --name=value (05 §7). A shell script
// can read whichever is more natural, and neither form needs the script to
// parse a JSON blob.
func skillCommand(sk *skill.Skill, given map[string]interface{}) (cmd string, env []string) {
	entry := filepath.Join(sk.Dir, sk.Entry)
	parts := []string{shellQuote(entry)}

	names := make([]string, 0, len(sk.Args))
	for _, arg := range sk.Args {
		names = append(names, arg.Name)
	}
	for _, name := range names {
		v, ok := given[name]
		if !ok || v == nil {
			continue
		}
		value := fmt.Sprintf("%v", v)
		parts = append(parts, shellQuote("--"+name+"="+value))
		env = append(env, "DUCKLAB_ARG_"+strings.ToUpper(strings.ReplaceAll(name, "-", "_"))+"="+value)
	}
	env = append(env, "DUCKLAB_SKILL_DIR="+sk.Dir)
	return strings.Join(parts, " "), env
}

// shellQuote wraps a value so the shell treats it as one word.
//
// Skill arguments come from a model, so a path with a space in it — or a
// semicolon — must not become a second command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
