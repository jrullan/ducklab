# Operability plan — installer, MCP operator, brownfield adoption

Three gaps reported 2026-08-04, after a week of real use: the tool works, but
living with it still requires a terminal, an external model cannot drive it,
and it cannot start from a codebase that already exists. Analysis and plan for
each, with a recommended order at the end.

---

## 1. Installer — the engine should be nobody's job

### The pain

Every change means: terminal, kill the engine, re-export the API key, start
the engine, relaunch the desktop. The desktop already *detects* a stale engine
("older than this app. Restart the engine.") and then hands the person a
chore it could do itself.

### What it actually decomposes into

**1a. The desktop supervises the engine.** (AC-3 generalised — the spec only
promised CLI auto-start, and not even that is built.)

- On launch: if no healthy engine answers, spawn `ducklab-engine` as a child,
  inheriting the desktop's environment. `engine.json` + health check already
  exist; the desktop's picker already reads them.
- On version skew (the stale-engine 404 the client already classifies): show
  one button — "Restart the engine" — enabled only when the engine is one the
  desktop started or one whose binary path matches the installed pair. The
  running-runs check matters: refuse while runs are active (I9 makes restarts
  survivable, but a person should choose, not a version check).
- On quit: leave the engine running (runs outlive windows) unless the desktop
  started it this session and nothing is running.

**The key constraint is I10.** The engine reads provider keys from its own
environment at call time. Today that works because the person exports the key
in the shell that starts the engine. A desktop launched from an icon has no
such shell. Two options, in order:

  1. Ship with the current model: supervision works when the desktop is
     launched from a terminal (the dev loop — the actual pain reported), and
     the provider list already says when a key is missing.
  2. Follow-up: teach the engine to read `key-env` names from the OS keyring
     (libsecret / Keychain) as a fallback when the env var is absent. Never
     through the UI, never on disk — the invariant holds.

**1b. `make dev-install` — one command for the loop we live daily.** Build,
install, and — new — tell the running engine to restart itself... which the
engine must not do while runs are active, and which the desktop can then
reconnect to. Realistically: `make dev-install` + the desktop's restart button
is the whole loop, two clicks fewer than today.

**1c. Packaging.** A `.deb` + AppImage for Linux (bundling the AppArmor
profile 0003 already documents), Homebrew formula for the Mac once a Mac has
built it (see README §macOS — still unverified). Auto-update is out of scope;
an installer that can be re-run is enough at this stage.

### Spec impact

AC-3 (engine auto-start) extends to the desktop. 08-DESKTOP-UI gains the
supervision rules above. I10 unchanged — the keyring fallback is an
implementation of it, not an exception.

---

## 2. MCP — an external model operating as the user

### What was asked

A model outside ducklab (a local agent, Claude, anything) takes the user's
seat: reads each stage/run result, decides accept / reject / request-changes,
answers questions, starts the next piece of work.

### What the spec has today is the opposite direction

05 §8 specifies ducklings *calling out* to MCP servers as tools. This request
is ducklab *being* an MCP server. New surface, needs its own spec section.

### Why the architecture is already shaped for it

Everything hard about "a model operates the app" was solved for the desktop:

- **The next-actions contract.** The engine states the legal actions on every
  run, task and bug. An MCP operator never guesses what it may do — it reads
  `next`, exactly like the desktop renders buttons. This is the whole reason
  the contract exists; a second client is its first real test.
- **Clients are stateless (I11), the engine is loopback (I12).** An MCP server
  is just a third client beside the CLI and desktop, speaking stdio to the
  model and HTTP to the engine. No engine changes to start.
- **I2 survives delegation.** Verdicts stay gates; what the operator takes
  over is the HUMAN gate — which is precisely what the person connecting an
  operator is choosing to delegate.

### Design

`ducklab mcp serve` (stdio MCP server, in the CLI binary — it may import only
client packages, which is exactly what this is):

- **v1 — decide.** Tools: `status` (projects, pending decisions, the Now
  inbox in JSON), `run_get` (result + transcript summary + diff + verdict +
  `next`), `artifact_get` (proposal + trace), `decide` (run id, action from
  `next`, reason — required, like the DecisionCard's consequence), `answer`
  (pending questions). Every decision is recorded with the operator's name:
  `approved_by: "mcp:<client>"` — the field is already a string; the record
  should never say a human decided what a model decided.
- **v2 — drive.** `task_next` + `run_start`, `stage_start`, `bug_report`,
  `triage` — the full loop. Budget guards are already engine-side (I3), which
  is what makes handing the wheel to a model survivable.
- **Out of scope:** the operator configuring ducklab (providers, ducklings,
  budgets stay human-owned); anything that returns a secret (I10 already
  guarantees there is nothing to return).

### Spec impact

New section (05 §10 or its own document): the operator surface, the
attribution rule, and the boundary list above.

---

## 3. Brownfield — adopting a codebase that already exists

### The pain, precisely

`project init` on an existing repo (ducklab itself, say) succeeds and then the
product goes mute: Cycle wants a brief for intake as if the product were an
idea, the board is empty, and nothing says what to do with forty thousand
lines of existing code. The full-cycle flow assumes greenfield.

### Design: adopt = intake that reads the tree

The missing stage is a **survey**: requirements drafted *from the code as it
stands*, then spec, then a plan whose tasks are new work only.

- **Engine.** `intake --adopt` (and spec/plan after it, unchanged): the
  architect's turn gets the read-only half of its toolbelt aimed at the tree
  — it already can read files; what changes is the prompt: "survey this
  codebase; write the requirements it ALREADY satisfies; mark inferred intent
  with **Assumption:**; invent nothing aspirational." The council critique
  applies as-is — a critic checking the draft against the tree is the same
  critic that today checks it against a brief. Trace stays honest: sections
  born from a survey carry a marker (`origin: adopted`) so a reader knows
  these were derived, not decided.
- **Then the normal loop.** Once the as-built documents are approved, feature
  addition — the extension flow that already exists — IS the development
  model. The plan's first tasks come from the first brief, not from the
  survey: adopted code needs no tasks to build what is already built.
- **Desktop.** The evident path the user asked for: Cycle's empty state, when
  the project has code (cheap check: tracked files beyond `.ducklab`), offers
  both doors with their meanings: "Start from a brief (the product is an
  idea)" / "Adopt this codebase (the product already runs — draft the
  requirements it satisfies)". That one screen is most of the perceived gap.
- **Honesty limit, stated in the UI:** a survey of a large codebase is
  bounded by context and budget like every run. Requirements for ducklab
  itself will be a model's READING of the tree, gated by the same human
  approval as everything else. The trace-check (every must REQ covered by
  SPEC) keeps the derived documents internally consistent.

### Spec impact

05 §1 gains the adopt variant of intake; 02 records the `origin` marker;
08 the two-door empty state.

---

## Recommended order, and why

1. **Brownfield (3)** — it unlocks dogfooding ducklab in ducklab, which
   compounds: every hour of that use produces exactly the feedback this week
   produced, on the tool's own repo. Smallest of the three (a prompt path, a
   marker, one screen).
2. **Supervision + dev-install (1a, 1b)** — daily friction, well-bounded,
   and it makes the dogfooding loop from (3) pleasant. Packaging (1c) waits
   until a Mac has built the desktop once.
3. **MCP operator (2)** — the largest and the one that most benefits from the
   other two being done: an operator is only worth wiring when the loop it
   operates is smooth, and its first real assignment can be ducklab's own
   backlog from (3).

Each lands in the usual rhythm: engine + tests, desktop + tests, spec
amendment, one commit per coherent step.
