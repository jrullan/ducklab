# 0008 — The advisor is a positioned turn, not a background consult

**Status:** accepted, 2026-08-17
**Extends:** 05 §4.2 (pair), 04 §2.4 (`ask_advisor`), 04 §6 (role prompts).
Supersedes the in-run half of SPEC-053 (the paused-question draft stays).

## The problem

The advisor ran in a goroutine, racing the run it advised: it answered a
snapshot of a paused question while the implementer's line of thought moved
on, and its reply landed late or for a problem already solved. Its first
positioned form (T-059) fired on loose signals — four `fs_read` misses summoned
a seat to write a note no round read — and emitted its events inside the
implementer's turn, so the desktop showed the two running in parallel.

Meanwhile the one organ that could read the implementer's distress had no
hands: the reviewer must not read the implementer's reasoning (I2/I7), the
person was not watching, and $20 of a strong model went into a fight with
`fs_patch` that anyone reading the transcript would have stopped in a minute.

## The decision

1. **One deterministic moment.** The advisor speaks after the implementer's
   turn is closed on the record and before the reviewer opens — never
   concurrently, never nested.
2. **Only on measured distress.** Brake refusals, a failure streak of one
   tool, red gates, or an item the implementer's own deliverables report
   names undelivered. Counted, never inferred from prose: an operator's own
   vocabulary is exactly what a self-referential detector trips on. A rough
   but working turn costs no duck; no seat means no consult, on the record.
3. **It listens to what the judge may not.** The reasoning, the trace, the
   report with notes. The reviewer gets telemetry as data only.
4. **Three answers, and the loop shape follows.** `none` → reviewer. `note` →
   the implementer runs again *now*, note in hand, before any reviewer turn is
   spent — `[implementer ↔ advisor]` bounded to two retries per round, because
   the duck is a counselor and only the reviewer and the gate are the
   independent check. `stop` → the run pauses with its work in place, the
   record names who stopped it and what to change, and the redo note is born
   with the reshuffle.
5. **A door for the implementer.** `ask_advisor` answers inline and never
   pauses — `ask_human` with no human, for the small seat that knows it is
   stuck but not what to do.

## Why "rubber duck"

The implementer has been thinking for a while, is frustrated, and gets
nowhere; the duck listens — and, unlike the desk toy, also advises. The shape
is built for local seats coached by a stronger one: the counsel is one call
on a small trace, at the exact moment it can still change the outcome.

## What this rules out

- Any advisor path that runs while the implementer is still working.
- Distress detection by grepping the model's words.
- The reviewer receiving the implementer's admissions in any form but counts.
