package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPlanManifestRejectsMoreThanTenTasks(t *testing.T) {
	manifest := PlanManifest{Milestones: []ManifestMilestone{{ID: "M-01", Title: "Too wide"}}}
	for i := 1; i <= MaxPlanManifestTasks+1; i++ {
		manifest.Milestones[0].Tasks = append(manifest.Milestones[0].Tasks, ManifestTask{
			ID: fmt.Sprintf("T-%03d", i), Title: "Task", Implements: []string{"SPEC-001"},
			WorkUnit: "one concern", AcceptanceSlices: []string{"observable"},
			AcceptanceProbes: []string{fmt.Sprintf("test-task-%d", i)},
			Produces:         []string{fmt.Sprintf("file:src/task%d.rs", i)}, Verification: "cargo test",
		})
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseContract("json:plan_manifest", string(raw)); err == nil || !strings.Contains(err.Error(), "at most 10 tasks") {
		t.Fatalf("oversized manifest error = %v", err)
	}
}

func TestPlanManifestPatchIsTypedAndScopedToUniqueTasks(t *testing.T) {
	text := `{"operations":[{"op":"replace_task","task_id":"T-001","milestone_id":"M-01","task":{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build the app","acceptance_slices":["app compiles"],"acceptance_probes":["cargo check"],"produces":["file:Cargo.toml"],"consumes":[],"verification":"cargo check"}},{"op":"delete_task","task_id":"T-002"}]}`
	got, err := ParseContract("json:plan_manifest_patch", text)
	if err != nil {
		t.Fatal(err)
	}
	patch := got.(*PlanManifestPatch)
	if len(patch.Operations) != 2 || patch.Operations[0].Task.ID != "T-001" {
		t.Fatalf("patch = %#v", patch)
	}

	for _, invalid := range []string{
		`{"operations":[]}`,
		`{"operations":[{"op":"delete_task","task_id":"T-001"},{"op":"delete_task","task_id":"T-001"}]}`,
		`{"operations":[{"op":"add_task","task_id":"T-003","milestone_id":"M-01","task":{"id":"T-004"}}]}`,
		`{"operations":[{"op":"rewrite_everything","task_id":"T-001"}]}`,
	} {
		if _, err := ParseContract("json:plan_manifest_patch", invalid); err == nil {
			t.Errorf("invalid patch accepted: %s", invalid)
		}
	}
}

func TestPlanManifestPatchCapsOperationsForSmallStructuredReplies(t *testing.T) {
	operations := make([]string, 0, MaxPlanManifestPatchOperations+1)
	for i := 1; i <= MaxPlanManifestPatchOperations+1; i++ {
		operations = append(operations, fmt.Sprintf(`{"op":"delete_task","task_id":"T-%03d"}`, i))
	}
	text := `{"operations":[` + strings.Join(operations, ",") + `]}`
	_, err := ParseContract("json:plan_manifest_patch", text)
	if err == nil || !strings.Contains(err.Error(), "operations must contain 1-4 items, got 5") {
		t.Fatalf("oversized patch error = %v", err)
	}
}

func TestPlanManifestPatchAcceptsTypedMilestoneCreation(t *testing.T) {
	text := `{"operations":[{"op":"add_milestone","milestone_id":"M-02","milestone_title":"Runtime"},{"op":"add_task","task_id":"T-002","milestone_id":"M-02","task":{"id":"T-002","title":"Run","implements":["SPEC-002"],"work_unit":"run service","acceptance_slices":["service runs"],"acceptance_probes":["true"],"produces":["capability:runtime"],"consumes":[],"verification":"true"}}]}`
	got, err := ParseContract("json:plan_manifest_patch", text)
	if err != nil {
		t.Fatal(err)
	}
	patch := got.(*PlanManifestPatch)
	if patch.Operations[0].MilestoneID != "M-02" || patch.Operations[0].MilestoneTitle != "Runtime" {
		t.Fatalf("milestone operation = %#v", patch.Operations[0])
	}
}

func TestDecompositionContractAllowsLongBodies(t *testing.T) {
	declared := 12000
	got := outputCapForContract(&declared, "json:decomposition")
	if got == nil {
		t.Fatal("decomposition output cap is nil")
	}
	if *got <= 2048 {
		t.Fatalf("decomposition cap = %d, want more than the classification cap", *got)
	}

	// A caller's smaller declared limit remains an upper bound even for a
	// contract whose responses may legitimately be long.
	small := 1024
	got = outputCapForContract(&small, "json:decomposition")
	if got == nil || *got != small {
		t.Fatalf("decomposition cap for declared %d = %v, want declared limit", small, got)
	}
}

func TestVerdictContractParsesFindings(t *testing.T) {
	text := `{"verdict":"request-changes","findings":[
		{"severity":"major","file":"auth.go","line":88,"issue":"nil deref when the token is expired","fix":"guard before deref"},
		{"severity":"minor","file":"auth.go","line":12,"issue":"unused import","fix":"remove it"}]}`
	got, err := ParseContract("verdict", text)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := got.(*Verdict)
	if !ok {
		t.Fatalf("got %T, want *Verdict", got)
	}
	if v.Approved() {
		t.Error("request-changes reported as approved")
	}
	if len(v.Findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(v.Findings))
	}
	if v.Findings[0].Line != 88 || v.Findings[0].File != "auth.go" {
		t.Errorf("finding anchor lost: %+v", v.Findings[0])
	}
	if len(v.Blocking()) != 1 {
		t.Errorf("got %d blocking findings, want 1 (major)", len(v.Blocking()))
	}
}

func TestVerdictApproveWithNoFindingsIsValid(t *testing.T) {
	got, err := ParseContract("verdict", `{"verdict":"approve","findings":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !got.(*Verdict).Approved() {
		t.Error("approve not recognised")
	}
}

func TestPlanManifestVerdictRequiresCompleteAccountableAudit(t *testing.T) {
	contract := "verdict:plan_manifest:SPEC-001,SPEC-002|T-001,T-002"
	valid := `{"verdict":"request-changes","findings":[{"severity":"major","file":"manifest","line":0,"issue":"T-002 bundles selection and execution","fix":"keep only selection in its work unit"}],"manifest_audit":{"specs":[{"id":"SPEC-001","status":"pass","evidence":"T-001 slice 1 and probe 1 preserve the authority boundary"},{"id":"SPEC-002","status":"fail","evidence":"T-002 work unit combines selection and execution"}],"tasks":[{"id":"T-001","status":"pass","evidence":"one registry concern with matching slice and probe"},{"id":"T-002","status":"fail","evidence":"two independent actors occur in its work unit"}]}}`
	got, err := ParseContract(contract, valid)
	if err != nil {
		t.Fatal(err)
	}
	if got.(*Verdict).ManifestAudit == nil || len(got.(*Verdict).ManifestAudit.Tasks) != 2 {
		t.Fatalf("manifest audit lost: %#v", got)
	}

	for name, text := range map[string]string{
		"missing target":        strings.Replace(valid, `,{"id":"SPEC-002","status":"fail","evidence":"T-002 work unit combines selection and execution"}`, "", 1),
		"approval with failure": strings.Replace(valid, `"verdict":"request-changes"`, `"verdict":"approve"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseContract(contract, text); err == nil {
				t.Fatal("incomplete or contradictory manifest audit passed")
			}
		})
	}
}

func TestVerdictRejectsFindingsThatSayNothingIsWrong(t *testing.T) {
	for _, text := range []string{
		`{"verdict":"approve","findings":[{"severity":"minor","file":"x.h","issue":"Task delivered exactly what was asked — no defects found.","fix":"N/A"}]}`,
		`{"verdict":"approve","findings":[{"severity":"minor","file":"x.h","issue":"Looks good","fix":"No change needed"}]}`,
	} {
		if _, err := ParseContract("verdict", text); err == nil || !strings.Contains(err.Error(), "empty findings list") {
			t.Fatalf("no-op finding was accepted: %v", err)
		}
	}
}

// A reviewer cannot approve while reporting blocking problems; accepting that
// would let a run look approved with majors outstanding.
func TestVerdictApproveWithBlockingFindingsIsRejected(t *testing.T) {
	_, err := ParseContract("verdict", `{"verdict":"approve","findings":[
		{"severity":"critical","file":"a.go","line":1,"issue":"data loss","fix":"do not"}]}`)
	if err == nil {
		t.Fatal("approve with a critical finding was accepted")
	}
}

func TestVerdictRequestChangesRequiresABlockingFinding(t *testing.T) {
	_, err := ParseContract("verdict", `{"verdict":"request-changes","findings":[
		{"severity":"minor","file":"x.c","line":9,"issue":"optional cleanup","fix":"remove dead helper"}]}`)
	if err == nil || !strings.Contains(err.Error(), "no critical or major") {
		t.Fatalf("minor-only request-changes diagnosis = %v", err)
	}

	got, err := ParseContract("verdict", `{"verdict":"approve","findings":[
		{"severity":"minor","file":"x.c","line":9,"issue":"optional cleanup","fix":"remove dead helper"}]}`)
	if err != nil || !got.(*Verdict).Approved() {
		t.Fatalf("approve with a preserved minor observation was rejected: %v", err)
	}
}

func TestNativeVerdictRequiresConcreteSweepEvidence(t *testing.T) {
	base := `{"verdict":"approve","findings":[]}`
	if _, err := ParseContract("verdict:native", base); err == nil || !strings.Contains(err.Error(), "native_checks is required") {
		t.Fatalf("native approval without sweep evidence was accepted: %v", err)
	}

	generic := `{"verdict":"approve","findings":[],"native_checks":{"completion":"ok","resources":"x.c: allocations paired","threads":"x.c: thread unreffed","representation":"x.c: masks normalized","cleanup":"x.c: error paths release handles"}}`
	if _, err := ParseContract("verdict:native", generic); err == nil || !strings.Contains(err.Error(), "native_checks.completion") {
		t.Fatalf("generic native evidence was accepted: %v", err)
	}

	concrete := `{"verdict":"approve","findings":[],"native_checks":{"completion":"worker() returns the GTask on both success and error","resources":"image_destroy() frees pixels and ImageData","threads":"capture() refs task and unrefs the GThread handle","representation":"convert() uses ctz(mask), mask widths and XGetPixel","cleanup":"worker() closes Display and destroys XImage on every exit"}}`
	got, err := ParseContract("verdict:native", concrete)
	if err != nil || !got.(*Verdict).Approved() {
		t.Fatalf("concrete native sweep was rejected: %v", err)
	}
}

func TestVerdictRejectsBadSeverity(t *testing.T) {
	for _, body := range []string{
		`{"verdict":"request-changes","findings":[{"file":"a.go","issue":"x","fix":"y"}]}`,
		`{"verdict":"request-changes","findings":[{"severity":"nit","file":"a.go","issue":"x","fix":"y"}]}`,
		`{"verdict":"request-changes","findings":[{"severity":"major","file":"a.go","issue":"","fix":"y"}]}`,
	} {
		if _, err := ParseContract("verdict", body); err == nil {
			t.Errorf("accepted an invalid finding: %s", body)
		}
	}
}

func TestVerdictNamesAMissingFixPrecisely(t *testing.T) {
	_, err := ParseContract("verdict", `{"verdict":"request-changes","findings":[
		{"severity":"critical","file":"x.c","issue":"required file does not exist"}]}`)
	if err == nil || !strings.Contains(err.Error(), "has no fix") {
		t.Fatalf("missing fix diagnosis = %v, want a precise repair instruction", err)
	}
	if strings.Contains(err.Error(), "no defect") {
		t.Fatalf("a real defect was mislabeled as no defect: %v", err)
	}
}

func TestVerdictRejectsUnknownVerdict(t *testing.T) {
	if _, err := ParseContract("verdict", `{"verdict":"lgtm","findings":[]}`); err == nil {
		t.Error(`accepted verdict "lgtm"`)
	}
}

// Models wrap JSON in fences and prose even when told not to.
func TestContractToleratesFencesAndPreamble(t *testing.T) {
	texts := []string{
		"```json\n{\"verdict\":\"approve\",\"findings\":[]}\n```",
		"Here is my review:\n\n{\"verdict\":\"approve\",\"findings\":[]}",
		"```\n{\"verdict\":\"approve\",\"findings\":[]}\n```\n",
	}
	for _, text := range texts {
		if _, err := ParseContract("verdict", text); err != nil {
			t.Errorf("failed to extract JSON from %q: %v", text, err)
		}
	}
}

func TestExtractJSONHandlesBracesInStrings(t *testing.T) {
	text := `{"verdict":"request-changes","findings":[{"severity":"major","file":"a.go","line":1,"issue":"uses {placeholder} syntax","fix":"escape it"}]}`
	got, err := ParseContract("verdict", text)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.(*Verdict).Findings[0].Issue, "{placeholder}") {
		t.Error("braces inside a string broke extraction")
	}
}

// AC-21: prose instead of JSON must fail, never be guessed at.
func TestVerdictRejectsProse(t *testing.T) {
	prose := "The code looks good to me overall, I'd approve it."
	if _, err := ParseContract("verdict", prose); err == nil {
		t.Fatal("prose was accepted as a verdict")
	}
}

func TestChoiceContract(t *testing.T) {
	got, err := ParseContract("choice", `{"choice":"B","reason":"smallest change that passes"}`)
	if err != nil {
		t.Fatal(err)
	}
	c := got.(*Choice)
	if c.Choice != "B" || !c.Chosen() {
		t.Errorf("got %+v, want choice B", c)
	}
}

func TestChoiceNoneIsValidButNotChosen(t *testing.T) {
	got, err := ParseContract("choice", `{"choice":"none","reason":"all candidates ignore the task"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.(*Choice).Chosen() {
		t.Error(`"none" reported as chosen`)
	}
}

func TestChoiceRejectsBadLabelsAndMissingReason(t *testing.T) {
	for _, body := range []string{
		`{"choice":"candidate B","reason":"x"}`,
		`{"choice":"1","reason":"x"}`,
		`{"choice":"","reason":"x"}`,
		`{"choice":"A"}`,
		`{"choice":"A","reason":"   "}`,
	} {
		if _, err := ParseContract("choice", body); err == nil {
			t.Errorf("accepted invalid choice: %s", body)
		}
	}
}

func TestMarkdownSectionsContract(t *testing.T) {
	text := `Some preamble.

## REQ-001 — Users can log in
**Priority:** must

Body of the first requirement.

## REQ-002 - Sessions expire
Body of the second.
`
	got, err := ParseContract("markdown_sections:REQ", text)
	if err != nil {
		t.Fatal(err)
	}
	secs := got.([]Section)
	if len(secs) != 2 {
		t.Fatalf("got %d sections, want 2", len(secs))
	}
	if secs[0].ID != "REQ-001" || secs[0].Title != "Users can log in" {
		t.Errorf("section 0 = %+v", secs[0])
	}
	// Both an em dash and a hyphen separator must work; models produce both.
	if secs[1].ID != "REQ-002" || secs[1].Title != "Sessions expire" {
		t.Errorf("section 1 = %+v", secs[1])
	}
	if !strings.Contains(secs[0].Body, "Body of the first requirement.") {
		t.Errorf("body lost: %q", secs[0].Body)
	}
}

func TestMarkdownSectionsCanonicalizeNumericPadding(t *testing.T) {
	parsed, err := ParseContract("markdown_sections:REQ", "## REQ-0010 — Clipboard confirmation\n\nBody.\n")
	if err != nil {
		t.Fatal(err)
	}
	secs := parsed.([]Section)
	if len(secs) != 1 || secs[0].ID != "REQ-010" {
		t.Fatalf("sections = %+v, want canonical REQ-010", secs)
	}
}

func TestPlanManifestContractRejectsIncompleteTopology(t *testing.T) {
	valid := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build the app","acceptance_slices":["the app compiles"],"acceptance_probes":["meson compile -C build"],"produces":["build-target:app"],"consumes":[],"verification":"meson compile -C build"}]}]}`
	parsed, err := ParseContract("json:plan_manifest", valid)
	if err != nil {
		t.Fatal(err)
	}
	manifest := parsed.(*PlanManifest)
	if len(manifest.Milestones) != 1 || manifest.Milestones[0].Tasks[0].ID != "T-001" {
		t.Fatalf("manifest = %#v", manifest)
	}
	invalid := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":[],"work_unit":"","acceptance_slices":[],"acceptance_probes":[],"produces":[],"verification":""}]}]}`
	if _, err := ParseContract("json:plan_manifest", invalid); err == nil {
		t.Fatal("incomplete topology passed the manifest contract")
	}
}

func TestPlanManifestCanonicalizesNumericPadding(t *testing.T) {
	parsed, err := ParseContract("json:plan_manifest", `{"milestones":[{"id":"M-001","title":"Setup","tasks":[{"id":"T-0001","title":"Build","implements":["SPEC-001"],"work_unit":"build the app","acceptance_slices":["the app compiles"],"acceptance_probes":["true"],"produces":["build-target:app"],"consumes":[],"verification":"true"}]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	manifest := parsed.(*PlanManifest)
	if manifest.Milestones[0].ID != "M-01" || manifest.Milestones[0].Tasks[0].ID != "T-001" {
		t.Fatalf("manifest ids = %s/%s, want M-01/T-001", manifest.Milestones[0].ID, manifest.Milestones[0].Tasks[0].ID)
	}
}

func TestPlanManifestRejectsUnknownProtocolFields(t *testing.T) {
	text := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build app","acceptance_slices":["app builds"],"acceptance_probes":["true"],"produces":["file:app"],"consumes":[],"verification":"true","owns":["file:app"]}]}]}`
	if _, err := ParseContract("json:plan_manifest", text); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown manifest field error = %v", err)
	}
}

func TestPlanManifestRejectsDuplicateProducers(t *testing.T) {
	text := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[` +
		`{"id":"T-001","title":"Scaffold","implements":["SPEC-001"],"work_unit":"scaffold","acceptance_slices":["scaffold exists"],"acceptance_probes":["true"],"produces":["file:src/main.c"],"consumes":[],"verification":"true"},` +
		`{"id":"T-002","title":"Wire app","implements":["SPEC-002"],"work_unit":"wire app","acceptance_slices":["app is wired"],"acceptance_probes":["true"],"produces":[" file:src/main.c "],"consumes":[],"verification":"true"}` +
		`]}]}`
	if _, err := ParseContract("json:plan_manifest", text); err == nil || !strings.Contains(err.Error(), "both produce file:src/main.c") {
		t.Fatalf("duplicate producer error = %v", err)
	}
}

func TestPlanManifestRejectsUntypedArtifacts(t *testing.T) {
	text := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build app","acceptance_slices":["app builds"],"acceptance_probes":["true"],"produces":["src/main.rs"],"consumes":[],"verification":"true"}]}]}`
	if _, err := ParseContract("json:plan_manifest", text); err == nil || !strings.Contains(err.Error(), "must use file:") {
		t.Fatalf("untyped manifest artifact error = %v", err)
	}
}

func TestPlanManifestRequiresAtomicTaskContract(t *testing.T) {
	base := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":%s,"acceptance_slices":%s,"acceptance_probes":["true"],"produces":["build-target:app"],"consumes":[],"verification":"true"}]}]}`
	for _, tc := range []struct {
		name, work, slices string
	}{
		{"missing work unit", `""`, `["builds"]`},
		{"missing slices", `"build app"`, `[]`},
		{"empty slice", `"build app"`, `[""]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseContract("json:plan_manifest", fmt.Sprintf(base, tc.work, tc.slices)); err == nil {
				t.Fatal("non-atomic manifest task passed")
			}
		})
	}
}

func TestPlanManifestParserDoesNotOwnSupportProfileSliceLimit(t *testing.T) {
	text := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build app","acceptance_slices":["one","two","three","four"],"acceptance_probes":["test-one","test-two","test-three","test-four"],"produces":["build-target:app"],"consumes":[],"verification":"test-all"}]}]}`
	if _, err := ParseContract("json:plan_manifest", text); err != nil {
		t.Fatalf("structurally valid standard-profile manifest rejected: %v", err)
	}
}

func TestPlanManifestPromptOwnsSmallSeatSliceGuidance(t *testing.T) {
	small := planManifestPromptFor(true)
	standard := planManifestPromptFor(false)
	if !strings.Contains(small, "1-3 observable acceptance_slices") || !strings.Contains(small, "more than three slices") {
		t.Fatalf("small prompt lost slice ceiling:\n%s", small)
	}
	if strings.Contains(standard, "1-3 observable acceptance_slices") || strings.Contains(standard, "more than three slices") {
		t.Fatalf("standard prompt inherited small-seat slice guidance:\n%s", standard)
	}
	if !strings.Contains(standard, "do not split or merge solely because of its slice count") {
		t.Fatalf("standard prompt lacks cohesion guidance:\n%s", standard)
	}
}

func TestPlanManifestRequiresOneDistinctRawProbePerSlice(t *testing.T) {
	for name, probes := range map[string]string{
		"too few":   `["test-one"]`,
		"duplicate": `["test-all","test-all"]`,
		"markdown":  "[\"`test-one`\",\"test-two\"]",
	} {
		t.Run(name, func(t *testing.T) {
			text := fmt.Sprintf(`{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build app","acceptance_slices":["one","two"],"acceptance_probes":%s,"produces":["build-target:app"],"consumes":[],"verification":"test-all"}]}]}`, probes)
			if _, err := ParseContract("json:plan_manifest", text); err == nil {
				t.Fatal("invalid acceptance probes passed the manifest contract")
			}
		})
	}
}

func TestPlanManifestReportsAcceptanceProbeCardinality(t *testing.T) {
	text := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"build app","acceptance_slices":["one","two"],"acceptance_probes":["test-one","test-two","test-three"],"produces":["build-target:app"],"consumes":[],"verification":"test-all"}]}]}`
	_, err := ParseContract("json:plan_manifest", text)
	if err == nil || !strings.Contains(err.Error(), "T-001 acceptance_probes has 3 items, want 2") {
		t.Fatalf("probe cardinality error = %v", err)
	}
}

// H1t patches could carry several independent bad fields, but the transaction
// reported only the first. The retry fixed one field merely to discover the
// next, spending its only application retry on serial diagnosis.
func TestPlanManifestReportsAllIndependentTaskFieldErrors(t *testing.T) {
	text := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[` +
		`{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"","acceptance_slices":["one","two"],"acceptance_probes":["true"],"produces":["file:app"],"consumes":[],"verification":"true"},` +
		`{"id":"T-002","title":"Wire","implements":["REQ-002"],"work_unit":"wire app","acceptance_slices":["wired"],"acceptance_probes":["cargo test wire"],"produces":["src/main.rs"],"consumes":["Cargo.toml"],"verification":""}` +
		`]}]}`
	_, err := ParseContract("json:plan_manifest", text)
	if err == nil {
		t.Fatal("invalid fields were accepted")
	}
	for _, want := range []string{
		"T-001 work_unit must not be empty",
		"T-001 acceptance_probes has 1 items, want 2",
		`T-002 implements invalid specification id "REQ-002"`,
		`T-002 produced artifact "src/main.rs"`,
		`T-002 consumed artifact "Cargo.toml"`,
		"T-002 verification must not be empty",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not include %q", err, want)
		}
	}
}

func TestPlanManifestReportsMissingTaskField(t *testing.T) {
	text := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["SPEC-001"],"work_unit":"","acceptance_slices":["one"],"acceptance_probes":["test-one"],"produces":["build-target:app"],"consumes":[],"verification":"test-all"}]}]}`
	_, err := ParseContract("json:plan_manifest", text)
	if err == nil || !strings.Contains(err.Error(), "T-001 work_unit must not be empty") {
		t.Fatalf("missing work_unit error = %v", err)
	}
}

func TestPlanManifestRejectsNonSpecificationImplementsID(t *testing.T) {
	text := `{"milestones":[{"id":"M-01","title":"Setup","tasks":[{"id":"T-001","title":"Build","implements":["REQ-001"],"work_unit":"build app","acceptance_slices":["app builds"],"acceptance_probes":["true"],"produces":["build-target:app"],"consumes":[],"verification":"true"}]}]}`
	if _, err := ParseContract("json:plan_manifest", text); err == nil || !strings.Contains(err.Error(), "T-001 implements invalid specification id") {
		t.Fatalf("non-SPEC Implements error = %v", err)
	}
}

func TestMarkdownSectionsRejectsNoMatches(t *testing.T) {
	if _, err := ParseContract("markdown_sections:REQ", "## Introduction\n\nNo ids here."); err == nil {
		t.Error("accepted text with no matching sections")
	}
}

func TestFreeformAndEditsParseToNil(t *testing.T) {
	for _, c := range []string{"", "freeform", "edits"} {
		got, err := ParseContract(c, "anything at all")
		if err != nil || got != nil {
			t.Errorf("contract %q: got (%v, %v), want (nil, nil)", c, got, err)
		}
	}
}

func TestUnknownContractIsAnError(t *testing.T) {
	if _, err := ParseContract("telepathy", "{}"); err == nil {
		t.Error("unknown contract accepted")
	}
}

func TestEmptyResponseIsAnError(t *testing.T) {
	for _, c := range []string{"verdict", "choice", "json:triage"} {
		if _, err := ParseContract(c, "   "); err == nil {
			t.Errorf("contract %q accepted an empty response", c)
		}
	}
}

func TestTriageParsesAClassification(t *testing.T) {
	got, err := ParseContract("json:triage", `{"severity":"high","duplicate_of":null,
		"component":"auth","suspected_files":["auth.go"],"reproducible":true,
		"task_title":"Fix session timeout","reason":"the timer resets on every request"}`)
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := got.(*Triage)
	if !ok || tr == nil {
		t.Fatalf("got %T", got)
	}
	if tr.Severity != "high" || tr.Component != "auth" || tr.TaskTitle == "" {
		t.Errorf("parsed %+v", tr)
	}
	if tr.Reproducible == nil || !*tr.Reproducible {
		t.Error("reproducible was lost")
	}
}

// A bug whose reproducibility is unknown is not the same as one known not to
// reproduce, and flattening them would close real reports.
func TestTriageKeepsUnknownReproducibilityDistinctFromFalse(t *testing.T) {
	unknown, err := ParseContract("json:triage", `{"severity":"low","reproducible":null,"reason":"no steps given"}`)
	if err != nil {
		t.Fatal(err)
	}
	if r := unknown.(*Triage).Reproducible; r != nil {
		t.Errorf("unknown reproducibility became %v", *r)
	}
	no, err := ParseContract("json:triage", `{"severity":"low","reproducible":false,"reason":"cannot reproduce"}`)
	if err != nil {
		t.Fatal(err)
	}
	if r := no.(*Triage).Reproducible; r == nil || *r {
		t.Error("a definite no was lost")
	}
}

// A classification with no reason cannot be argued with, and it goes in front
// of a person who has to decide whether to trust it.
func TestTriageRequiresAReason(t *testing.T) {
	if _, err := ParseContract("json:triage", `{"severity":"high","reason":"  "}`); err == nil {
		t.Error("a classification with no reason was accepted")
	}
}

func TestTriageRejectsAnUnknownSeverity(t *testing.T) {
	if _, err := ParseContract("json:triage", `{"severity":"spicy","reason":"x"}`); err == nil {
		t.Error("an unknown severity was accepted")
	}
}

// The implementer's licence to ask. Sonnet burned two million tokens
// deliberating where a "week" starts, with ask_human sitting unused in its
// toolbelt — because nothing in its instructions said asking was ever the
// right move, and models treat unsanctioned asking as failure. The rule cuts
// both ways: user-observable decisions the task left open get asked; internals
// never do, or every task would pause on trivia.
func TestTheImplementerIsToldWhenToAsk(t *testing.T) {
	for _, want := range []string{"ask_human", "do not guess", "needed outcome or decision", "never approval to run a shell command", "never ask about those"} {
		if !strings.Contains(implementerPrompt, want) {
			t.Errorf("the implementer prompt does not say %q", want)
		}
	}
	if strings.Contains(strings.ToLower(implementerPrompt), "ask the human for approval") {
		t.Errorf("the implementer prompt must not promise shell-command approval: %q", implementerPrompt)
	}
}

// The strategy normalizes from what models actually type, and NEVER invents:
// an absent or unrecognizable answer stays empty — no recommendation.
func TestTriageTestStrategyNormalizes(t *testing.T) {
	for in, want := range map[string]string{
		"test-first": "test-first", "Test First": "test-first", "TESTS": "test-first",
		"build-only": "build-only", "build only": "build-only", "Only build": "build-only",
		"": "", "whatever": "",
	} {
		got, err := parseTriage(`{"severity":"low","duplicate_of":null,"component":"x",` +
			`"suspected_files":[],"reproducible":true,"task_title":"t",` +
			`"test_strategy":"` + in + `","test_reason":"r","reason":"because"}`)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got.TestStrategy != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got.TestStrategy, want)
		}
	}
}
