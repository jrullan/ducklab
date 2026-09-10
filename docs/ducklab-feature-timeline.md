# Ducklab feature timeline and the AI-Native SDLC playbook

This timeline compares Ducklab's development history with Anthropic's
publication of [*The AI-Native SDLC playbook*][playbook]. It is intended as a
chronology, not as evidence of causation or independent access to unpublished
work.

Anthropic published the playbook on **August 21, 2026** and records a last
modification date of **August 26, 2026**. Ducklab's first implementation commit
is dated **July 25, 2026**, 27 days before publication.

## Timeline

| Date | Major Ducklab development | Relationship to the playbook |
| --- | --- | --- |
| **July 25** | **v0.1:** Core packages and the engine skeleton are implemented. | Ducklab development begins before the article. |
| **July 26** | Duckling roles and toolbelt ceilings, the conversation engine, architect/reviewer pairing, tournament mode, the run queue, reports, and mode dispatch. | Specialized agent roles and bounded collaboration are already present. |
| **July 26–27** | **v0.3–v0.4 foundations:** Versioned artifacts, proposals, traceability, councils, stage execution, lifecycle orchestration, and the task board. | Ducklab establishes a staged workflow and an artifact chain that the playbook later describes as central to an AI-native SDLC. |
| **July 28** | Review and release stages, bug triage, skills, and `verify_run` gates. | Review, testing, release, and governance become parts of the agentic loop rather than external activities. |
| **July 29–30** | Test-first execution, revision at human gates, budgets and cost accounting, evidence capture, and engine hardening in response to a real project. | Ducklab already treats development as an observable and governed feedback loop. |
| **August 4** | Existing codebases gain a dedicated adoption workflow. | The lifecycle expands from greenfield development to brownfield work. |
| **August 6** | Test-first runs chain directly into implementation. | Verification starts directing the next action rather than checking only at the end. |
| **August 14** | The first explicit small-seat rails forgive recoverable mistakes, stop repeated calls, and point models at missing information. | Ducklab begins specializing in making smaller models reliable inside the lifecycle. |
| **August 18 — v0.6.0** | Roster and seating, acceptance and verification gates, provider resilience, autopilot guidance, loop and repetition controls, provenance, bug lifecycle, MCP operation, and token limits. | Most of the broad architectural backbone associated with the playbook is present before publication. |
| **August 20 — v0.6.2** | The final tagged Ducklab release before the article. | This is Ducklab's immediate pre-publication baseline. |
| **August 21** | **Anthropic publishes *The AI-Native SDLC playbook*.** | Primary comparison point. |
| **August 23–28 — v0.7.0 to v0.9.3** | Better adoption evidence, recovery guidance for interrupted runs, verifiable acceptance, remote workflows, configuration, document handling, and run observability. | Ducklab continues moving toward auditable artifacts and automated handoffs. |
| **August 29** | **Human Intent becomes a first-class, version-controlled Ducklab artifact.** | This closely resembles the playbook's `intent.md` concept, but the implementation lands eight days after publication. |
| **August 31** | Intent provenance is included in reviewed candidates. | The initiating decision becomes part of the downstream audit trail. |
| **September 3–4** | Neocapture cold-transfer experiments run under frozen harness configurations. | Ducklab begins testing whether the harness generalizes to smaller models and a C/GTK stack. |
| **September 5–7** | Fledge transfer experiments add capability conformance work, support tiers, seeded manifests, reasoning/content observability, and transactional patching. | Ducklab evolves from implementing an agentic SDLC into experimentally evaluating its reliability. |
| **September 8 — v0.9.4** | Planning and document composition, review and implementation quality, Intent, UI workflow, and release behavior are consolidated. | The artifact-driven lifecycle is hardened using evidence from the transfer experiments. |
| **September 9 — v0.9.5** | Artifact and task workflow, releases and acceptance, desktop behavior, budgets, service behavior, and project/file integrity improve. | Reliability and operational governance receive another concentrated pass. |
| **September 9–10** | Backlog work addresses grammar contracts, revision chaining, reviewer preflight, bounded plan amendments, request visibility, retries, and run recovery. | The interfaces and controls needed for repeatable agent execution become stricter and more explicit. |

## Architectural overlap at publication

By August 21, Ducklab already contained most of the broad ideas that overlap
with the playbook:

- a staged lifecycle operated by agents;
- specialized architect, reviewer, advisor, and implementation roles;
- version-controlled artifacts passed between stages;
- automated verification and explicit acceptance gates;
- human decisions at consequential boundaries;
- traceability from requirements through implementation;
- failures recorded and fed into later iterations;
- skills and constraints applied during generation; and
- auditable runs, decisions, costs, tool calls, and findings.

The clearest later convergence is **Intent**. The playbook presents
`intent.md` as the initiating artifact; Ducklab made Intent first-class on
August 29. The published page also contains an example that labels an
`intent.md` as June 2, 2026. That is evidence about the content of the
published page, but the example date may be illustrative and does not by
itself establish when Anthropic developed the concept.

## What this chronology can and cannot establish

The repository history establishes that Ducklab preceded the article in much
of the general architecture. It also identifies features that landed after
publication. Temporal and conceptual overlap alone does **not** establish that
Anthropic or Claude had access to Ducklab or used it to develop the playbook.

Testing that stronger hypothesis would require separate provenance evidence:
dated pre-publication conversations or shared documents, access records, and
distinctive Ducklab concepts or language that later appeared in the article.
Those questions are deliberately outside this timeline.

## Sources and method

- Ducklab dates come from the repository's commit and annotated-tag history;
  they are commit chronology, not estimates based on recollection.
- Release scope is cross-checked against the release notes in
  [`.ducklab/docs/releases/`](../.ducklab/docs/releases/).
- The publication and modification dates come from the structured metadata and
  visible date on the [official article][playbook].
- Dates are presented in Ducklab's repository timezone where applicable.

[playbook]: https://claude.com/blog/the-ai-native-sdlc-playbook
