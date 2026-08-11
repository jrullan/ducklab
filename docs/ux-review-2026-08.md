# UX review — August 2026

*A fresh pass over the desktop after a month of dogfooding on a real project
(exercise-tracker: 80+ tasks, 25 bugs, chats, TDD chains, budget lifts).
Numbered UX-n, worst first within each tier. Evidence cites what actually
happened, not what might. Complements docs/ux-evaluation.md (P/F/I), which
this review does not renumber.*

## The frame

The app now has two ambient surfaces and one focused one:

- **The guide rail** (left): the pulse — running work, the engine's next
  steps, the ask-why chat. Survives every view change.
- **Now** (first view): the inbox — cards with buttons: decide, verify,
  relaunch, launch next.
- **Everything else**: focused work on one object (a run, a board, the
  documents, settings).

Most current friction is the boundary between the first two drifting. The
contract this review proposes: **the rail glances, the inbox acts.** If it
has a button, it lives in Now; if you only need to see it, it lives in the
rail; nothing lives in both at full size.

## Tier A — worth doing soon

**UX-1 · Rail/inbox drift.** The running section moved twice in two days
(Now-bottom → Now-top → rail-top) because the two surfaces had no contract.
Adopted above; enforced by habit and pins. Remaining application of it:
Now's "running" section is now the *fuller* view (live spend, limits) and
the rail's is the glance — correct under the contract, but review any future
addition against it before placing it.

**UX-2 · A dead engine wears broken views.** Twice this month a desktop
window outlived an engine restart and every view degraded into Load Errors
with no explanation — the person had to know the ritual (relaunch desktop).
The app knows its connection state (the footer chip says "closed"). When the
stream is closed AND requests 401/refuse, the honest UI is one full-screen
card: "the engine restarted — relaunch the desktop (your token expired with
it)", not fifteen broken panels. Highest-value fix on this list.

**UX-3 · Nothing says "you are needed" from other views.** ~~Resolved before
this review noticed~~: the Now nav item already carries the waiting count
(`nav-badge`), and the window title mirrors it. Review error, kept for
honesty.

**UX-4 · RunView is one very long page for five different kinds of run.**
Build, test, chat, stage, triage all render the same stack: lanes, gate,
budget, tabs, timeline. The chat's own layout (composer pinned, lanes as
conversation) proved per-kind layouts pay. *Done for stage runs (Aug 11):
the proposed document renders where the decision happens, and non-code runs
offer only the calls tab.* Remaining candidates: triage already shows its
proposals; build/test are the native shape.

## Tier B — real, not urgent

**UX-5 · The exits from a state are engine-known but unevenly rendered.**
run.next / bug.Next / NextStep all state the legal actions, and most views
render from them — but the month's confusions ("cómo termino el chat?",
"T-019 quedó en Blocked sin botones", the aborted-stage dead end) were each
a state whose exits existed and were not all on screen. A periodic pin-style
audit: for every pending/terminal state, every action in `next` has a
visible control where the state is shown.

**UX-6 · Board rail selection vs deep links.** Clicking a task selects it in
the rail (good, now sticky) but the selection is not a route — you cannot
link or reopen "board with T-051 selected". Minor until pop-outs or "back"
matter.

**UX-7 · Header load.** Nav, app control (chip + Launch/Stop + details),
project selector. It works at current width; one more control and it will
not. Rule of thumb going forward: the header is for global state only
(project, app, connection); new controls go in views or the rail.

**UX-8 · Vocabulary is now taught in three places.** Outcome-first wording
in the guide steps, the harness dossier in the ask-why chat, and the raw
terms everywhere else (intake, gate, triage). Consistent rule going
forward: UI labels lead with the outcome, parenthesize the term — the
guide's convention, applied wherever a term appears cold.

## Tier C — noted, cheap to ignore

- **UX-9** Reports view is still tables-first; the estimates-in-launcher
  pattern (cost where the decision happens) is the better direction for any
  new number.
- **UX-10** Settings has grown five sections; fine solo, will need grouping
  if the fleet grows past ~10 ducklings.
- **UX-11** No cross-project inbox; irrelevant while one project is active
  at a time, revisit if that changes.

## What this review deliberately does not propose

- A redesign. The bones — inbox-first, engine-stated actions, record-first
  views — matched real use well this month; the frictions were placements
  and gaps, not architecture.
- More ambient surfaces. Two is the budget: the rail glances, the inbox
  acts. A third would re-create the drift this review exists to stop.
