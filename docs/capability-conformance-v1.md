# Capability conformance v1

This suite captures the stack-neutral behaviour that an external capability
runtime must preserve before Ducklab may trust it. The fixtures are a public
wire-level contract: an implementation must not need to import Go packages or
read Ducklab's unit tests to determine the expected result.

This is a **shadow-parity contract**, not the eventual grant of execution
authority. It records the embedded runtime's legacy command strings so the two
runtimes can be compared without ambiguity. Before an external Feather may
govern a run, its gate proposal must cross a separate activation contract with
structured steps and an explicit human trust decision. Matching a legacy
compound command does not authorize executing it.

The H0 registry slice covers all six stack-neutral operations currently exposed
by the embedded runtime:

- `resolve_project`;
- `resolve_checks`;
- `resolve_inspections`;
- `inspect_plan_task`;
- `observe_gate`;
- `inspect_review_findings`.

Later H0 slices add filesystem-backed examples from the built-in providers and
the Feather v1 schema. New operations may be added to v1; the meaning of an
existing field or expected result may not change.

## Location

Fixtures live under:

```text
internal/capability/testdata/conformance/v1/
```

Every file is a JSON object with this shape:

```json
{
  "schema_version": "fledge.capability-conformance/v1",
  "operation": "resolve_project",
  "cases": []
}
```

Unknown schema versions or operations are failures, not invitations to guess.
Case IDs and provider IDs are unique within their respective scopes. Duplicate
provider identity is an invalid Feather installation even though the embedded
Go constructor historically accepts last-write-wins registration; conformance
fixtures do not rely on that accident.

## Provider input

A conformance provider is data rather than executable stack logic. Depending on
the operation it declares:

- `id`: unique provider identity;
- `detection`: capability identity and reproducible evidence;
- `gates`: proposed gate candidates;
- `review_rules`: bounded guidance contributed by a detected provider;
- `checks` and `inspections`: task diagnostics;
- `plan_inspections`: planning-contract findings;
- `gate_findings`: interpretations of completed gate evidence;
- `review_finding_inspections`: inadmissible reviewer remedies.

An empty `detection.evidence` means the provider did not match. Its review
rules are therefore absent. Gate candidates are still resolver inputs because
the embedded contract historically lets a detector describe availability and
matching through the candidate itself; concrete Feathers should normally emit
neither evidence nor gates when they do not match.

## Selection input

- `auto: true` initially selects every registered provider.
- `enabled` then adds named providers.
- `disabled` wins last and removes named providers.
- enabling an unknown provider returns a stable error.
- disabling an unknown provider is harmless.

Provider registration order cannot affect the result.

## Composition result

Detections are ordered by provider ID because selected providers are traversed
in that order. Review rules are ordered by `(capability, id)`.

Gate candidates are divided into primary and supplemental candidates. The
primary candidate with the lowest numeric priority wins, with capability ID as
the tie breaker. Supplemental commands are appended in the same ordering only
when the selected primary gate is an available `tests` gate. Supplemental
candidates never create a gate by themselves and are not appended to build or
lint gates.

The normalized output contains:

```json
{
  "detections": [],
  "gate": null,
  "review_rules": [],
  "error": ""
}
```

`error` is part of the output so implementations in different languages can be
compared without relying on exception types. The fixtures use relative paths,
stable strings, and no host toolchain probes.

## Operation-specific selection

`resolve_project`, `resolve_checks`, and `resolve_inspections` apply `auto`,
`enabled`, and `disabled`. Inspection errors identify the provider that failed.

`inspect_plan_task` asks every registered plan inspector to self-filter from the
task body and verification, matching the embedded contract. `observe_gate` and
`inspect_review_findings` receive an explicit `capability_ids` list representing
the profile already frozen onto the run; unknown IDs contribute nothing and no
stack detection recurs.

All findings are normalized in `(capability, name-or-kind)` order. Review
finding inspections with the same capability and rule are ordered by source
finding index. Empty results are JSON arrays, not `null`.

## Authority

Passing this suite establishes behavioural compatibility; it does not grant a
runtime or Feather authority to select an effective project gate. Ducklab owns
execution and records the human-approved pack version, digest, composition,
and proposed gate before that proposal may govern a run.

## H0 coverage

The initial corpus contains 29 cases. Synthetic providers cover every registry
operation, ordering rule, selection path, active-profile filter, supplemental
gate behaviour, and attributed error. Filesystem-backed cases exercise every
built-in provider family without depending on the developer's installed Go or
Python toolchain: declared tool probes are isolated with fixture executables.

| Provider | Behaviour pinned in the initial corpus |
|---|---|
| `go` | marker detection and successful test-collector probe |
| `python` | source/metadata detection and test-before-compile priority |
| `node` | package test detection and polyglot priority |
| `rust` | Cargo marker and test gate |
| `typescript` | config marker, build gate, and disabled composition |
| `c-native` | required API check, warning policy, and header inspection |
| `meson` | build-graph observation for an uncompiled source |
| `gtk4-ui` | planning rejection and reviewer-remedy rejection |
| `gtk4-clipboard` | unchecked publication result inspection |
| `glib-async` | impossible idle-source fallback inspection |
| `glib-options` | erased callback ABI inspection |
| `x11-image` | unshifted channel-mask inspection |

The corpus is a minimum interoperability boundary, not a replacement for the
embedded package's exhaustive regression tests. Complex providers gain more
portable cases as they enter shadow migration. H1 is required to implement the
neutral registry contract and its own initial Feathers; it is not required to
port every GTK/GLib rule before Rust itself has been characterized.
