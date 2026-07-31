# The desktop's UX, evaluated against one person actually using it

2026-07-31. An evaluation of the desktop app and a proposal for reworking it,
written after a week of watching its one real user drive a real project
(`calculator`, 25 tasks, 2 bugs, ~40 runs) through it.

## 1. The evidence base

This is not a heuristic walkthrough. The past week produced ~25 usability
incidents from a real user doing real work, each one reported as friction and
each one traced to a cause. That corpus is better evidence than any review
against a checklist, and this document leans on it throughout.

A sample, with what each incident revealed:

| The user said | The defect underneath |
|---|---|
| "No encuentro ninguna forma de crear un proyecto" | No entry point; empty states pointed at the CLI |
| "Acepté el run de intake, pero Cycle dice que espera mi aceptación" | One decision, two surfaces, two states |
| "El textbox solo aparece en Cycle, no en el run" | The same gate hand-built twice, diverging |
| "Esos botones no debieran aparecer" | Controls not derived from state |
| "Necesito entender mejor el board" | Columns that could never be entered; vocabulary unexplained |
| "Cómo rayos quieres que lo mueva a mano?" | Advice pointing at functionality that did not exist |
| "En Settings el valor sigue en 6" | A Save button sitting in the middle of its own fields |
| "No estoy viendo lo que el modelo produce" | Streaming wired in one of six run kinds |
| "Me parece extraño que 24 turnos no le hayan dado" | A truncated read the model could not interpret; the cost invisible until too late |
| "Cómo se guarda ese valor???" | A silent no-op indistinguishable from success |

Two meta-facts matter more than any single row:

1. **Every incident was found by the user, not by a test or by review.** The
   engine-side guards added since (route coverage, event coverage) now catch
   two classes mechanically. The UI itself has no equivalent guard.
2. **The incidents are one class wearing twenty costumes**: state that exists
   in the engine and never reaches a surface, or a surface that guesses what
   the engine allows and guesses wrong.

## 2. What this product is, for one person

A solo developer using ducklab runs a loop:

```
  define work ──▶ launch a run ──▶ wait (minutes) ──▶ decide ──▶ repeat
                                      │
                                      └── do something else entirely
```

The structural fact of solo use is that **the human is the scarcest resource
in the system and the only one that cannot be parallelised**. Runs take
minutes and run unattended; the person leaves — to their editor, to another
task, to lunch. The product's real job during those minutes is nothing. Its
real job the moment a run pauses is to *get the person back and put the
decision in front of them whole*.

Measured against that job, the current app is inverted: it is excellent at
displaying resources and poor at routing attention. It answers "what is a
run?" everywhere and "what needs me right now?" almost nowhere.

## 3. Findings

### F1 — The architecture mirrors the engine, not the work

The nav has **ten top-level destinations**: Overview, Runs, Cycle, Board,
Review, Release, Reports, Ducklings, Projects, Settings. That is one tab per
engine resource, which is exactly how a client grows when each new endpoint
gets its own page.

But the user has *one* workflow, and it is smeared across three of those tabs:
documents live in Cycle, tasks and bugs in Board, execution in Runs. The
week's transcript shows the cost — the user repeatedly had to be told where a
thing lived ("the spec gate is in Cycle", "the task controls are in the Board
rail", "the triage proposal is in the run view"). A person should never need a
map of the client to use the client.

Board.tsx is now 1,041 lines. It did not get there by design; it got there by
accretion — tasks, bugs, launcher, gate state, bug filing, bug editing, task
removal, each added where the previous thing was. That is the file telling us
the architecture has no place for new capabilities, so they pile up wherever
the last one landed.

### F2 — Attention routing is manual

The engine models "waiting for a human" precisely (`pending_kind`, one of the
best ideas in the system). The client renders it as: a tile on Overview, a
count in the status bar, a chip on a run row — three passive indicators on
three different screens. Nothing *calls* the person.

Observed consequence: the user polled. Screenshot after screenshot in the
transcript is them switching views to find out whether anything had happened.
For a product whose runs take minutes and whose user is one person, this is
the single most expensive defect in the app: it taxes the only resource that
cannot be scaled.

The spec already names the fix (AC-31, OS notifications). It is unbuilt.

### F3 — Seven shapes for one decision

Every gate in the system asks the same question — *do you take this work?* —
and the client currently asks it with seven different hand-built surfaces:

1. RunView header Accept/Reject buttons
2. StageGate in RunView (accept / request changes / reject, note on top)
3. StageGate in Cycle (same component now, after it diverged once)
4. The Board rail's TaskRunner (build / test-first / review)
5. The relaunch panel (failed runs)
6. BugNext in the bug rail (triage / promote)
7. The triage proposals section

Three of the week's incidents were exactly this: surfaces disagreeing about
the same decision. Every new decision kind has meant a new hand-built panel,
and each panel is another chance to forget the evidence (the triage gate
shipped with Accept/Reject and *nothing to decide on* — the proposals were in
the event stream, unrendered).

### F4 — The client guesses what the engine allows

The worst incident class of the week — "vuelves a insinuar funcionalidad que
no existe" — has a single root: the client (and I, advising the user) reasoned
about what *should* be possible instead of being *told* what is.

The one place this is fixed is instructive: bugs now carry `next` — the legal
transitions, computed by the engine from its own rules — and the rail renders
one button per entry. A button the loop forbids cannot render; a state the UI
forgot cannot dead-end. Since that change, zero incidents in that area.

Runs and tasks have no equivalent. The client hardcodes "paused+gate means
Accept/Reject", "todo means Build it", "failed means relaunch" — every one of
those rules was wrong at least once this week.

### F5 — State that exists but never renders

The engine is a diligent recorder. The client, historically, was not a
diligent reader. Things that existed in full and reached no surface at some
point this week: why a run failed, what a run spent while running, the per-
model split, the run's actual budget ceiling, the model's thinking, streaming
for five of the six run kinds, a triage's proposals, a triage's failures, the
call log, the brief a stage was given, blocked reasons, `settled`,
`contestant_failed`, `split_result`…

Each was fixed *after the user hit the gap*. Two guards now hold the line
mechanically (`TestEveryEngineCapabilityIsReachableFromTheDesktop`,
`TestEveryEmittedEventIsKnownToTheDesktop`) — but "the client *can* call it"
and "the vocabulary *knows* it" are weaker claims than "a person can see it".
Three client methods exist today that no view calls (`ducklingProbe`,
`projectStatus`, `traceShow`). That gap class has no guard.

### F6 — The vocabulary is the engine's

The user — a capable engineer — had to ask, over the week: what Blocked means,
what Review means, how rounds relate to turns, how *those* relate to "model
calls per turn", what counts against the token budget, whether the budget is
per duckling or per run. Every answer existed only in code comments.

The recent pattern of explaining at point of use ("a round is one pass over
every participant…") measurably worked — questions stopped after each note
shipped. But the notes are patches on vocabulary that leaks from the engine:
two different things called "turns", a gate called "none", a verdict called
UNVERIFIED that reads as an error and is in fact the honest normal state of
every artifact stage.

### F7 — Silent no-ops burned trust

Three times an action reported success and did nothing observable: accepting
a triage (applied nothing), saving settings (the second section's values
looked unsaved), rejecting a run (left its edits in the tree). Each is fixed;
the lesson is a principle the UI never had: **an action's result must be
observable in the next thing the user looks at**, or the action did not
happen as far as trust is concerned.

### F8 — Cost is real money and was an afterthought

The Overview literally hardcoded `spentToday={0}` while runs spent $3.93 in
an afternoon. It is computed now, but a person paying per token still has no
answer to "what has this project cost me" or "what does a T-025 attempt cost
on average" without opening Reports and doing arithmetic.

## 4. Design principles

**P1 — One person, one queue.** The first screen is the list of things that
need the human, ordered by how long they have been waiting. Everything else
is secondary. When the queue is empty, the first screen says what is ready to
start — because for a solo dev, "nothing needs me" and "what should I do
next" are the same moment.

**P2 — The engine states, the client renders.** Legal actions travel with the
resource, the way `bug.next` already works. The client never encodes the
loop's rules; it draws buttons from lists. This is the structural end of both
"a button that errors" and "a state with no button".

**P3 — Every decision has one shape.** Claim, evidence, cost, verdict — one
component, everywhere the system asks for a human judgment. What changes per
kind is the evidence block, never the layout of the decision.

**P4 — Explain at the point of use; prefer presets to knobs.** The inline
notes stay and grow. Raw engine knobs (rounds, caps, budgets) live behind an
"advanced" fold; the primary controls are profiles a person can name.

**P5 — If the engine wrote it, one surface owns it — provably.** Extend the
guard chain to its last link: a test asserting every client method is
referenced by a view.

## 5. The proposal

### 5.1 Three destinations instead of ten

```
┌──────────────────────────────────────────────────────────────┐
│ 🦆 ducklab      Now ●2   Work   Records          [calculator ▾] ⚙ │
└──────────────────────────────────────────────────────────────┘
```

- **Now** — the inbox. Default screen, badge shows the count.
- **Work** — the project's substance: requirements / spec / plan as document
  tabs (absorbing Cycle), the task board, the bugs board. One rail pattern.
- **Records** — history and analysis: runs, reports, reviews, releases,
  bench. Read-only; nothing here ever needs a decision.
- **⚙** — Settings, Ducklings (fleet + providers), Projects. Configuration is
  not a destination a solo dev visits daily; it does not spend a tab.

Overview dies — Now replaces it and does the job better. Nothing else is
removed; things are *relocated*, and every existing component is reused.

### 5.2 Now: the inbox

```
┌────────────────────────────────────────────────────────────┐
│  Waiting for you                                           │
│                                                            │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ ✋ T-026 · pair · passed · waiting 4m        $0.31    │  │
│  │    "Recompute the edge label for the dragged vertex" │  │
│  │    gate ✓ green · diff +22 −4 · reviewer approved    │  │
│  │    [ Accept ]  [ Request changes ]  [ Reject ]       │  │
│  └──────────────────────────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ ⚠ B-003 triage failed · could not classify           │  │
│  │    provider chat: rate limit (429)                   │  │
│  │    [ Retry triage ]                                  │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                            │
│  Running                                                   │
│  ◔ T-027 · solo · k3 · 2m · 214k/1.5M tokens              │
│                                                            │
│  Nothing else needs you.                                   │
│  Ready to start: T-028 — Angle input validation  [ Run ▸ ] │
└────────────────────────────────────────────────────────────┘
```

Contents, in order: decisions (gates, questions), failures worth acting on
(with their one next step), active runs (live spend, so cost is ambient
information rather than a report), and — when the queue is empty — the
engine's `tasks/next` answer with a one-click launcher.

Everything on this screen already exists in the engine. The screen is a query,
not a feature.

### 5.3 The decision card

One component renders every human gate. Its shape never varies:

```
┌ what was asked ──────────────────────────────────────────┐
│ T-026 — Recompute the edge label for the dragged vertex  │
│ from B-002 · pair · pato-sonnet + dsv4flash              │
├ what happened ───────────────────────────────────────────┤
│ gate ✓ sh scripts/check.sh · reviewer approved (0 findings)│
│ diff +22 −4 in index.html                    [view full] │
│ 412k tokens · $0.31 · 3m12s                              │
├ what accepting does ─────────────────────────────────────┤
│ commits to master, moves B-002 to fixed                  │
├──────────────────────────────────────────────────────────┤
│ [ Accept ]  [ Request changes… ]  [ Reject ]             │
└──────────────────────────────────────────────────────────┘
```

The "what accepting does" line is new and non-negotiable: three of the week's
incidents were the user discovering *after* the click what the click did (or
did not do). The verdict buttons come from the engine (5.4), the evidence
block varies by kind (stage → proposal diff; triage → proposals list; task →
code diff + gate), the frame never does.

Replaces all seven surfaces from F3. RunView keeps the transcript, timeline,
and calls tab — it becomes the *evidence detail* page the card links into,
not a decision surface of its own.

### 5.4 The next-actions contract

Generalize `bug.next` to runs and tasks:

```json
{ "id": "r-…", "status": "paused", "pending_kind": "gate",
  "next": ["accept", "request_changes", "reject"] }

{ "id": "T-028", "status": "todo",
  "next": ["run", "test_first", "remove"] }
```

Engine-side this is one function per type, reading rules that already exist
(`acceptRun`'s preconditions, the board's status derivation, `TaskRemove`'s
guards). Client-side, decision cards and rails render buttons *from the
list*. The week's whole "functionality that does not exist" class — moveBug
unreachable, remove refused after the click, Accept on a triage that applied
nothing — becomes structurally impossible: the engine cannot offer what it
will refuse, and the client cannot invent what it was not offered.

This is the highest-leverage single change in this document.

### 5.5 Attention

- **OS notification** when a run pauses for a human or fails (AC-31): title,
  verdict, one line. Click focuses the app on that decision card.
- **Badge** on the Now tab and in the window title (`ducklab ●2`), so the
  count survives the app being in another workspace.
- A run *completing without needing anything* does not notify. Silence is
  information; spending it on non-decisions teaches the person to ignore it.

### 5.6 Presets over knobs

The Settings page keeps every raw control under an "advanced" fold, but the
primary interface becomes three named profiles on the launcher:

- **Quick** — solo, default budget, script caps.
- **Standard** — pair, the mode's saved line-up.
- **Thorough** — tournament or pair with raised budget and turn caps.

A profile is nothing but a saved `RunRequest`; the engine needs no changes.
The launcher shows the estimated cost of the profile next to its name, from
the project's own run history — the number the user currently computes by
hand from Reports.

### 5.7 The last guard

A vitest that walks the client's method list and asserts each is referenced
by at least one view, with a `knownUnwired` list for the deliberate cases —
same shape as the engine's route guard, closing the same class one level up.
It would fail today on three methods, which is the point.

## 6. Phasing

Each phase ships alone and leaves the app better; none rewrites anything.

| Phase | What | Why first |
|---|---|---|
| 1 | Now inbox + OS notifications + title badge | Attacks F2, the most expensive defect; pure recomposition of existing data |
| 2 | next-actions contract (engine + client) + decision card | Ends the F3/F4 classes structurally; unblocks everything after |
| 3 | IA collapse to Now/Work/Records; Cycle folds into Work; Overview retired | Needs 1–2 in place so nothing loses a home |
| 4 | Presets, cost-ambient displays, vocabulary pass, reachability guard | Polish that sticks because the structure now has places for things |

Phase 1 is small: the inbox is a filter over `useRuns` plus `tasks/next`,
both already in the store. Phase 2 is the real work, most of it engine-side
and test-first. Phases 3–4 are mostly moves, not builds.

## 7. What this deliberately does not propose

- **No rewrite.** Every component survives; they are re-homed and re-fed.
- **No new engine capabilities** beyond `next` on two more types — the week
  proved the engine's data is already sufficient; the client just never
  looked.
- **No multi-user features.** The single-queue premise is load-bearing; if
  collaboration ever matters, this design is wrong and should be revisited
  rather than extended.
