---
kind: intent
version: 3
updated_at: 2026-09-09T10:04:38Z
approved_by: human
---

## INT-001 — The approved requirements were adopted on 2026-08-05 and the product …

**Run:** r-20260814-223257-jinz
**Submitted at:** 2026-08-14T22:32:57Z
**Outcome:** not accepted
**Requirements:** -

_Imported from the historical run record; section-level links may be unavailable._

### Original brief

The approved requirements were adopted on 2026-08-05 and the product has grown since. Survey the code as it stands (git log since Aug 5 is the map) and ADD requirements for capabilities the document does not yet cover. These are ALREADY BUILT — write them as adopted capabilities (Priority: must), same register as the existing REQs. Areas shipped since the adopt: plan amendment ("extend without redesign": 1-3 tasks, spec-debt markers, upward settle via spec_settle); fragment-based document updates (existing docs update by emitting changed sections only, merged deterministically) with section-at-a-time orchestration for small-context architects; context-fit preflight warnings and tool-result caps scaled to a seat's declared context; loop rails (consecutive gate-fail brake, identical-repeat-call brake, per-run model-call caps with live accounting and in-flight lifts); provider resilience (stall watchdog instead of stream timeouts, transient-exhaustion pauses as resumable weather, output-truncation pauses, resume for document stages); declared fallback ducklings with human-decided reseat (seat_failover recorded); per-run overrides at launch (seat picks, agent turn caps, screenshots to vision-capable architects); triage now also recommends a verification strategy per bug (test-first with repro sketch, or build-only when the honest check is eyes) and the task's front door follows it; taskless runs record a subject (which bugs a triage read); accepted test-first commits can be retired (reverted, recorded); signed bug audit trail; autopilot (starts next mechanical step, idles at human gates); app launcher (run/stop the project's app); project guide (computed next steps); spend reports per mode and per duckling.

## INT-002 — The approved requirements were adopted on 2026-08-05 and the product …

**Run:** r-20260814-231255-ym4e
**Submitted at:** 2026-08-14T23:12:55Z
**Outcome:** accepted
**Requirements:** -

_Imported from the historical run record; section-level links may be unavailable._

### Original brief

The approved requirements were adopted on 2026-08-05 and the product has grown since. Survey the code as it stands (git log since Aug 5 is the map) and ADD requirements for capabilities the document does not yet cover. These are ALREADY BUILT — write them as adopted capabilities (Priority: must), same register as the existing REQs. Areas shipped since the adopt: plan amendment ("extend without redesign": 1-3 tasks, spec-debt markers, upward settle via spec_settle); fragment-based document updates (existing docs update by emitting changed sections only, merged deterministically) with section-at-a-time orchestration for small-context architects; context-fit preflight warnings and tool-result caps scaled to a seat's declared context; loop rails (consecutive gate-fail brake, identical-repeat-call brake, per-run model-call caps with live accounting and in-flight lifts); provider resilience (stall watchdog instead of stream timeouts, transient-exhaustion pauses as resumable weather, output-truncation pauses, resume for document stages); declared fallback ducklings with human-decided reseat (seat_failover recorded); per-run overrides at launch (seat picks, agent turn caps, screenshots to vision-capable architects); triage now also recommends a verification strategy per bug (test-first with repro sketch, or build-only when the honest check is eyes) and the task's front door follows it; taskless runs record a subject (which bugs a triage read); accepted test-first commits can be retired (reverted, recorded); signed bug audit trail; autopilot (starts next mechanical step, idles at human gates); app launcher (run/stop the project's app); project guide (computed next steps); spend reports per mode and per duckling; the advisor, chat runs, and notifications you will find in the tree beyond this list.

## INT-003 — Q: Two drifts found between the requirements and the code. (1) REQ-01…

**Run:** r-20260905-142952-zjo4
**Submitted at:** 2026-09-05T14:29:52Z
**Outcome:** not accepted
**Requirements:** -

### Original brief

Q: Two drifts found between the requirements and the code. (1) REQ-011 says "Seven roles" but config.go defines nine (architect, implementer, reviewer, judge, triager, advisor, scribe, consultant, human) — the doc omits advisor and consultant. (2) REQ-012 lists run artifacts as `diff.txt` and `tests.txt`, but runlog actually writes `diff.patch`, `verify.log`, and `tests.patch`. Both are factual misstatements of what the code does. Which drifts should I correct in the requirements to match the code as it stands?
A: Fix all three (REQ-011 role count, REQ-012 filenames, add captures/brief). The requirements are as-built and must state the implementation contract accurately: REQ-011 names nine roles including advisor and consultant; REQ-012 lists the actual runlog artifacts diff.patch, verify.log and tests.patch, adding captures/brief if they are persisted artifacts. Decision by Jose, 2026-09-08 backlog audit.

