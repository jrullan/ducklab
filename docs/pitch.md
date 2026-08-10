# Ducklab — pitch deck source

*One slide per `##`. Written for developers who want engineering rigor from
AI coding, and who want to choose their models — local, OpenRouter, or both.*

---

## Ducklab

**Software engineering discipline for AI coding — with the models you choose.**

A full-cycle development harness where multiple models hold real roles, a
deterministic gate decides what passes, and every run leaves a record you can
audit. Local models are first-class citizens, not a fallback.

---

## The problem with today's harnesses

Claude Code, Codex, Pi, OpenCode are excellent **conversational copilots** —
and they share one shape:

- **One model, one seat.** The same model writes, reviews its own work, and
  declares it done.
- **The model grades itself.** "Tests pass" is whatever the model says it is.
- **Process lives in the prompt.** Discipline is a system prompt away from
  not happening.
- **The vendor picks your model.** Local models are an afterthought at best.
- **The record is a scrollback.** When something went wrong last Tuesday,
  good luck reconstructing why.

Great for conversation. Not built to be a *process*.

---

## Ducklab's bet

> The rigor is in the **harness**, not in the model.

If the structure enforces the discipline — separate roles, independent
review, deterministic verification, budgets, records — then even small local
models produce trustworthy work. And frontier models produce *auditable*
work.

---

## A fleet, not a copilot

Configure **ducklings**: any OpenAI-compatible endpoint.

- llama.cpp on your workstation, vLLM on your lab box, OpenRouter for
  frontier models — mixed freely **in the same run**.
- Per-model cost tables, context/caps declarations, vision support.
- Pair a local implementer with a frontier reviewer. Or the reverse.
- API keys live in your environment, never in config files or the UI.

Tokens on your own silicon are free. Ducklab is built to exploit that.

---

## Roles are law, not prompt

Every seat has a **toolbelt ceiling enforced by construction**:

- The **reviewer** physically cannot write files — not "is asked not to".
- The **implementer** builds; the reviewer never sees its rationalizations
  (independent second reading, by design).
- **Tournaments** are judged blind: candidates are anonymized before the
  judge reads them.
- A conversation script can *narrow* a role's tools, never widen them.

Self-review theater ends here.

---

## The gate is deterministic

Your verification command — pytest, go test, whatever you trust — decides
green or red. **No model ever grades its own work.**

- Green gate + human accept → committed. Every acceptance is a commit with
  a traceable run behind it.
- No executable gate? The run says **UNVERIFIED** — never "passed".
- Reviewer findings land as structured verdicts; dissent under a green gate
  is surfaced, not swallowed.

---

## Full cycle, not just code

Intake → **spec** → plan → tasks → **test-first builds** → review → bugs →
triage → release. All in the harness:

- **Test-first chains**: the test is written first, lands red, is committed
  — then the build runs against it. One authorization, whole chain.
- Specs and plans are drafted by model councils, **approved by you** —
  with guards against a draft silently gutting the approved document.
- Bugs with screenshots; a vision model triages them; a click promotes a
  bug to a task. Findings from reviews file straight into the bug board.
- Chat with any model *about* a task or bug — dossier loaded, read-only
  tools, and it can file the bug it just diagnosed when you say so.

---

## Budgets are governance, not decoration

- Tokens, dollars, turns, wallclock — per run, visible live, attributed
  **per model**.
- Any cap can be lifted mid-flight — one-way, recorded, the other caps
  still guarding. A run near its ceiling gets headroom instead of dying.
- **No error discards work**: budget death pauses the run with the work in
  place; resume continues the same ledger. Aborting is the only rollback.
- Cost-per-mode estimates shown *before* you launch, from your project's
  own history.

---

## Everything on the record

Runs are event-sourced: every turn, tool call, model call (including the
failed ones), diff, verdict, and human decision — in plain files beside
your repo.

- Reopen a run from last week and read exactly what happened, live-or-dead.
- Per-call token/cost ledger (`llm.jsonl`) — audit any model's bill.
- Prompt hygiene enforced: a 644KB generated file cannot ride a review
  prompt; oversized diffs collapse to honest stubs (that one bug was
  costing 5.4M tokens per review).

---

## Guardrails a security reviewer would ask for

- Shell **allowlist** with human approval for anything else.
- Path jail + write guards (`.git`, run records, protected globs).
- Write-capable tools reach a model only where a role's ceiling grants
  them.
- Autonomy levels per run — from manual to guarded to auto — with
  UNVERIFIED never auto-accepted, at any level.

---

## Ducklab vs the field

| | Claude Code / Codex / Pi / OpenCode | **Ducklab** |
|---|---|---|
| Seats | one model | a fleet, mixed local + API |
| Review | self-review | enforced independent reviewer / blind judge |
| Verification | model-reported | deterministic gate command |
| Lifecycle | chat + edits | spec → test-first → build → review → bugs → release |
| Cost | opaque meter | per-run, per-model budgets with live lifts |
| Record | scrollback | replayable event log + per-call ledger |
| Local models | afterthought | first-class, the design center |
| Discipline | in the prompt | in the construction |

---

## Who it's for

- Developers who believe **TDD, code review, and audit trails** shouldn't
  be suspended just because an AI is typing.
- Teams running **local inference** who want their hardware to punch above
  its weight through process, not prayer.
- Anyone who wants to **choose models per role** — cheap and fast where it
  fits, frontier where it counts — and see exactly what each one cost.

**Ducklab: the discipline is in the harness. The models are your call.**
