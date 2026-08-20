# 05 — Lifecycle, Conversations and Modes

`spec-1.2` — pair gains the rubber duck and the deliverables contract (§4.2);
acceptance from a clean checkout borrows installed dependency trees (§5.4).
`spec-1.1` — unchanged in substance from `spec-1.0`. Two reading notes for the
daemon topology: every "the human is asked" below happens through the
**Human-gate inbox** (`08-DESKTOP-UI.md §3.2`) or `ducklab run answer`, never by
blocking a terminal; and every stage step 5–8 of §1.1 runs inside the engine, so
no client needs to be attached for a run to reach its gate and wait there.

---

## 1. The seven stages

```
 intake ──▶ spec ──▶ plan ──▶ build ──▶ review ──▶ release
   ▲                            ▲                     │
   └──────────── operate ◀──────┴─────────────────────┘
```

| Stage | Input | Conversation | Output artifact | Gate |
|-------|-------|--------------|-----------------|------|
| `intake` | human intent, optional seed doc | `council` | `docs/requirements.md` (REQ-*) | human approve |
| `spec` | approved requirements | `council` | `docs/spec.md` (SPEC-*) | human approve + trace: every `must` REQ covered |
| `plan` | spec | `council` | `docs/plan.md`, milestones + tasks in DB | human approve + trace: every task implements a SPEC |
| `build` | one task | `solo`/`pair`/`tournament`/`split` | a diff on a branch | verify gate + human accept |
| `review` | a task's diff | `solo`/`council` | `docs/reviews/<id>.md` | reviewer verdict + human |
| `release` | accepted tasks since last release | `solo` (scribe) | `docs/releases/<v>.md`, tag | human approve, then deploy recipe |
| `operate` | bug reports | `solo` (triager) | bug records, promoted tasks | human approve unless autonomy ≥ auto |

Stages are **not** enforced as a strict sequence. A user may run `build` on a
hand-written task with no requirements at all; `trace check` will report the gap
and nothing will block. Ducklab records the truth; it does not impose ceremony.

### 1.1 Stage execution (uniform)

```
1. resolve roster for the stage
2. create run dir, write state.json (status=running)
3. run the conversation script
4. produce the candidate output (artifact proposal, or a working-tree diff)
5. run the gate (verify) — for artifact stages the gate is `none` plus the
   deterministic trace checks
6. compute the verdict
7. present to the human (diff of the artifact, or the code diff + gate output)
8. on accept: commit the change / promote the proposal, update DB, append to
   project.md, mark run accepted
   on reject: keep the proposal, record the reason as a failed attempt
9. write transcript.md, close the run
```

Steps 5–8 are **never** delegated to a model.

---

## 2. Autonomy levels

`project.toml → autonomy`, overridable per command with `--autonomy`.

| Level | Human is asked… | Shell tool | Notes |
|-------|-----------------|------------|-------|
| `manual` | before every mutating tool call | guarded, per-call approval | For untrusted models or dangerous repos. |
| `guarded` (default) | at the end of each run, before commit | guarded | The recommended mode. |
| `auto` | at the end of each **milestone** | guarded | Runs a queue of tasks; stops on the first red gate. |
| `yolo` | never | free | Still bound by the gate: a red run is not committed. Prints a warning banner. |

**No autonomy level can bypass the gate.** `yolo` means "don't ask me", never
"pretend it passed" (P3).

---

## 3. The conversation engine

### 3.1 Model

A `conv.Script` (see `01 §4.5`) is a list of turns plus a round cap plus a
termination expression. The engine:

```
for round := 1; round <= script.MaxRounds; round++ {
    emit round_start
    for i, turn := range script.Turns {
        if state.Skip(turn) { continue }
        out := agent.RunTurn(turn, state)
        state.Record(turn, out)
        checkpoint()
    }
    if eval(script.Until, state) { break }
}
```

Turn order is fixed data. **A model never decides who speaks next.** This is the
central determinism property (P1); the price is that Ducklab cannot do
free-form multi-agent chatter, and that price is paid deliberately.

### 3.2 Roster resolution

`Turn.Duckling` empty → look up `Turn.Role` in the project roster → if that
duckling's `Roles` list is non-empty and excludes the role, fail with a config
error naming both. `--ducklings a,b,c` overrides positionally for the
multi-contestant modes.

If two turns in the same round resolve to the **same** duckling for roles that
are supposed to be decorrelated (`implementer` vs `reviewer`, contestants in
`tournament`), Ducklab prints a warning: `"pair mode with the same duckling on
both sides measures self-consistency, not review"`. It runs anyway — that
comparison is itself a legitimate experiment — but the warning is recorded in
the run manifest so reports can segment it.

### 3.3 Termination expressions

`Script.Until` is a tiny expression language evaluated by
`internal/conv/expr.go`. Grammar:

```
expr    := term (("and" | "or") term)*
term    := "not"? atom
atom    := IDENT (("==" | "!=") LITERAL)? | "(" expr ")"
```

Available identifiers:

| Ident | Type | Meaning |
|-------|------|---------|
| `gate` | string | `green`, `red`, `none` — result of the last `verify_run` |
| `verdict` | string | last reviewer verdict: `approve`, `request-changes`, `` |
| `changed` | bool | working tree differs from the run's base commit |
| `choice` | string | last judge choice: `A`, `B`, …, `none`, `` |
| `round` | int | current round number |
| `no_findings` | bool | last reviewer returned an empty findings list |

Examples: `gate == "green" and verdict == "approve"`, `choice != "none"`.
Anything not in this table is a script-load error. There is no arbitrary code
evaluation, ever.

---

## 4. The five duck modes

All five ship as `conv.Script` values in `internal/strategy`. Each is defined
here completely enough to implement without further design.

### 4.1 `solo` — the yardstick

```
turns:
  - role: implementer, toolbelt: full, contract: edits, max_turns: 24
until: gate == "green"
max_rounds: 3
```

One duckling, all tools, iterating against the gate. **This is the control arm.**
Every other mode's value is measured as a delta against `solo` on the same task
with the same duckling. `ducklab report` treats it as the baseline.

Between rounds, the implementer is given the gate output verbatim (capped at 8
KB, tail-biased) and told to fix it.

### 4.2 `pair` — driver and navigator

```
turns:
  - role: implementer, toolbelt: full,       contract: edits
  - role: reviewer,    toolbelt: read-only,  contract: verdict, anonymize: false
until: gate == "green" and verdict == "approve"
max_rounds: 3
```

Round *n*'s reviewer findings are injected into round *n+1*'s implementer prompt
as:

```
## Review of your previous attempt
- [major] auth.go:88 — nil deref when the token is expired → guard before deref
```

The reviewer sees the **diff**, not the implementer's reasoning: a reviewer that
reads the author's rationalisation adopts it. This is why `pair` is more than
one model talking to itself.

#### The rubber duck (spec-1.2)

Between the implementer and the reviewer sits a third seat that usually says
nothing: the **advisor**. It takes a turn at exactly one deterministic moment —
the implementer's turn is closed on the record, the reviewer has not spoken —
and only when the harness **measured** distress in that turn:

- a brake refused a call (`REFUSED:` — the repeat brake, the fs_patch brake,
  the gate brake),
- five consecutive failures of one tool,
- three red `verify_run`s, or
- the implementer's own **deliverables report** names an item undelivered.

Counted, never inferred from prose. A rough-but-working turn costs no duck.
No advisor seated: the consult is skipped on the record and never fails a run.

The duck reads what the reviewer must not — the implementer's reasoning, its
final words, its tool trace, its report with notes; I2 binds the judge, not the
counselor — and answers with one of three things:

| answer | effect |
|---|---|
| `none` | the turn was rough but the implementer got through; on to the reviewer |
| `note` | the implementer runs **again, now, note in hand** — `[implementer ↔ advisor]` loops before the reviewer, at most twice per round; advice applied warm costs one implementer turn, wounded work sent to review costs a reviewer turn and the next round. The note also stays for later rounds. |
| `stop` | the run is not converging: it pauses with its work in place (no error discards work), the record carries `advisor_stop {reason, reshuffle}` and `Resolution: stopped by advisor <seat>`, and the redo note is born with the reshuffle. |

The reviewer receives the measured telemetry **as data** (`{"refusals":1,
"failure_streak":28,"failure_streak_tool":"fs_patch","gate_reds":0,
"undelivered":[4]}`), never a narrative, so its verdict can tell wounded
execution from wrong design without reading a rationalisation.

The implementer may also summon the duck itself, mid-turn, with `ask_advisor`
(04 §2.4) — the answer lands inline and the run never pauses.

#### The deliverables contract (spec-1.2)

The task's top-level bullets are **what** must be delivered — the plan's or the
promotion's words, numbered, never the implementer's own: a model that writes
its own list grades itself against a target it can quietly narrow, and I2 says
the model does not define its success criterion. **How** it gets there stays
the implementer's. Bullets after an *Out of scope* marker and indented
sub-bullets are not deliverables; a task with no bullets is one deliverable,
itself.

The implementer's prompt ends with the numbered list and the instruction to
close its reply with one JSON object reporting each by number:

```
{"deliverables":[{"id":1,"status":"done"},{"id":4,"status":"blocked","note":"…"}]}
```

`status ∈ done | partial | not_done | blocked`. Anything not `done` is a
distress signal (above). The reviewer gets ids and statuses as data plus the
numbered list — a rubric to verify against the diff — and never the notes
(I7). An approve over items the implementer itself reported undelivered is
recorded as `deliverables_gap`. A missing report is data for the reviewer, not
distress: a seat learning the contract is not punished; the parse is tolerant
and never fails a turn.

If `max_rounds` is exhausted with a green gate but unresolved `minor` findings,
the run's verdict is `PASSED` and the findings are attached to the run as
follow-up notes. Unresolved `critical`/`major` findings with a green gate
produce verdict `PASSED` with a loud warning — the tests are still ground truth
(I2), but the human sees the disagreement.

### 4.3 `tournament` — independent attempts, arbitrated

```
phase 1: N implementers (default 2) run CONCURRENTLY, each in its own git
         worktree, each unaware of the others.
phase 2: Ducklab runs the gate in each worktree. Results: green set G.
phase 3: |G| == 1  → short_circuit: apply that candidate verbatim (I8). Done.
         |G| >  1  → judge turn over the green candidates only → apply choice.
         |G| == 0  → judge turn over all candidates:
                      choice != none → apply it, then continue as `pair`
                                       for up to 2 rounds to fix the gate
                      choice == none → verdict FAILED, all candidates kept
                                       under runs/<id>/candidates/
```

Resolution is recorded as one of `short_circuit`, `judge_pick`,
`judge_pick_red`, `no_winner`, and reported per mode.

Hard rule (I8): a green candidate is applied **verbatim**. The judge evaluates;
it never rewrites. On modest models, free regeneration corrupts working code —
this is the most important lesson baked into the design.

Candidates are anonymised as A/B/… by order of *completion time hashed*, not by
duckling order, so the judge cannot learn a positional convention across runs.

### 4.4 `council` — the artifact modes

Used by `intake`, `spec`, `plan`. No code is written.

```
turns:
  - role: architect, contract: markdown_sections:<PREFIX>   # drafts
  - role: reviewer,  contract: verdict                       # one turn PER CRITIC
    (repeated for each duckling in the line-up after the first, pinned to it)
  - role: human,     contract: freeform                      # optional, see below
  - role: architect, contract: markdown_sections:<PREFIX>   # revises, or keeps
until: verdict == "approve" or round == max_rounds
max_rounds: 2
```

The council seats **one drafter plus N critics**: the first duckling of the
mode's line-up drafts, and every further duckling gets its own critique turn,
pinned to it. The product's thesis is decorrelation between models; a draft
read by N models with N different blind spots is that thesis applied to
documents. Two rules keep the extra seats honest:

- **Critics do not read each other.** Each critique turn sees the draft with
  the other reviewers' turns omitted (I7's mechanism) — a critic shown a
  fellow critic's findings anchors on them, and N critics collapse into one
  critique read N times. The architect's revision turn sees every critique.
- **The round's verdict is the worst across critics.** One `request-changes`
  among approvals is a request for changes; folding by overwrite would let the
  last critic to speak decide for everyone.

With a line-up of one or none, the council keeps its original two-chair shape:
the roster's reviewer takes the single critique turn.

The `human` turn is **conditional**: it executes when autonomy is `manual` or
when the architect's turn used `ask_human`; otherwise it is skipped. The human's
input is injected as a first-class turn, which is the literal expression of the
rubber-duck premise — the user is one of the ducks.

For `intake` specifically, the architect's first turn is an **interview**: it is
given `ask_human` and instructed to ask at most 5 questions, one message at a
time, before drafting. Under `--yes` or a non-TTY, it skips straight to drafting
from the seed document and marks every gap with `**Assumption:**`.

**Adoption** (`intake --adopt`) is the intake variant for a codebase that
already exists: instead of interviewing, the architect surveys the tree and
drafts the requirements the code ALREADY satisfies — inferred intent marked
with `**Assumption:**`, nothing aspirational invented. Valid only on intake
and only while the project has no approved requirements; after approval the
extension flow is the development model. The surveyed proposal carries
`origin: adopted` in its frontmatter (see `02`): derived by a model, not
decided by a person — same human gate, honest provenance. A seed passed
alongside travels as context, not as the task.

Adoption propagates downstream. The first spec of an adopted project is a
survey too — it inherits `origin: adopted`, and its prompt instructs the
architect to mark every section the code already implements with
`**As-built:** yes` (a section-level field, so it survives extensions, which
preserve section bodies verbatim). The marker is load-bearing three ways: the
trace check treats as-built sections as delivered by the tree rather than
demanding tasks for them; the plan prompt forbids tasks for them — a task to
build what is built is invented work; and a first `plan` over a fully
as-built spec is refused outright ("nothing to plan"), because the plan of an
adopted project grows from feature briefs and bug promotions, which create it
on their own.

### 4.5 `split` — decompose to raise the ceiling

For tasks beyond one model's context or capacity.

```
phase 1 (architect): decompose the task into 2–5 subtasks, each with an
        explicit FILE OWNERSHIP LIST. Contract: json:decomposition.
        {"subtasks":[{"title":…,"files":["src/a.go"],"body":…}]}
phase 2 (validation, deterministic): reject the decomposition if two subtasks
        claim the same file, or if any file is outside the repo. Retry the
        architect once with the conflict named; then abort.
phase 3: each subtask runs `solo` (or `pair`) CONCURRENTLY in its own worktree.
phase 4 (integration, DETERMINISTIC): because file ownership is disjoint, the
        integration is a per-file copy from each worktree into the run branch.
        No model regenerates anything.
phase 5: run the gate on the integrated tree.
        red → up to 2 rounds of `pair` on the integrated tree to fix seams.
```

Phase 2 and phase 4 are the whole point. A weak model asked to merge whole files
destroys working code; disjoint file ownership makes the merge a `copy`. If a
task cannot be decomposed with disjoint file ownership, `split` is the wrong
mode and Ducklab says so rather than degrading into model-driven merging.

---

## 5. Verification — the gate

### 5.1 Tiers

| Tier | Meaning | Verdict when it passes |
|------|---------|------------------------|
| `tests` | a real test suite ran and was green | `PASSED` |
| `build` | it compiles / type-checks | `PASSED` |
| `lint` | a linter was clean | `PASSED` |
| `none` | nothing executable exists | `UNVERIFIED` |

Detection order and commands are in `02-DATA-MODEL.md §3.1`. The tier in force
is printed before every run and stored in the run record.

### 5.2 Honesty rules (P3)

- A `none` gate can **never** produce `PASSED`. Documentation changes,
  configuration, a brand-new repo: those runs end `UNVERIFIED` and still reach
  the human gate with a diff. They are counted separately in every report.
- The gate command's exit code is the only signal. Parsing test output to decide
  "it looks fine" is forbidden.
- If the gate command itself fails to start (binary missing), the verdict is
  `UNVERIFIED` with the reason recorded — not `FAILED`, because nothing was
  actually tested.
- A gate that was already red **before** the run starts is detected in step 0
  and reported: `"note: the gate was already red before this run (N failures);
  the run will be judged on whether it makes it greener, and the pre-existing
  failures are listed"`. The verdict compares against the baseline failure set,
  never against zero.

### 5.3 Test-tampering guard

After a run, Ducklab compares the diff against the test files (any path matching
the project's test globs: `*_test.go`, `test_*.py`, `*.test.ts`, `tests/**`). If
the diff modifies tests **and** the task did not mention tests, the run is
flagged `tests_modified` and the human gate shows those hunks first, separately,
with the message: `"this change edits tests; read these hunks before accepting"`.
Ducklab does not block it — sometimes tests must change — but it never hides it.

---

### 5.4 Acceptance reproduces the gate from a clean checkout

An accepted commit is proven from a **detached worktree at that commit**, not
from the working tree — the tree can hide what the commit lacks (an
unanchored `.gitignore` once swallowed a whole package). Stage-aware
polarity: build and stage accepts must be green; a test-first accept must be
**red and structurally so** — not a compile failure — because the committed
red test is the deliverable, and a green one asserts nothing.

The checkout borrows the live tree's **installed dependencies** — `node_modules`
where the commit carries a `package.json`, `.venv` where it carries a Python
marker (`pyproject.toml`, `requirements.txt`, `setup.py`, `setup.cfg`,
`pytest.ini`, `tox.ini`, `Pipfile`) — the same custody rule the gate's
environment scrub follows: isolate engine state, never the tools of the trade.
Build products are never borrowed. This table is the zero-config default; a
declared `[verify] link_deps` / `setup` (B-061) is the general form.

An auto-accept whose reproduction fails **pauses at the human gate wearing the
error** — never a run stranded as running.

## 6. Bugs and the operate loop

```
report ──▶ bug (open) ──▶ triage ──▶ {duplicate | wontfix | triaged}
                                          │
                                     promote to task
                                          │
                                       build run
                                          │
                                    accept → bug: fixed
                                          │
                             verify (re-run gate) → bug: verified → closed
```

Bug intake sources:
- `ducklab bug add` (human or script)
- `--body-file` for pasted stack traces
- GitHub issues via `gh issue list --json` (v0.4, `[github] enabled = true`)
- a failed run may auto-open a bug when `autonomy >= auto` and the failure is a
  gate regression rather than an incomplete task

Triage is one `triager` turn per bug (batched: up to 10 bugs per run, each its
own turn, so a bad answer on one does not poison the others). Duplicate
detection is proposed by the model but applied only after the human gate under
`manual`/`guarded`.

`bug promote` creates a task whose body embeds the bug's reproduction steps and
whose `traceability` edge is `bug → task`. When the task's run is accepted and
the gate is green, the bug moves to `fixed`; `ducklab bug verify <id>` re-runs
the gate on `main` and moves it to `verified`.

---

## 7. Skills

A skill is a directory. Minimum contents:

```
<skill-name>/
  SKILL.md          # required
  run.sh / run.ps1  # optional executable entry point
  <anything else>
```

`SKILL.md` frontmatter:

```yaml
---
name: pdf-extract
description: Extract text and tables from a PDF into markdown. Use when a task
  references a PDF the model cannot read directly.
version: 1
args:
  - name: path
    type: string
    required: true
  - name: pages
    type: string
    required: false
entry: run.sh          # omit for a documentation-only skill
timeout_s: 120
---
```

Rules:

- `description` is what a duckling sees in `skill_list`. It must state **when**
  to use the skill, not only what it does. Skills whose description is a bare
  noun phrase are rejected by `ducklab skill validate`.
- Resolution order: project `.ducklab/skills/` shadows global
  `~/.local/share/ducklab/skills/` on name collision.
- `skill_run` executes `entry` through the same shell policy as the `shell`
  tool, with args passed as `DUCKLAB_ARG_<NAME>` environment variables and
  positional `--name=value` arguments, cwd = project root.
- A documentation-only skill (no `entry`) is read with `skill_read` and its body
  is injected into the model's context. This is the cheap and safe form and
  should be the default.

### 7.1 Ducklings authoring skills

A duckling may propose a skill by writing to `.ducklab/skills/<name>/` with the
normal `fs_write` tool. That directory is **not** privileged: the write guard
applies, `ducklab skill validate` runs automatically on the run's diff, and the
skill only becomes usable after the human accepts the run. A duckling can
therefore extend the harness, but only through the same gate as any other change
— which is the whole reason skills are files on disk and not a runtime registry.

---

## 8. MCP

Configured servers (`02 §2`) are started lazily on first `mcp_call`, over stdio,
and reused for the process lifetime. `ducklab mcp tools <server>` lists them.

- MCP tools are **not** merged into the flat toolbelt namespace; they are reached
  through the single `mcp_call` tool, so a server cannot shadow `fs_write`.
- A server that fails to start is reported once per run and its tools are
  unavailable; the run continues.
- MCP tool results are subject to the same size cap and the same logging as
  native tools.
- Servers are subject to the autonomy level: under `manual`, each `mcp_call` is
  confirmed.

---

## 8b. The operator surface (ducklab as an MCP server)

§8 is ducklings calling OUT to MCP servers as tools. This is the opposite
direction: `ducklab mcp serve` exposes ducklab itself over stdio, so an
external model can take the user's seat — read each stage and task result,
decide gates, answer questions, start work.

It is the third client, beside the CLI and the desktop, and it changes no
invariant:

- **The next-actions contract is the law.** `decide` reads the run's `next`
  from the engine and refuses any action not offered — an operator can never
  take an action a person could not.
- **I2 survives delegation.** Verdicts stay gates. What the operator takes
  over is the HUMAN gate, which is precisely what the person connecting an
  operator chooses to delegate.
- **Attribution.** Every decision is recorded with the operator's name:
  `approved_by: mcp:<client>`, resolution `accepted by mcp:<client>`, and a
  required free-text reason. The record must never say a human decided what a
  model decided.
- **Out of scope, deliberately:** configuring providers, ducklings or budgets
  — those stay human-owned — and anything returning a secret (I10 already
  guarantees there is nothing to return).

Tools: `status`, `run_get`, `decide`, `answer`, `artifact_get`, `task_list`,
`run_start`, `stage_start` (including `adopt`), `bug_report`.

---

## 9. Release and deploy

### 9.1 Release

`ducklab release plan --bump minor`:

1. Collect accepted runs since the last release tag (deterministic, from the DB).
2. Group by milestone.
3. One `scribe` turn renders user-facing notes (§6.7 of `04`).
4. Write `docs/releases/<version>.md` as a proposal; human gate.
5. `ducklab release cut <version>` writes the release row, tags the commit
   (`vX.Y.Z`), and — if configured — runs the deploy recipe.

### 9.2 Deploy recipes

Deploy is deliberately **not** cloud-aware. It is an ordered list of shell steps
with gates, defined in `project.toml`:

```toml
[deploy.staging]
description = "build and push to the staging box"
confirm     = true            # ask before starting
steps = [
  { name = "build",  run = "make build",              expect_exit = 0 },
  { name = "test",   run = "go test ./...",           expect_exit = 0 },
  { name = "push",   run = "./scripts/deploy-staging.sh", timeout_s = 900 },
  { name = "smoke",  run = "./scripts/smoke.sh",      expect_exit = 0, on_fail = "rollback" },
]
rollback = "./scripts/rollback-staging.sh"
```

Rules: steps run sequentially; the first non-matching exit code stops the recipe
and runs `rollback` if the failing step declares `on_fail = "rollback"`; the
whole recipe is logged as a run with stage `release`; **no model is involved in
a deploy** unless the human explicitly asks for a `review` of the recipe's
output. Deploy is where a hallucination is most expensive, so it is the one
place Ducklab is purely a script runner.
