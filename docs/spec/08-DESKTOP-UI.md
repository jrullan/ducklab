# 08 — Desktop Application

`spec-1.1` — new in this revision. `ducklab-desktop` is the primary face of
Ducklab.

The desktop app exists for one reason the CLI cannot serve: **watching several
models work, in real time, and stepping in at the right moment.** Every design
decision below serves observation and intervention. It is a client (I11): it
renders engine state and calls engine endpoints, and it holds no truth.

---

## 1. Application shell

### 1.1 Window

- Default 1440 × 900, minimum 1024 × 700, resizable, state persisted per machine
  in `<state-dir>/ducklab/window.json`.
- Native title bar on macOS and Windows; on Linux a client-side header bar.
- Single-window by default. A run may be **popped out** into a secondary window
  (`⌘⇧O` / `Ctrl+Shift+O`) so a long run can be watched beside an editor. A
  pop-out is a second view of the same SSE stream, never a second engine client
  session.
- System tray / menu-bar item showing: active run count, and a badge when any run
  is waiting for a human. Clicking it raises the Human-gate inbox.

### 1.2 Layout

```
┌───────────────────────────────────────────────────────────────────────┐
│  ● ducklab   [ MiEmpresa ▾ ]                        ⌘K   ◐   ⚙        │  header 48px
├──────────┬────────────────────────────────────────────────────────────┤
│ Overview │                                                            │
│ Cycle    │                                                            │
│ Board    │                     view area                              │
│ Runs   ②│                                                            │
│ Review   │                                                            │
│ Release  │                                                            │
│ Reports  │                                                            │
│ ────     │                                                            │
│ Ducklings│                                                            │
│ Skills   │                                                            │
├──────────┴────────────────────────────────────────────────────────────┤
│ ⬤ engine 0.2.0 · 2 runs · $0.31 today · ⏸ 1 waiting for you           │  status 28px
└───────────────────────────────────────────────────────────────────────┘
```

- **Sidebar** 200px, collapsible to 56px icons, state persisted. Badge counts are
  live from the event stream.
- **Header**: project switcher (recent projects + "Open folder…"), command
  palette trigger, theme toggle, settings.
- **Status bar**: engine health dot (`good` / `warning` reconnecting / `critical`
  unreachable), active run count, today's spend, and the waiting-for-human count
  as a button that opens the inbox. The engine dot is never hidden — when the
  stream drops, the UI keeps rendering the last known state and the dot turns
  `warning` with `Reconnecting…` (never blank the UI).

### 1.3 Navigation model

Nine top-level views: **Overview · Cycle · Board · Runs · Review · Release ·
Reports · Ducklings · Skills**, plus modal **Settings**. Views are routes
(`/overview`, `/runs/:id`, …) so a pop-out window opens a route directly.

---

## 2. Design system

### 2.1 Tokens

Defined once as CSS custom properties on `:root`, with a dark set under both
`@media (prefers-color-scheme: dark)` and `:root[data-theme="dark"]` so the
in-app toggle wins in both directions. Dark is a **selected** set of values, not
a programmatic inversion.

| Role | Light | Dark |
|---|---|---|
| `--page` (app plane) | `#f9f9f7` | `#0d0d0d` |
| `--surface-1` (panels, cards, chart surface) | `#fcfcfb` | `#1a1a19` |
| `--surface-2` (sunken: code, logs, diffs) | `#f0efec` | `#141413` |
| `--text-primary` | `#0b0b0b` | `#ffffff` |
| `--text-secondary` | `#52514e` | `#c3c2b7` |
| `--text-muted` | `#898781` | `#898781` |
| `--border` | `rgba(11,11,11,0.10)` | `rgba(255,255,255,0.10)` |
| `--gridline` | `#e1e0d9` | `#2c2c2a` |
| `--axis` | `#c3c2b7` | `#383835` |

**Status palette (fixed, never themed, never reused as a series color):**

| Role | Hex | Used for |
|---|---|---|
| `--status-good` | `#0ca30c` | gate green, `PASSED`, duckling reachable |
| `--status-warning` | `#fab219` | `UNVERIFIED`, reconnecting, budget ≥ 80 % |
| `--status-serious` | `#ec835a` | paused, waiting for human, queued too long |
| `--status-critical` | `#d03b3b` | `FAILED`, `BUDGET_EXCEEDED`, policy violation, engine down |

**Status color never carries meaning alone.** Every status is rendered as
`icon + label + color` — `✓ passed`, `⚠ unverified`, `⏸ waiting`, `✕ failed`.
This is non-negotiable: `warning` and `serious` are below 3:1 on the light
surface by design, and the icon+label pairing is the mitigation.

**Categorical series palette** (charts, duckling identity, conversation lanes) —
fixed order, assigned by slot, **never cycled**:

| Slot | Hue | Light | Dark |
|---|---|---|---|
| 1 | blue | `#2a78d6` | `#3987e5` |
| 2 | orange | `#eb6834` | `#d95926` |
| 3 | aqua | `#1baf7a` | `#199e70` |
| 4 | yellow | `#eda100` | `#c98500` |
| 5 | magenta | `#e87ba4` | `#d55181` |
| 6 | green | `#008300` | `#008300` |
| 7 | violet | `#4a3aa7` | `#9085e9` |
| 8 | red | `#e34948` | `#e66767` |

Rules that follow from this palette and must be obeyed:

- A duckling's color is assigned by its **stable index in the roster**, not by
  its position in the current chart. Filtering a report must never repaint the
  survivors.
- Slots are assigned in order 1→8. A 9th series is **never** a generated hue: it
  folds into "Other", or the view switches to small multiples.
- For **all-pairs** forms (scatter, bubble, small multiples) only the **first
  three slots** are permitted; past three, facet.
- Sequential encoding (heatmaps, magnitude) uses the **blue ramp only**,
  light→dark: `#cde2fb #b7d3f6 #9ec5f4 #86b6ef #6da7ec #5598e7 #3987e5 #2a78d6
  #256abf #1c5cab #184f95 #104281 #0d366b`. On light, an ordinal ramp starts no
  lighter than `#86b6ef`; on dark, no darker than `#184f95`.
- Diverging encoding (only place it appears: "better/worse than the solo
  baseline") is **blue ↔ red** with a gray midpoint (`#f0efec` light,
  `#383835` dark). Equal steps per arm.
- Before shipping any change to these values, run the validator from the dataviz
  skill against both surfaces and fix every FAIL.

### 2.2 Type and spacing

- Face: `system-ui, -apple-system, "Segoe UI", sans-serif` everywhere, including
  large numbers. No display or serif face.
- Mono (code, diffs, logs, model output):
  `ui-monospace, "JetBrains Mono", "Cascadia Code", Menlo, Consolas, monospace`.
- Scale: 11 / 12 / 13 / 15 / 20 / 32 / 48 px. Body is 13. Hero stat is 32; the
  single headline number on Reports is 48.
- `font-variant-numeric: tabular-nums` **only** in aligned columns (table rows,
  axis ticks, the token/cost meters). Standalone hero numbers use proportional
  figures.
- Spacing scale: 4 / 8 / 12 / 16 / 24 / 32 / 48. Radius 6 (controls), 10 (cards).
- One hairline border, never a shadow, for separation. Shadows only on true
  overlays (command palette, popovers, toasts).

### 2.3 The duck

The rubber duck is the product's character and appears in exactly three places —
nowhere else, so it stays charming rather than twee:

1. The app icon and the empty-state illustration.
2. A small duck glyph as each duckling's avatar in conversation lanes, tinted
   with that duckling's categorical slot color.
3. The idle/thinking indicator: a duck that bobs while a turn is in flight.

Ducklings are addressed by their configured id (`pato-local`), never by a
generated nickname.

---

## 3. Cross-cutting elements

### 3.1 Command palette (`⌘K` / `Ctrl+K`)

Fuzzy search over: every CLI verb from `03-CLI.md` (same grammar, same names),
every task and bug by id or title, every run id, every view, every duckling.
Executing a command shows the equivalent CLI invocation in the result row — the
palette is also how a user learns the CLI.

### 3.2 The Human-gate inbox

The single most important UI in the application. Anything blocked on a person
appears here:

- a run at its accept/reject gate,
- an `ask_human` question from a duckling mid-turn,
- a `manual`-autonomy tool-call approval,
- a shell command outside the allowlist requesting one-shot approval,
- a stage proposal awaiting promotion.

Each entry shows: project, run, what is being asked, how long it has waited, and
the actions inline. Opening an entry deep-links to the exact place in the Run
view. A run waiting on a human is **not** an error state — it is
`--status-serious` (`⏸ waiting for you`) and it waits indefinitely by design
(`01-ARCHITECTURE.md §7.1`).

### 3.3 Notifications

OS-level notifications, via `xplat`, for: run finished (with verdict), human
input needed, budget threshold crossed, engine lost. Each is configurable in
Settings and each is off by default except *human input needed* and *engine
lost*. Clicking a notification raises the window and routes to the source.

### 3.4 Toasts

Bottom-right, max 3 stacked, auto-dismiss 5 s except errors (manual dismiss).
Toasts are for outcomes of user actions only. Run progress goes to the Run view,
never to toasts.

---

## 4. The views

### 4.1 Overview — the cockpit

The landing view for an open project. Four bands, top to bottom:

1. **Cycle strip** — the seven stages as a horizontal progress rail
   (`intake → spec → plan → build → review → release`, with `operate` looping
   back). Each stage shows a state chip: `not started` / `N of M` / `✓ complete`
   / `⏸ awaiting approval`. Clicking a stage routes to it.
2. **Stat tiles** — four, in one row. Each is a value + label + a delta line; no
   plot inside a tile.
   - *Tasks done* — `12 / 31` with a thin progress track.
   - *Gate* — `tests · go test ./...` with a `✓ green` / `✕ red` status chip.
   - *Spend today* — `$0.31` with `budget $5.00` beneath and a track that turns
     `warning` at 80 %.
   - *Open bugs* — `4` with `1 critical` beneath.
3. **Active runs** — one card per running or paused run: mode, task, ducklings
   with their slot colors, elapsed, live token counter, current turn, and a
   sparkline of tokens/second. Cards are clickable into the Run view.
4. **Recent activity** — the last 20 events across the project, plain rows.

Empty project (nothing initialised): a single duck illustration and one primary
action, *Start with a brief* → routes to Cycle/Intake.

A project whose tree already holds committed code (`has_code` on the project
record) gets **two doors** in the Cycle empty state, each with its meaning:
*Start from a brief* (the product is an idea) and *Adopt this codebase*
(survey the tree into the requirements the code already satisfies — `05 §4.4
adoption`). The second door is what makes an existing repo's first screen
answer "what can I do here" instead of going mute.

### 4.2 Documents — intent, requirements, spec, plan

*As built (2026-08-29, #7 and #8). The original three-tab sketch is kept below
this block for the parts that still hold.*

Documents is one workspace with four stages in reading order — **Intent →
Requirements → Specification → Plan** — shown as a numbered stage strip, a
health line (`✓ 0 breaks in the spine` / `⚠ N breaks`, coverage, a `Review
issues` door to the ledger) and a three-column body:

- **Outline** (left): the stage's sections as a searchable list with state
  filters (all / breaks / no task), each row marking `break`, `no task yet`,
  `proposal pending`. Selecting a row focuses it.
- **Reading pane** (middle): **empty until a section is selected** — by
  design, the pane shows *one* section in full (complete body, no disclosure)
  rather than the whole document; `Clear selection` returns to the empty
  state. The selection is addressable: `#/cycle/<stage>/<SECTION-ID>` (the old
  `?section=` form still parses), so run-origin breadcrumbs, task cards and
  chats deep-link into the exact section.
- **Section inspector** (right, at widths ≥ 1280 px): the selected section's
  id, title and state; its **document chain** (intent ↔ requirement ↔ spec ↔
  milestone ↔ tasks, from `trace/{id}` walked two levels, each node clickable
  and carrying live task status); `Propose change to <id>` (pre-fills the
  operation drawer with a focused brief, or a plan amendment on the plan tab);
  `Review traceability issue` when the spine reports one; and `Chat about
  <id>` — a consultant chat whose dossier is the approved section plus its
  chain (`about_kind: document`). A chat run's header carries `← Back to
  <id>`.
- **Operation drawer**: the stage's one primary action (`+ Add intention` /
  `Propose specification update` / `Extend plan`, named from the stage state)
  opens a right drawer with the roster, mode and brief; it says outright that
  it creates a proposal and the approved document stays unchanged until
  accepted. Spec and Plan refuse impossible launches and name the missing
  prerequisite instead (accept the requirements proposal first, draft the
  specification first); the engine enforces the same rule before a run record
  exists.

**Intent** (#8) is the human input boundary: every Intake brief (pasted, from
a file, or the answers of an interview) is preserved verbatim as an
immutable, addressable `INT-nnn` entry in `.ducklab/docs/intent.md` before a
model sees it — never the references or digests appended to the prompt.
When the requirements proposal lands, only the sections it added or textually
changed acquire `Originates from: INT-nnn`; on accept the entry records
`Outcome: accepted` and `Requirements: REQ-…`; reject and supersede record
theirs. Historical intake runs are imported lazily (marked as imported, no
invented edges; adoption runs excluded). Empty projects land on Intent;
established ones on Requirements. `trace check` reports an accepted intention
that changed no requirement (`unrealized_intent`). CLI: `ducklab intent`;
MCP: `artifact_get kind=intent`.

**Journey rail.** Bug and task rails, and a run after accept, show the
record's ladder with the current rung and name the next door (`GET
/v1/projects/{id}/next/{ref}`): the launcher's label *is* the door.

The original sketch, for what still applies:

- **Left**: the artifact rendered as a document, sections addressable by id
  (`REQ-003`). Inline edit via a Monaco pane toggled with `E` — this is the one
  place Monaco is loaded.
- **Right**: a traceability rail. For a requirement: which spec sections
  implement it, which tasks, which runs, which release. Broken links render with
  `⚠ orphan` and route to `trace check`.
- **Bottom bar**: `Run intake ▸` / `Run spec ▸` / `Run plan ▸` with the mode
  selector (default `council`), the roster for that stage, and a budget estimate.

When a stage run produces a **proposal**, the view switches to a **side-by-side
document diff** (current ← → proposed) with `Accept` / `Reject` / `Edit then
accept`. Nothing is written to the artifact until Accept (`05 §1.1` step 8).

The `council` conversation appears live in a collapsible drawer at the right —
the user watches the architect draft and the reviewer object while it happens,
and a `human` turn surfaces as an inline input in that drawer.

### 4.3 Board — tasks and bugs

Two toggleable boards sharing one layout.

- **Tasks**: columns `todo · in progress · blocked · review · accepted`. Cards
  show id, title, complexity, the spec section it implements, and the assigned
  duckling's color chip. Drag between columns issues `PATCH /tasks/{id}`.
- **Bugs**: columns `open · triaged · in progress · fixed · verified`. Severity
  is a status chip (`critical` / `high` → `serious` / `normal` → muted).
- Filters in **one row above the board**: milestone, status, severity, duckling,
  free-text.
- Right rail on selection: full record, traceability, run history, and the
  primary actions — for a task `Run ▸` (with mode selector), for a bug
  `Triage ▸` and `Promote to task ▸`.
- Batch select for `Triage` on bugs; the engine batches to 10 per run
  (`05 §6`).

### 4.4 Run — the monitoring view

The reason the desktop app exists. Route `/runs/:id`. Four regions:

```
┌───────────────────────────────────────────────────────────────┐
│ T-003 Implement session tokens · pair · round 2/3      ⏱ 4m12s│  header
│ pato-local ● implementer   pato-nube ● reviewer   $0.014      │
├──────────────────────────┬────────────────────────────────────┤
│                          │  ┌ Gate ──────────────────────┐    │
│   CONVERSATION LANES     │  │ tests · go test ./...      │    │
│   (live, streaming)      │  │ ✕ red · 2 failing          │    │
│                          │  └────────────────────────────┘    │
│                          │  ┌ Budget ────────────────────┐    │
│                          │  │ tokens ▓▓▓▓▓░░░ 184k/400k  │    │
│                          │  │ cost   ▓░░░░░░░ $0.014/$2  │    │
│                          │  │ turns  ▓▓▓░░░░░ 9/24       │    │
│                          │  └────────────────────────────┘    │
├──────────────────────────┴────────────────────────────────────┤
│   TOOL TIMELINE   ▮fs_read ▮fs_read ▮fs_patch ▮verify_run     │
├───────────────────────────────────────────────────────────────┤
│   DIFF  |  VERIFY OUTPUT  |  TRANSCRIPT  |  LLM CALLS         │
└───────────────────────────────────────────────────────────────┘
```

**Conversation lanes.** One vertical lane per participant, each tinted with that
duckling's slot color; the human has a neutral lane. Messages stream token by
token (`token_delta`). A turn in flight shows the bobbing duck. Rules:

- Tool calls render as **one collapsed line** — `fs_patch auth.go · 2 edits ·
  12ms · ok` — expandable to arguments and result. A run that made 40 `fs_read`
  calls must still be skimmable.
- A tool **error** result is `--status-critical` and expanded by default.
- A `policy_violation` event is always expanded, with the rule that fired.
- Anonymised turns (judge, reviewer under `tournament`) render as `A` / `B` with
  a lock glyph and a tooltip: *"identities hidden — this reviewer must not know
  who wrote which candidate"*. **The UI must not reveal the mapping**, even
  though it could: I7 is a property of the product, not only of the prompt.
- The implementer's **deliverables report** renders at the end of its own turn
  as a checklist — `n/m done`, one row per item with its mark (`☑` done, `◐`
  partial, `☐` not done / not reported, `⊘` blocked — never colour alone) and
  the implementer's note beneath. Deliberately **not** a rail card: it is an
  unreviewed progress report, subject to the run's later rounds; beside the
  gate it would read as the result. Each duck-sent retry is its own turn with
  its own checklist, so progress within a run reads in sequence.
- The advisor's consult is its own turn, **after** the implementer's closes and
  before the reviewer's — never nested inside the implementer's turn (which
  once made the two read as parallel).
- A reviewer verdict that approved over items the implementer reported
  undelivered wears the flag on the verdict itself: `⚠ approved over
  deliverables the implementer reported undelivered: 4`.
- `ask_human` renders as an inline input in the lane. Answering posts to
  `/answer`; the lane resumes in place.

**Gate card.** The verify tier, the command, the result, and — when the gate was
already red before the run — the baseline failure set, shown separately
(`05 §5.2`). A `none` gate shows `⚠ unverified · nothing executable to run` and
never anything that reads as success.

**Budget meters.** Three horizontal tracks (tokens, cost, turns), each turning
`warning` at 80 % and `critical` at 100 %. Values in tabular figures.

**Tool timeline.** A compact horizontal strip of every tool call in order, one
tick per call, colored by tool family, hover for detail. This is how a user spots
"it read the same file nine times" at a glance.

**Bottom tabs.**
- *Diff* — side-by-side via `diff2html`, file list on the left, per-hunk. If the
  run is flagged `tests_modified`, those hunks are pinned to the top under a
  `⚠ this change edits tests` banner (`05 §5.3`).
- *Verify output* — the raw gate log, monospace, tail-following.
- *Transcript* — the rendered markdown.
- *LLM calls* — a table: seq, duckling, role, tokens in/out, latency, cost,
  finish reason. Row expands to the redacted request/response.

**Tournament and split** add a *Candidates* tab: anonymised A/B/… cards, each
with its own gate result and diff, side by side, plus the judge's choice and
reason once made. Applied candidates are marked `applied verbatim` — surfacing
I8 to the user.

**Actions**, always in the header: `Accept`, `Reject`, `Abort`, `Pop out`. Accept
is disabled with a tooltip when the verdict is `FAILED`, and carries an explicit
confirmation when the verdict is `UNVERIFIED` — the user must acknowledge that
nothing was executed.

### 4.5 Review

The diff on the left, the reviewer's findings on the right as a list grouped by
severity, each anchored to `file:line` and clicking to that hunk. The gate result
is shown at the top of the findings list with the note that a red gate makes
`approve` unavailable (I2). Findings can be converted to bugs or to tasks in one
click.

### 4.6 Release

- **Changelog**: accepted work since the last tag, grouped by milestone, with the
  scribe's rendered notes and an editor. `No user-visible changes.` renders as an
  explicit empty state, not as a blank.
- **Deploy recipes** from `project.toml`, each as a card with its ordered steps.
  Running a recipe renders a **live step list**: each step pending → running
  (with streaming output) → `✓` / `✕`, and a rollback step highlighted if it
  fires. Because no model is involved in a deploy (`05 §9.2`), this view has no
  conversation lane — it is a process monitor, and it should look like one.

### 4.7 Reports — where the thesis gets measured

The headline is a **hero number**, not a chart:

```
        +14.6 pts
   pair vs solo baseline           n = 19 runs · last 30 days
```

Below it:

1. **Outcome mix by mode** — horizontal stacked bar per mode, segments
   `passed` / `unverified` / `failed` using the **status** palette with icon+label
   in the legend, 2px surface gap between segments. Not the categorical palette:
   these are states, not series.
2. **Pass rate by mode** — horizontal bar, one categorical slot per mode, sorted
   descending, with the solo baseline drawn as a labelled reference line. Direct
   value labels on each bar; no legend needed (single series).
3. **Cost and tokens over time** — line chart, one line per duckling (≤ 8, then
   "Other"), legend always present, ≤ 4 lines also directly labelled at their
   right end. **One y-axis.** Cost and tokens are two separate charts — never a
   dual axis.
4. **Per-duckling table** — runs, pass rate, avg tokens, avg latency, avg cost,
   with tabular figures. Estimated token counts are prefixed `~` and are never
   summed into a measured total (`04 §7`).

Filters in one row above the charts: date range (preset rows: today, 7/30/90
days, month-to-date), mode, duckling, task. Every chart has a `Table` toggle that
renders the same data as an accessible table.

A `Bench` tab runs and displays `ducklab bench` results with the same forms.

### 4.8 Ducklings

Card grid, one per duckling, each showing: name, provider, model, a reachability
status chip, context window, tool dialect (`native` / `text protocol`), cost per
Mtok, and the roles it is eligible for. Actions per card: `Probe`, `Test`,
`Edit`, `Remove`.

`Test` opens a small chat panel that streams a single round-trip and reports
tokens, latency and cost — the fastest way to answer "is this model alive and
sane". A **Roster** panel at the top assigns roles to ducklings by drag, with the
decorrelation warning from `05 §3.2` shown inline when the same duckling lands on
both sides of a `pair`.

### 4.9 Skills

List with scope badges (`project` shadows `global`), the description each
duckling sees, argument schema, and a validate action. A skill authored by a
duckling during a run appears here marked `pending acceptance` and is greyed
until its run is accepted (`05 §7.1`).

### 4.10 Settings (modal)

Tabs: **General** (autonomy, theme, notifications), **Providers**,
**Ducklings**, **Shell policy** (mode, allowlist, denylist, timeout, network),
**Budgets**, **MCP servers**, **Engine** (port, autostart, concurrency, restart,
view log), **About**.

Secrets are never displayed. An API key field shows `[set]` or `[unset]` and the
name of the environment variable it reads (`07-ENGINE-API.md §4.9`); the app
offers to copy the `export` line, never the value.

---

## 5. Real-time behaviour

- One SSE connection per window to `/v1/events`, scoped to the open project, plus
  an unscoped subscription for engine-level events.
- On mount of a run view: `GET /v1/runs/{id}` for the snapshot, then subscribe
  with `from_seq` = the snapshot's last seq. Backlog then live, no gap, no
  duplicate (`07 §6`).
- `token_delta` updates are batched with `requestAnimationFrame`; never one React
  render per token.
- Conversation lanes and log panes are **virtualised** (`@tanstack/react-virtual`).
  A run with 5000 events must scroll at 60fps.
- On `overflow` from the server, the client refetches the snapshot and
  resubscribes; it shows a `Resynced` chip, not an error.
- Optimistic UI is **forbidden** for anything the engine owns: accepting a run
  shows a pending state until the engine confirms the commit. The UI never
  displays a commit that may not exist.

## 6. Keyboard

| Key | Action |
|---|---|
| `⌘K` / `Ctrl+K` | Command palette |
| `⌘1…9` | Jump to view |
| `⌘⇧O` | Pop out the current run |
| `G` then `O/C/B/R` | Go to Overview / Cycle / Board / Runs |
| `A` / `R` | Accept / Reject the focused run or proposal |
| `X` | Abort the focused run |
| `E` | Toggle the artifact editor |
| `/` | Focus the filter of the current view |
| `?` | Shortcut sheet |
| `Esc` | Close overlay / clear selection |

Every action reachable by keyboard is also reachable by pointer, and vice versa.

## 7. States

Every view specifies all four:

- **Loading** — skeleton blocks matching the final layout. No spinners over 400 ms
  of content that is already known.
- **Empty** — a sentence saying what would fill it and one primary action.
  Example, Runs: *"No runs yet. Pick a task on the Board and press Run."*
- **Error** — the engine's one-line message, the failing endpoint, and a Retry.
  Never a raw stack; `--debug` adds detail to a copyable block.
- **Disconnected** — last known state, dimmed by 40 %, with the `Reconnecting…`
  chip. Actions that require the engine are disabled with a tooltip.

## 8. Accessibility

- All interactive elements are real focusable controls with visible focus rings
  (2px, `--status-good` at 3:1 minimum against both surfaces).
- Status is never color-alone anywhere in the app (§2.1).
- Charts: ≥ 2 series always carry a legend; ≤ 4 also get direct labels; every
  chart has a table view.
- Live regions: the human-gate inbox count and run verdict changes are announced.
- Respect `prefers-reduced-motion`: the bobbing duck becomes a static glyph, and
  streaming text appears without transitions.
- Respect `forced-colors`: charts switch to the texture channel (45° / 135°
  hand-drawn lines) instead of hue.
- Minimum target 32 × 32 px; text ≥ 11px only for tabular metadata.

## 9. Frontend layout and build

```
frontend/
  index.html
  vite.config.ts
  tailwind.config.ts          # tokens mirrored from §2.1, single source
  src/
    main.tsx
    app/         routes.tsx  layout.tsx  theme.ts  shortcuts.ts
    api/         generated.ts (from /v1/openapi.json — DO NOT EDIT)
                 client.ts    events.ts (SSE subscriber with backoff + Last-Event-ID)
    store/       session.ts project.ts runs.ts notifications.ts   (zustand)
    views/       Overview/ Cycle/ Board/ Run/ Review/ Release/ Reports/ Ducklings/ Skills/ Settings/
    components/  StatTile  StatusChip  DiffView  ConversationLane  ToolTimeline
                 BudgetMeter  GateCard  CandidateCard  TraceRail  CommandPalette
                 HumanGateInbox  DuckAvatar  ChartFrame  DataTable  EmptyState
    lib/         format.ts (money, tokens, duration)  colors.ts (slot assignment)
```

- `api/generated.ts` is produced by `make api` from the engine's OpenAPI document
  and is committed. Hand-editing it is a spec violation (`07 §7.3`).
- `lib/colors.ts` owns slot assignment and is the **only** place a series color is
  chosen. No component may hardcode a hue.
- Build: `wails3 build` runs `vite build` and embeds `dist/` into the Go binary.
  The frontend is never served from disk at runtime.
- Tests: Vitest for `lib/` and `store/`; Playwright against a **fake engine**
  (a small Go binary replaying a recorded event stream) for the Run view's
  streaming, reconnect, and human-gate behaviour. No test may require a live model.
