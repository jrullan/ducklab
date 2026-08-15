---
kind: plan
version: 0
approved_by: 
---

## M-001 — Reported bugs

### T-001 — Expose budget lifting through MCP and identify the invalid kind field in lift errors

Fixes B-004.

## Reported

What happened: both spec refresh runs (r-...-7s32, r-...-32gx) hit the default 3M token cap mid-council. run_get offered next: [resume, abort] — but resume continues the ledger from what was spent, so resuming without lifting the cap re-pauses immediately. The only lift door is POST /v1/runs/{id}/budget/lift over HTTP with the bearer token, which a remote MCP operator (Elena) cannot reach. A small operator obeying the offered actions enters resume→pause→resume forever.

Expected: the MCP surface closes the loop it opens — either a budget_lift tool (run_id, kind), or decide accepts a lift alongside resume ("resume with tokens uncapped"). Recorded like the desktop's lift: one-way, per-cap, attributed.

Related detail worth fixing in the same file: the lift endpoint's error for a wrong field names the valid VALUES ("no budget cap named ...") but not the FIELD (kind) — the artifact_read lesson applies (errors teach the field).

## Triage

**Component:** MCP budget controls

MCP offers resume for capped runs without exposing the required lift action, trapping remote operators in an endless pause-resume loop.

**Verification (triage recommends):** test-first — Pause a capped run, lift its budget through MCP, and verify resume proceeds; invalid lift fields should name kind.

This section is the triager's reading, not the reporter's. Check it rather than assume it.


