# 0009 — The normative spec moves in-repo

**Decision (2026-08-20):** the `ducklab-spec` repository's nine documents
(00-VISION … 08-DESKTOP-UI, plus its README) are vendored into `docs/spec/`.
`docs/spec/` is canonical from this commit; the separate repository is
retired and will not be published.

**Why.** The README promised the spec would be "published alongside" the
code — which meant every collaborator would meet a repo of claims they could
not read until a second publication happened, and a maintainer of one would
keep two repos synchronized for a boundary that only matters when different
teams own each half. One maintainer, one clone, one place to send a reader.

**What each spec is for.** This repo now carries three layers of truth:

| Layer | Where | Answers |
|---|---|---|
| Normative | `docs/spec/` | what the system MUST be: vision, invariants (I-numbers), protocol contracts, acceptance criteria |
| Divergence | `docs/decisions/` | where the code deliberately differs, and why |
| As-built | `.ducklab/docs/` | what the system IS today — requirements, spec, plan, maintained by the loop, signed at a human gate |

The diff between the first and the third is the roadmap, and the alignment
stage computes it — that is not a metaphor, it is the run that produced most
of the current plan.

**The normative spec shrinks from here.** Descriptive content that the
as-built spec now owns is not worth maintaining twice. What stays normative
is what a survey of the code can never produce: the vision, the invariants,
the contracts third parties depend on (agent protocol, skill format), and
acceptance criteria not yet met. Prune on touch, never in bulk.

**History.** The spec repo's git history stays in the retired repository;
this import is a snapshot at its final commit (`b5f865c`). The loss was
weighed: the decisions/ directory already tells the story of every
consequential change, and one history is enough.
