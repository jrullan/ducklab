package service

import (
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

// ensureHarnessProfile resolves once and persists before any coding seat is
// called. A resumed run reuses the record: stack probes are launch work, not a
// tax paid by every turn or verify_run.
func ensureHarnessProfile(rs *runState, root string, project *config.Project, taskVerification string) (string, error) {
	if rs.run.HarnessProfile != nil {
		return harnessCapsule(rs.run.HarnessProfile), nil
	}
	registry := capability.DefaultRegistry()
	resolved, detectErr := registry.ResolveProject(capability.Context{
		ProjectRoot: root, TaskVerification: taskVerification, Policies: project.Capabilities.Policy,
	}, project.Capabilities.Auto, project.Capabilities.Enabled, project.Capabilities.Disabled)
	checks, err := registry.ResolveChecks(capability.Context{
		ProjectRoot: root, TaskVerification: taskVerification, Policies: project.Capabilities.Policy,
	}, project.Capabilities.Auto, project.Capabilities.Enabled, project.Capabilities.Disabled)
	if err != nil {
		return "", fmt.Errorf("resolve harness checks: %w", err)
	}

	profile := &runlog.HarnessProfile{TaskVerification: taskVerification}
	for _, detected := range resolved.Detections {
		profile.Capabilities = append(profile.Capabilities, runlog.HarnessCapability{
			ID: detected.Capability, Evidence: detected.Evidence,
		})
	}
	if resolved.Gate != nil {
		profile.DetectedGate = &runlog.HarnessGate{
			Kind: resolved.Gate.Kind, Command: resolved.Gate.Command, Source: "detected",
		}
	}
	if detectErr != nil {
		profile.DetectionError = detectErr.Error()
	}
	profile.EffectiveGate = runlog.HarnessGate{
		Kind: project.Verify.Mode, Command: gateCommandFor(project.Verify), Source: "project",
	}
	if project.Verify.Mode == "auto" {
		profile.EffectiveGate.Source = "detected"
		if profile.DetectedGate != nil {
			profile.EffectiveGate.Kind = profile.DetectedGate.Kind
			profile.EffectiveGate.Command = profile.DetectedGate.Command
		}
	}
	for _, check := range checks {
		profile.Diagnostics = append(profile.Diagnostics, runlog.HarnessDiagnostic{
			Capability: check.Capability, Name: check.Name, Command: check.Command,
			Enforcement: string(check.Enforcement),
		})
	}

	rs.run.HarnessProfile = profile
	if err := rs.writer.AppendEvent("capabilities_resolved", map[string]interface{}{"profile": profile}); err != nil {
		return "", err
	}
	if err := rs.writer.WriteState(); err != nil {
		return "", err
	}
	return harnessCapsule(profile), nil
}

func harnessCapsule(profile *runlog.HarnessProfile) string {
	if profile == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Resolved project harness\n\nThese are harness facts, not suggestions to rewrite the accepted task or its gates.\n")
	if len(profile.Capabilities) > 0 {
		b.WriteString("- Stack: ")
		for i, item := range profile.Capabilities {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(item.ID)
			if len(item.Evidence) > 0 {
				fmt.Fprintf(&b, " (%s)", strings.Join(item.Evidence, ", "))
			}
		}
		b.WriteByte('\n')
	}
	if profile.TaskVerification != "" {
		fmt.Fprintf(&b, "- Task verification (authoritative): `%s`\n", capsuleCommand(profile.TaskVerification))
	}
	gate := profile.EffectiveGate
	if gate.Command != "" {
		fmt.Fprintf(&b, "- Project gate (%s, %s): `%s`\n", gate.Kind, gate.Source, capsuleCommand(gate.Command))
	} else {
		fmt.Fprintf(&b, "- Project gate: %s; no executable command\n", gate.Kind)
	}
	for _, diagnostic := range profile.Diagnostics {
		fmt.Fprintf(&b, "- Diagnostic (%s, %s): `%s`\n", diagnostic.Capability, diagnostic.Enforcement, capsuleCommand(diagnostic.Command))
	}
	if profile.DetectionError != "" {
		fmt.Fprintf(&b, "- Stack detection warning: %s\n", profile.DetectionError)
	}
	b.WriteString("Use these commands and paths as given. Fix the implementation when a gate fails; do not weaken or replace the gate.")
	return b.String()
}

func capsuleCommand(command string) string {
	command = strings.ReplaceAll(strings.TrimSpace(command), "`", "'")
	const max = 500
	if len(command) > max {
		return command[:max] + "…"
	}
	return command
}
