package capability

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const CapabilityConformanceV1 = "fledge.capability-conformance/v1"

// StructuredReference is reference material the caller explicitly asks the
// harness to treat as an executable contract rather than advisory prose.
type StructuredReference struct {
	Source  string
	Content []byte
}

// ReferenceContract is the normalized, stack-neutral invariant extracted
// from one conformance document. Provenance binds the rule to the exact bytes
// supplied by the caller; a later edit is a different contract.
type ReferenceContract struct {
	SchemaVersion        string
	Operation            string
	RequiredOutputFields []string
	OptionalOutputFields []string
	AdditionalProperties bool
	Source               string
	Digest               string
}

// ReferenceOutputDeclaration is the canonical artifact representation of a
// normalized output contract. It preserves required/optional meaning and the
// additional-property policy instead of flattening them into prose.
type ReferenceOutputDeclaration struct {
	Required             []string `json:"required"`
	Optional             []string `json:"optional"`
	AdditionalProperties bool     `json:"additional_properties"`
}

const (
	referenceContractRegionBegin = "<!-- ducklab-reference-contracts:begin -->"
	referenceContractRegionEnd   = "<!-- ducklab-reference-contracts:end -->"
)

// RenderReferenceContractRegion serializes the exact normalized contracts and
// their provenance as machine-owned, visible artifact content. The same bytes
// are used in prompts and proposals so a model is never asked to reproduce
// metadata the harness already possesses.
func RenderReferenceContractRegion(contracts []ReferenceContract) string {
	if len(contracts) == 0 {
		return ""
	}
	ordered := append([]ReferenceContract(nil), contracts...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Operation == ordered[j].Operation {
			return ordered[i].Source < ordered[j].Source
		}
		return ordered[i].Operation < ordered[j].Operation
	})
	declarations := map[string]ReferenceOutputDeclaration{}
	for _, contract := range ordered {
		if _, exists := declarations[contract.Operation]; exists {
			continue
		}
		required := make([]string, len(contract.RequiredOutputFields))
		copy(required, contract.RequiredOutputFields)
		optional := make([]string, len(contract.OptionalOutputFields))
		copy(optional, contract.OptionalOutputFields)
		declarations[contract.Operation] = ReferenceOutputDeclaration{
			Required:             required,
			Optional:             optional,
			AdditionalProperties: contract.AdditionalProperties,
		}
	}
	encoded, _ := json.MarshalIndent(declarations, "", "  ")
	var b strings.Builder
	b.WriteString(referenceContractRegionBegin)
	b.WriteString("\n\n> **Machine-owned reference contracts.** Ducklab materialized this normalized metadata from the explicitly supplied structured references; model-authored prose cannot replace it.\n>\n> Provenance:\n")
	for _, contract := range ordered {
		fmt.Fprintf(&b, "> - `%s`: `%s` (%s)\n", contract.Operation, contract.Source, contract.Digest)
	}
	b.WriteString("\n```ducklab-reference-contracts\n")
	b.Write(encoded)
	b.WriteString("\n```\n\n")
	b.WriteString(referenceContractRegionEnd)
	return b.String()
}

type referenceEnvelope struct {
	SchemaVersion string          `json:"schema_version"`
	Operation     string          `json:"operation"`
	Contract      json.RawMessage `json:"contract"`
	Cases         []struct {
		ID       string          `json:"id"`
		Expected json.RawMessage `json:"expected"`
	} `json:"cases"`
}

type declaredContract struct {
	Output struct {
		Required             []string `json:"required"`
		Optional             []string `json:"optional"`
		AdditionalProperties *bool    `json:"additional_properties"`
	} `json:"output"`
}

// DeclaresReferenceContract distinguishes an ordinary JSON reference from a
// document that asks to participate in the mechanical artifact gate. Invalid
// declared documents are still declarations and therefore fail normalization
// instead of silently degrading to advisory prose.
func DeclaresReferenceContract(raw []byte) bool {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return false
	}
	_, declared := envelope["contract"]
	return declared
}

// NormalizeReferenceContracts parses declared conformance contracts, proves
// their own fixtures obey the declaration, and returns one stable invariant
// per operation. Conflicting declarations are rejected rather than resolved
// by input order.
func NormalizeReferenceContracts(refs []StructuredReference) ([]ReferenceContract, error) {
	byOperation := map[string]ReferenceContract{}
	out := make([]ReferenceContract, 0, len(refs))
	for _, ref := range refs {
		contract, err := normalizeReferenceContract(ref)
		if err != nil {
			return nil, err
		}
		if prior, exists := byOperation[contract.Operation]; exists {
			if !sameOutputContract(prior, contract) {
				return nil, fmt.Errorf("structured reference %q conflicts with %q for operation %q", ref.Source, prior.Source, contract.Operation)
			}
		} else {
			byOperation[contract.Operation] = contract
		}
		out = append(out, contract)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Operation == out[j].Operation {
			return out[i].Source < out[j].Source
		}
		return out[i].Operation < out[j].Operation
	})
	return out, nil
}

func normalizeReferenceContract(ref StructuredReference) (ReferenceContract, error) {
	var envelope referenceEnvelope
	if err := json.Unmarshal(ref.Content, &envelope); err != nil {
		return ReferenceContract{}, fmt.Errorf("structured reference %q: decode JSON: %w", ref.Source, err)
	}
	if envelope.SchemaVersion != CapabilityConformanceV1 {
		return ReferenceContract{}, fmt.Errorf("structured reference %q: unsupported schema_version %q", ref.Source, envelope.SchemaVersion)
	}
	if strings.TrimSpace(envelope.Operation) == "" {
		return ReferenceContract{}, fmt.Errorf("structured reference %q: operation is required", ref.Source)
	}
	if len(envelope.Contract) == 0 || string(envelope.Contract) == "null" {
		return ReferenceContract{}, fmt.Errorf("structured reference %q: declared contract is required", ref.Source)
	}
	var declared declaredContract
	if err := json.Unmarshal(envelope.Contract, &declared); err != nil {
		return ReferenceContract{}, fmt.Errorf("structured reference %q: decode contract: %w", ref.Source, err)
	}
	required, err := normalizedFieldNames(declared.Output.Required)
	if err != nil {
		return ReferenceContract{}, fmt.Errorf("structured reference %q: required output fields: %w", ref.Source, err)
	}
	optional, err := normalizedFieldNames(declared.Output.Optional)
	if err != nil {
		return ReferenceContract{}, fmt.Errorf("structured reference %q: optional output fields: %w", ref.Source, err)
	}
	if len(required) == 0 {
		return ReferenceContract{}, fmt.Errorf("structured reference %q: at least one required output field is required", ref.Source)
	}
	if declared.Output.AdditionalProperties == nil {
		return ReferenceContract{}, fmt.Errorf("structured reference %q: additional_properties must be declared explicitly", ref.Source)
	}
	for _, field := range optional {
		if containsString(required, field) {
			return ReferenceContract{}, fmt.Errorf("structured reference %q: output field %q is both required and optional", ref.Source, field)
		}
	}
	contract := ReferenceContract{
		SchemaVersion: envelope.SchemaVersion, Operation: envelope.Operation,
		RequiredOutputFields: required, OptionalOutputFields: optional,
		AdditionalProperties: *declared.Output.AdditionalProperties,
		Source:               ref.Source, Digest: contentDigest(ref.Content),
	}
	if len(envelope.Cases) == 0 {
		return ReferenceContract{}, fmt.Errorf("structured reference %q: at least one conformance case is required", ref.Source)
	}
	for _, fixture := range envelope.Cases {
		if err := contract.validateExpected(fixture.ID, fixture.Expected); err != nil {
			return ReferenceContract{}, fmt.Errorf("structured reference %q: %w", ref.Source, err)
		}
	}
	return contract, nil
}

// ValidateOutputFields compares a candidate's declared object fields with the
// normalized contract. Returned strings are deterministic mechanical
// violations, separate from reviewer findings.
func (c ReferenceContract) ValidateOutputFields(fields []string) []string {
	actual, err := normalizedFieldNames(fields)
	if err != nil {
		return []string{fmt.Sprintf("%s output contract: %v", c.Operation, err)}
	}
	var violations []string
	for _, required := range c.RequiredOutputFields {
		if !containsString(actual, required) {
			violations = append(violations, fmt.Sprintf("%s output is missing required field %q", c.Operation, required))
		}
	}
	if !c.AdditionalProperties {
		allowed := append(append([]string(nil), c.RequiredOutputFields...), c.OptionalOutputFields...)
		for _, field := range actual {
			if !containsString(allowed, field) {
				violations = append(violations, fmt.Sprintf("%s output declares forbidden field %q", c.Operation, field))
			}
		}
	}
	return violations
}

// ValidateOutputDeclaration proves that persisted machine-owned metadata still
// represents the normalized source contract, including required/optional
// classification and the additional-property policy.
func (c ReferenceContract) ValidateOutputDeclaration(declared ReferenceOutputDeclaration) []string {
	fields := append(append([]string(nil), declared.Required...), declared.Optional...)
	violations := c.ValidateOutputFields(fields)
	required, requiredErr := normalizedFieldNames(declared.Required)
	optional, optionalErr := normalizedFieldNames(declared.Optional)
	if requiredErr != nil {
		violations = append(violations, fmt.Sprintf("%s required output fields: %v", c.Operation, requiredErr))
	} else if strings.Join(required, "\x00") != strings.Join(c.RequiredOutputFields, "\x00") {
		violations = append(violations, fmt.Sprintf("%s required output fields do not match the source contract", c.Operation))
	}
	if optionalErr != nil {
		violations = append(violations, fmt.Sprintf("%s optional output fields: %v", c.Operation, optionalErr))
	} else if strings.Join(optional, "\x00") != strings.Join(c.OptionalOutputFields, "\x00") {
		violations = append(violations, fmt.Sprintf("%s optional output fields do not match the source contract", c.Operation))
	}
	if declared.AdditionalProperties != c.AdditionalProperties {
		violations = append(violations, fmt.Sprintf("%s additional_properties does not match the source contract", c.Operation))
	}
	return violations
}

func (c ReferenceContract) validateExpected(caseID string, raw json.RawMessage) error {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return fmt.Errorf("case %q expected must be a JSON object", caseID)
	}
	fields := make([]string, 0, len(value))
	for field := range value {
		fields = append(fields, field)
	}
	if violations := c.ValidateOutputFields(fields); len(violations) > 0 {
		return fmt.Errorf("case %q violates its declared output contract: %s", caseID, strings.Join(violations, "; "))
	}
	return nil
}

func normalizedFieldNames(fields []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(fields))
	for _, raw := range fields {
		field := strings.TrimSpace(raw)
		if field == "" {
			return nil, fmt.Errorf("field name is empty")
		}
		if seen[field] {
			return nil, fmt.Errorf("field %q is duplicated", field)
		}
		seen[field] = true
		out = append(out, field)
	}
	sort.Strings(out)
	return out, nil
}

func sameOutputContract(a, b ReferenceContract) bool {
	return strings.Join(a.RequiredOutputFields, "\x00") == strings.Join(b.RequiredOutputFields, "\x00") &&
		strings.Join(a.OptionalOutputFields, "\x00") == strings.Join(b.OptionalOutputFields, "\x00") &&
		a.AdditionalProperties == b.AdditionalProperties
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func contentDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("sha256:%x", sum)
}
