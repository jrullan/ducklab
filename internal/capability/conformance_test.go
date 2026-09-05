package capability

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

const conformanceSchemaV1 = "fledge.capability-conformance/v1"

type conformanceFile struct {
	SchemaVersion string            `json:"schema_version"`
	Operation     string            `json:"operation"`
	Cases         []conformanceCase `json:"cases"`
}

type conformanceCase struct {
	ID              string                  `json:"id"`
	Registry        string                  `json:"registry"`
	Providers       []conformanceProvider   `json:"providers"`
	Files           map[string]string       `json:"files"`
	Tools           map[string]portableTool `json:"tools"`
	Selection       conformanceSelection    `json:"selection"`
	Context         portableContext         `json:"context"`
	PlanTask        portablePlanTask        `json:"plan_task"`
	GateObservation portableGateObservation `json:"gate_observation"`
	ReviewFindings  []portableReviewFinding `json:"review_findings"`
	CapabilityIDs   []string                `json:"capability_ids"`
	Expected        json.RawMessage         `json:"expected"`
}

type conformanceSelection struct {
	Auto     bool     `json:"auto"`
	Enabled  []string `json:"enabled"`
	Disabled []string `json:"disabled"`
}

type portableTool struct {
	ExitCode int `json:"exit_code"`
}

type portableContext struct {
	ProjectRoot      string            `json:"project_root"`
	TaskVerification string            `json:"task_verification"`
	ProducedFiles    []string          `json:"produced_files"`
	ConsumedFiles    []string          `json:"consumed_files"`
	Policies         map[string]string `json:"policies"`
}

func (c portableContext) native() Context {
	return Context{
		ProjectRoot: c.ProjectRoot, TaskVerification: c.TaskVerification,
		ProducedFiles: c.ProducedFiles, ConsumedFiles: c.ConsumedFiles, Policies: c.Policies,
	}
}

type portablePlanTask struct {
	ID           string `json:"id"`
	Body         string `json:"body"`
	Verification string `json:"verification"`
}

func (p portablePlanTask) native() PlanTaskContext {
	return PlanTaskContext{ID: p.ID, Body: p.Body, Verification: p.Verification}
}

type portableGateObservation struct {
	ProjectRoot     string   `json:"project_root"`
	Diff            string   `json:"diff"`
	Output          string   `json:"output"`
	BuildGraphFiles []string `json:"build_graph_files"`
}

func (o portableGateObservation) native() GateObservation {
	return GateObservation{
		ProjectRoot: o.ProjectRoot, Diff: o.Diff, Output: o.Output,
		BuildGraphFiles: o.BuildGraphFiles,
	}
}

type portableReviewFinding struct {
	Issue     string `json:"issue"`
	Fix       string `json:"fix"`
	Invariant string `json:"invariant"`
}

func nativeReviewFindings(findings []portableReviewFinding) []ReviewFinding {
	out := make([]ReviewFinding, 0, len(findings))
	for _, finding := range findings {
		out = append(out, ReviewFinding{Issue: finding.Issue, Fix: finding.Fix, Invariant: finding.Invariant})
	}
	return out
}

type conformanceProvider struct {
	ProviderID               string                            `json:"id"`
	Detection                Detection                         `json:"detection"`
	Gates                    []portableGate                    `json:"gates"`
	ReviewRules              []ReviewRule                      `json:"review_rules"`
	CheckResults             []Check                           `json:"checks"`
	Inspections              []Inspection                      `json:"inspections"`
	InspectionError          string                            `json:"inspection_error"`
	PlanInspections          []Inspection                      `json:"plan_inspections"`
	GateFindings             []GateFinding                     `json:"gate_findings"`
	ReviewFindingInspections []portableReviewFindingInspection `json:"review_finding_inspections"`
}

type portableDetection struct {
	Capability string   `json:"capability"`
	Evidence   []string `json:"evidence"`
}

type portableGate struct {
	Capability   string `json:"capability"`
	Kind         string `json:"kind"`
	Command      string `json:"command"`
	Scope        string `json:"scope"`
	Priority     int    `json:"priority"`
	Supplemental bool   `json:"supplemental"`
	Unavailable  string `json:"unavailable,omitempty"`
}

type portableReviewRule struct {
	Capability string `json:"capability"`
	ID         string `json:"id"`
	Guidance   string `json:"guidance"`
}

type portableCheck struct {
	Capability  string      `json:"capability"`
	Name        string      `json:"name"`
	Command     string      `json:"command"`
	Enforcement Enforcement `json:"enforcement"`
}

type portableInspection struct {
	Capability  string      `json:"capability"`
	Name        string      `json:"name"`
	Detail      string      `json:"detail"`
	Enforcement Enforcement `json:"enforcement"`
}

type portableGateFinding struct {
	Capability  string      `json:"capability"`
	Kind        string      `json:"kind"`
	Detail      string      `json:"detail"`
	Files       []string    `json:"files"`
	Enforcement Enforcement `json:"enforcement"`
}

type portableReviewFindingInspection struct {
	Index      int                `json:"index"`
	Inspection portableInspection `json:"inspection"`
}

type resolveProjectResult struct {
	Detections  []portableDetection  `json:"detections"`
	Gate        *portableGate        `json:"gate"`
	ReviewRules []portableReviewRule `json:"review_rules"`
	Error       string               `json:"error"`
}

type checksResult struct {
	Checks []portableCheck `json:"checks"`
	Error  string          `json:"error"`
}

type inspectionsResult struct {
	Inspections []portableInspection `json:"inspections"`
	Error       string               `json:"error"`
}

type gateFindingsResult struct {
	Findings []portableGateFinding `json:"findings"`
}

type reviewInspectionsResult struct {
	Inspections []portableReviewFindingInspection `json:"inspections"`
}

func (p conformanceProvider) ID() string { return p.ProviderID }

func (p conformanceProvider) Detect(Context) Contributions {
	gates := make([]GateCandidate, 0, len(p.Gates))
	for _, gate := range p.Gates {
		candidate := GateCandidate{
			Capability: gate.Capability, Kind: gate.Kind, Command: gate.Command,
			Scope: gate.Scope, Priority: gate.Priority, Supplemental: gate.Supplemental,
		}
		if gate.Unavailable != "" {
			candidate.Unavailable = errors.New(gate.Unavailable)
		}
		gates = append(gates, candidate)
	}
	return Contributions{Detection: p.Detection, Gates: gates, ReviewRules: p.ReviewRules}
}

func (p conformanceProvider) Checks(Context) []Check {
	return append([]Check(nil), p.CheckResults...)
}

func (p conformanceProvider) Inspect(Context) ([]Inspection, error) {
	if p.InspectionError != "" {
		return nil, errors.New(p.InspectionError)
	}
	return append([]Inspection(nil), p.Inspections...), nil
}

func (p conformanceProvider) InspectPlanTask(PlanTaskContext) []Inspection {
	return append([]Inspection(nil), p.PlanInspections...)
}

func (p conformanceProvider) ObserveGate(GateObservation) []GateFinding {
	return append([]GateFinding(nil), p.GateFindings...)
}

func (p conformanceProvider) InspectReviewFindings([]ReviewFinding) []ReviewFindingInspection {
	out := make([]ReviewFindingInspection, 0, len(p.ReviewFindingInspections))
	for _, inspection := range p.ReviewFindingInspections {
		out = append(out, ReviewFindingInspection{
			Index: inspection.Index,
			Inspection: Inspection{
				Capability: inspection.Inspection.Capability, Name: inspection.Inspection.Name,
				Detail: inspection.Inspection.Detail, Enforcement: inspection.Inspection.Enforcement,
			},
		})
	}
	return out
}

func TestFledgeCapabilityConformance(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "conformance", "v1", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no Fledge conformance fixtures found")
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fixture conformanceFile
		if err := json.Unmarshal(raw, &fixture); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if fixture.SchemaVersion != conformanceSchemaV1 {
			t.Fatalf("%s: unsupported schema_version %q", path, fixture.SchemaVersion)
		}
		if len(fixture.Cases) == 0 {
			t.Fatalf("%s: conformance file has no cases", path)
		}
		if !knownConformanceOperation(fixture.Operation) {
			t.Fatalf("%s: unsupported operation %q", path, fixture.Operation)
		}
		caseIDs := map[string]bool{}
		for _, testCase := range fixture.Cases {
			if testCase.ID == "" || caseIDs[testCase.ID] {
				t.Fatalf("%s: case IDs must be non-empty and unique; got %q", path, testCase.ID)
			}
			caseIDs[testCase.ID] = true
			t.Run(fixture.Operation+"/"+testCase.ID, func(t *testing.T) {
				registry := conformanceRegistry(t, &testCase)
				got := runConformanceOperation(t, fixture.Operation, registry, testCase)
				assertConformanceJSON(t, got, testCase.Expected)
			})
		}
	}
}

func conformanceRegistry(t *testing.T, testCase *conformanceCase) *Registry {
	t.Helper()
	if len(testCase.Files) > 0 || len(testCase.Tools) > 0 {
		root := t.TempDir()
		for relative, body := range testCase.Files {
			clean := filepath.Clean(relative)
			if filepath.IsAbs(clean) || clean == ".." || len(clean) > 3 && clean[:3] == ".."+string(filepath.Separator) {
				t.Fatalf("fixture path escapes its root: %q", relative)
			}
			path := filepath.Join(root, clean)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if len(testCase.Tools) > 0 {
			bin := filepath.Join(root, ".fixture-bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, tool := range testCase.Tools {
				if filepath.Base(name) != name || name == "." || name == "" {
					t.Fatalf("fixture tool must be a bare executable name: %q", name)
				}
				if tool.ExitCode < 0 || tool.ExitCode > 9 {
					t.Fatalf("fixture tool exit_code must be between 0 and 9: %d", tool.ExitCode)
				}
				body := []byte("#!/bin/sh\nexit " + strconv.Itoa(tool.ExitCode) + "\n")
				if err := os.WriteFile(filepath.Join(bin, name), body, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
		testCase.Context.ProjectRoot = root
		if testCase.GateObservation.ProjectRoot == "$fixture" {
			testCase.GateObservation.ProjectRoot = root
		}
	}
	if testCase.Registry == "default" {
		if len(testCase.Providers) > 0 {
			t.Fatal("default registry cases cannot declare synthetic providers")
		}
		return DefaultRegistry()
	}
	if testCase.Registry != "" && testCase.Registry != "synthetic" {
		t.Fatalf("unknown registry %q", testCase.Registry)
	}
	providers := make([]Provider, 0, len(testCase.Providers))
	providerIDs := map[string]bool{}
	for _, provider := range testCase.Providers {
		if provider.ProviderID == "" || providerIDs[provider.ProviderID] {
			t.Fatalf("provider IDs must be non-empty and unique; got %q", provider.ProviderID)
		}
		providerIDs[provider.ProviderID] = true
		providers = append(providers, provider)
	}
	return NewRegistry(providers...)
}

func knownConformanceOperation(operation string) bool {
	switch operation {
	case "resolve_project", "resolve_checks", "resolve_inspections", "inspect_plan_task", "observe_gate", "inspect_review_findings":
		return true
	default:
		return false
	}
}

func runConformanceOperation(t *testing.T, operation string, registry *Registry, testCase conformanceCase) interface{} {
	t.Helper()
	switch operation {
	case "resolve_project":
		profile, err := registry.ResolveProject(testCase.Context.native(), testCase.Selection.Auto, testCase.Selection.Enabled, testCase.Selection.Disabled)
		return normalizeProject(profile, err)
	case "resolve_checks":
		checks, err := registry.ResolveChecks(testCase.Context.native(), testCase.Selection.Auto, testCase.Selection.Enabled, testCase.Selection.Disabled)
		result := checksResult{Checks: portableChecks(checks)}
		if err != nil {
			result.Error = err.Error()
		}
		return result
	case "resolve_inspections":
		inspections, err := registry.ResolveInspections(testCase.Context.native(), testCase.Selection.Auto, testCase.Selection.Enabled, testCase.Selection.Disabled)
		result := inspectionsResult{Inspections: portableInspections(inspections)}
		if err != nil {
			result.Error = err.Error()
		}
		return result
	case "inspect_plan_task":
		return inspectionsResult{Inspections: portableInspections(registry.InspectPlanTask(testCase.PlanTask.native()))}
	case "observe_gate":
		return gateFindingsResult{Findings: portableGateFindings(registry.ObserveGate(testCase.GateObservation.native(), testCase.CapabilityIDs))}
	case "inspect_review_findings":
		return reviewInspectionsResult{Inspections: portableReviewInspections(registry.InspectReviewFindings(nativeReviewFindings(testCase.ReviewFindings), testCase.CapabilityIDs))}
	default:
		t.Fatalf("unsupported conformance operation %q", operation)
		return nil
	}
}

func assertConformanceJSON(t *testing.T, got interface{}, expected json.RawMessage) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, expectedValue interface{}
	if err := json.Unmarshal(gotJSON, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(expected, &expectedValue); err != nil {
		t.Fatalf("decode expected result: %v", err)
	}
	if !reflect.DeepEqual(gotValue, expectedValue) {
		prettyGot, _ := json.MarshalIndent(gotValue, "", "  ")
		prettyExpected, _ := json.MarshalIndent(expectedValue, "", "  ")
		t.Fatalf("result mismatch\ngot:  %s\nwant: %s", prettyGot, prettyExpected)
	}
}

func normalizeProject(profile Profile, err error) resolveProjectResult {
	result := resolveProjectResult{
		Detections:  make([]portableDetection, 0, len(profile.Detections)),
		ReviewRules: make([]portableReviewRule, 0, len(profile.ReviewRules)),
	}
	for _, detection := range profile.Detections {
		result.Detections = append(result.Detections, portableDetection{Capability: detection.Capability, Evidence: detection.Evidence})
	}
	for _, rule := range profile.ReviewRules {
		result.ReviewRules = append(result.ReviewRules, portableReviewRule{Capability: rule.Capability, ID: rule.ID, Guidance: rule.Guidance})
	}
	if err != nil {
		result.Error = err.Error()
	}
	if profile.Gate != nil {
		result.Gate = &portableGate{
			Capability: profile.Gate.Capability, Kind: profile.Gate.Kind, Command: profile.Gate.Command,
			Scope: profile.Gate.Scope, Priority: profile.Gate.Priority, Supplemental: profile.Gate.Supplemental,
		}
		if profile.Gate.Unavailable != nil {
			result.Gate.Unavailable = profile.Gate.Unavailable.Error()
		}
	}
	return result
}

func portableChecks(checks []Check) []portableCheck {
	out := make([]portableCheck, 0, len(checks))
	for _, check := range checks {
		out = append(out, portableCheck{Capability: check.Capability, Name: check.Name, Command: check.Command, Enforcement: check.Enforcement})
	}
	return out
}

func portableInspections(inspections []Inspection) []portableInspection {
	out := make([]portableInspection, 0, len(inspections))
	for _, inspection := range inspections {
		out = append(out, portableInspection{Capability: inspection.Capability, Name: inspection.Name, Detail: inspection.Detail, Enforcement: inspection.Enforcement})
	}
	return out
}

func portableGateFindings(findings []GateFinding) []portableGateFinding {
	out := make([]portableGateFinding, 0, len(findings))
	for _, finding := range findings {
		out = append(out, portableGateFinding{
			Capability: finding.Capability, Kind: finding.Kind, Detail: finding.Detail,
			Files: finding.Files, Enforcement: finding.Enforcement,
		})
	}
	return out
}

func portableReviewInspections(inspections []ReviewFindingInspection) []portableReviewFindingInspection {
	out := make([]portableReviewFindingInspection, 0, len(inspections))
	for _, inspection := range inspections {
		out = append(out, portableReviewFindingInspection{
			Index: inspection.Index,
			Inspection: portableInspection{
				Capability: inspection.Capability, Name: inspection.Name,
				Detail: inspection.Detail, Enforcement: inspection.Enforcement,
			},
		})
	}
	return out
}
