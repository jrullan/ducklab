package service

import (
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

// attachReviewContractValidator composes stack-specific reviewer checks onto
// the generic agent contract-repair mechanism. Only capabilities frozen onto
// this run participate; resumption therefore cannot silently re-detect a new
// policy from a changing worktree.
func attachReviewContractValidator(ectx *tools.ExecContext) {
	if ectx == nil {
		return
	}
	active := append([]string(nil), ectx.ActiveCapabilities...)
	ectx.NormalizeContract = func(role config.Role, contract string, parsed interface{}) (bool, error) {
		if role != config.RoleReviewer || (contract != "verdict" && contract != "verdict:native") {
			return false, nil
		}
		verdict, ok := parsed.(*agent.Verdict)
		if !ok || verdict == nil {
			return false, nil
		}
		if count := len(ectx.TaskAcceptanceProbes); count > 0 {
			if len(verdict.AcceptanceEvidence) != count {
				return false, fmt.Errorf("verdict contract: acceptance_evidence must contain exactly %d entries, one for each accepted slice/probe", count)
			}
			seen := make(map[int]bool, count)
			for _, evidence := range verdict.AcceptanceEvidence {
				if evidence.Slice < 1 || evidence.Slice > count || seen[evidence.Slice] {
					return false, fmt.Errorf("verdict contract: acceptance_evidence must identify each slice 1..%d exactly once", count)
				}
				seen[evidence.Slice] = true
				status := strings.ToLower(strings.TrimSpace(evidence.Status))
				if status != "pass" && status != "fail" {
					return false, fmt.Errorf("verdict contract: acceptance_evidence slice %d status must be pass or fail", evidence.Slice)
				}
				detail := strings.ToLower(strings.Trim(strings.TrimSpace(evidence.Evidence), "."))
				if detail == "" || detail == "ok" || detail == "verified" || detail == "green" || detail == "pass" || detail == "fail" {
					return false, fmt.Errorf("verdict contract: acceptance_evidence slice %d must name concrete observed output or behavior", evidence.Slice)
				}
				if verdict.Approved() && status != "pass" {
					return false, fmt.Errorf("verdict contract: approval requires acceptance_evidence slice %d to pass", evidence.Slice)
				}
			}
		}
		findings := make([]capability.ReviewFinding, 0, len(verdict.Findings))
		for _, finding := range verdict.Findings {
			findings = append(findings, capability.ReviewFinding{Issue: finding.Issue, Fix: finding.Fix, Invariant: finding.Invariant})
		}
		rejected := map[int]capability.ReviewFindingInspection{}
		for index, finding := range verdict.Findings {
			fix := strings.ToLower(strings.TrimSpace(finding.Fix))
			issue := strings.ToLower(strings.TrimSpace(finding.Issue))
			if strings.Contains(fix, "no change required") || strings.Contains(fix, "already does this") {
				rejected[index] = capability.ReviewFindingInspection{Index: index, Inspection: capability.Inspection{
					Capability: "review-contract", Name: "self-negating-finding", Enforcement: capability.Required,
					Detail: fmt.Sprintf("finding %d is inadmissible: its own remedy says the current code requires no change or already performs the requested action", index),
				}}
				continue
			}
			if (strings.Contains(issue, "might ") || strings.Contains(issue, "may be ") || strings.Contains(issue, "could be ")) &&
				(strings.HasPrefix(fix, "verify ") || strings.HasPrefix(fix, "check whether ") || strings.HasPrefix(fix, "confirm ")) {
				rejected[index] = capability.ReviewFindingInspection{Index: index, Inspection: capability.Inspection{
					Capability: "review-contract", Name: "speculative-finding", Enforcement: capability.Required,
					Detail: fmt.Sprintf("finding %d is inadmissible: it states only a possibility and asks the implementer to perform the reviewer's verification; report concrete evidence or omit it", index),
				}}
			}
		}
		for _, finding := range capability.DefaultRegistry().InspectReviewFindings(findings, active) {
			if finding.Enforcement == capability.Required {
				rejected[finding.Index] = finding
			}
		}
		if len(rejected) == 0 {
			return false, nil
		}
		kept := verdict.Findings[:0]
		for index, finding := range verdict.Findings {
			if rejectedFinding, found := rejected[index]; found {
				if ectx.OnDistress != nil {
					ectx.OnDistress("review_finding_rejected", map[string]interface{}{
						"index": index, "capability": rejectedFinding.Capability,
						"rule": rejectedFinding.Name, "detail": rejectedFinding.Detail,
					})
				}
				continue
			}
			kept = append(kept, finding)
		}
		verdict.Findings = kept
		if verdict.Verdict == "request-changes" && len(kept) == 0 {
			verdict.Verdict = "approve"
		}
		return true, nil
	}
}

// ensureHarnessProfile resolves once and persists before any coding seat is
// called. A resumed run reuses the record: stack probes are launch work, not a
// tax paid by every turn or verify_run.
func ensureHarnessProfile(rs *runState, root string, project *config.Project, taskVerification string, acceptanceProbes, buildGraphFiles []string) (string, error) {
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

	profile := &runlog.HarnessProfile{
		TaskVerification: taskVerification,
		AcceptanceProbes: append([]string(nil), acceptanceProbes...),
		BuildGraphFiles:  append([]string(nil), buildGraphFiles...),
	}
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
	for _, rule := range resolved.ReviewRules {
		profile.ReviewRules = append(profile.ReviewRules, runlog.HarnessReviewRule{
			Capability: rule.Capability, ID: rule.ID, Guidance: rule.Guidance,
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
	for index, probe := range profile.AcceptanceProbes {
		fmt.Fprintf(&b, "- Acceptance probe %d (authoritative): `%s`\n", index+1, capsuleCommand(probe))
	}
	if len(profile.BuildGraphFiles) > 0 {
		fmt.Fprintf(&b, "- Accepted dependency sources required in the build graph: %s\n", strings.Join(profile.BuildGraphFiles, ", "))
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
	for _, rule := range profile.ReviewRules {
		fmt.Fprintf(&b, "- Stack invariant (%s/%s): %s\n", rule.Capability, rule.ID, rule.Guidance)
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
