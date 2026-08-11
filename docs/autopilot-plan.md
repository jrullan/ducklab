# Autopilot (yolo mode) — plan

*Status: COMPLETE. Steps 1–3 shipped 2026-08-11 (dissent-aware auto-accept;
project autonomy honored + yolo checkbox; guide-driven autopilot with stop
rails and rail toggle — internal/service/autopilot.go). Step 4 shipped
2026-08-12: yolo questions pause and take the advisor's drafted answer
automatically (advice_taken on the record); triage auto-applies under
auto/yolo except duplicate proposals, which always wait for a person; the
driver runs triage itself when the project's autonomy allows it.*

## What already exists (per-run autonomy)

- Four levels in `internal/config/config.go:55-58`: `manual`, `guarded`
  (default), `auto`, `yolo`. Carried by `RunRequest.Autonomy`, recorded on
  the run.
- **Green-gate auto-accept**: under `auto` or `yolo`, verdict PASSED accepts
  itself through the same `acceptRun` the button uses
  (`service.go` — "Auto-accept or finish"). UNVERIFIED never auto-accepts,
  even in yolo (P3) — keep this law.
- **ask_human under auto/yolo** returns "no human available" instead of
  pausing (`tools/exec.go`), forcing the model to decide.
- Client plumbing exists and is dead: `client.ts runStart` maps `yes` →
  `autonomy: "yolo"`, but no view passes it. CLI has a `--yes` path.
- `project.toml` has an `autonomy` field that **RunStart does not read** —
  runs default to guarded regardless (gap #1 below).

## What is missing

1. **The orchestrator (the real project).** Nothing launches the next task
   after an accept. Autopilot = a project-level driver that, on accept,
   picks the next startable task (respecting `depends_on` and
   `projectHeld`), launches the test-first→build chain in yolo, and repeats
   until: no startable tasks, N consecutive failures, session budget spent,
   or a pause it must not cross. **Design law: the autopilot is a scripted
   human** — it may only call the same service methods the buttons call
   (`TestStart`, `RunStart`, accept is already automatic), through the same
   queue, with `"by": "autopilot"` on every event it causes.

2. **Dissent-aware auto-accept (prerequisite, valuable today).** Auto-accept
   looks only at the gate verdict. T-028 taught us a reviewer can approve a
   green gate it never verified — and `reviewerDissent` detection lives only
   in the frontend. The ENGINE must refuse to auto-accept when the final
   round's reviewer verdict is request-changes, and pause at the gate
   instead. Without this, yolo rubber-stamps the fleet's most confident
   liar. This fix applies to the existing `auto` level too.

3. **Stop rails.**
   - Project/session budget for the autopilot (per-run budgets don't bound
     a 20-task loop).
   - Failure policy: N consecutive failed/paused tasks → stop and quack.
   - Money caps are never auto-lifted; a budget pause quacks and waits.
   - Every stop reason lands on the record and quacks (the notification
     system is the other half of this mode).

4. **Plumbing gaps.**
   - Honor `project.toml autonomy` as the default for runs launched without
     an explicit level.
   - UI: yolo checkbox on the launcher (per-run), and the Autopilot toggle
     (per-project) with a visible chip + Stop button.
   - Triage claims "under manual and guarded nothing is applied" but no code
     consults autonomy — auto-apply under auto/yolo is unimplemented.
   - Advisor-auto-answer: under yolo, a pending question could take the
     advisor's drafted answer automatically (recorded as advisor-answered)
     instead of the current dry "no human" error.

5. **What yolo must NOT touch.**
   - Shell allowlist (autonomy and shell reach are separate axes).
   - Spec/plan document approval stays human.
   - UNVERIFIED never auto-accepts.

## How current behavior stays intact when off

Autonomy is already per-run data defaulting to guarded. The autopilot is a
component that is not instantiated unless the project mode says so; with it
off, no new code path runs. Pins: guarded behavior byte-identical with the
autopilot compiled in; yolo-loop pins for the driver.

## Order

1. Dissent-aware auto-accept (engine; fixes a real hole in `auto` today).
2. Plumbing: project autonomy honored, yolo checkbox in launcher.
3. The autopilot driver + stop rails + UI toggle.
4. Advisor-auto-answer and triage auto-apply under yolo.
