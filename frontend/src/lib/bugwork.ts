import type { Bug, Run } from "../api/client";

/** A report the person is owed a next step on because nothing is running for
 * it — and which step, because the three cases want different doors:
 *
 * - reopened: the person sent it back after its fix. The engine refuses new
 *   work until it is triaged again, so the door is re-triage, never a rerun.
 * - remaining: a promotion with an unaccepted task that has no run. The door
 *   is THAT task — TI-36X B-003 offered to rerun the accepted T-014 while
 *   T-015 was the half still owed.
 * - unsettled: every current task accepted and the report still in progress.
 *   Rerunning accepted work answers nothing; the door is the bug's own move. */
export type IdleBug =
  | { kind: "reopened"; bug: Bug }
  | { kind: "remaining"; bug: Bug; task: string }
  | { kind: "unsettled"; bug: Bug; tasks: string[] };

const LIVE_RUN = new Set(["running", "queued", "paused"]);
// The engine's own word for a task with a run going or waiting at its gate;
// covers the moment between a launch and the store hearing about the run.
const LIVE_TASK = new Set(["in_progress", "review"]);

/** Task ids with a run in flight. Every live run counts — a paused chat about
 * a task is someone working on it (B-251) — and terminal ones never do. */
export function tasksInFlight(runs: Run[]): Set<string> {
  return new Set(runs.filter((r) => LIVE_RUN.has(r.status) && !!r.task_id).map((r) => r.task_id));
}

/** The reports with nothing running for them, in the order given.
 *
 * TI-36X B-003 was listed "reopened" because its task_id — only the first
 * half of a split — had no run, while the second half sat paused at its gate.
 * So every task of the current promotion is read, and status in_progress is
 * never taken to mean reopened: the engine says that itself. */
export function idleBugs(bugs: Bug[], inFlight: Set<string>): IdleBug[] {
  const out: IdleBug[] = [];
  for (const bug of bugs) {
    if (bug.reopened && (bug.status === "triaged" || bug.status === "in_progress")) {
      // A reopen retires the promotion, so its tasks are no longer current —
      // but one relaunched by hand is still something running for the report.
      if (!(bug.tasks ?? []).some((t) => inFlight.has(t.id) || LIVE_TASK.has(t.status))) {
        out.push({ kind: "reopened", bug });
      }
      continue;
    }
    if (bug.status !== "in_progress") continue;
    const current = currentTasks(bug);
    if (current.length === 0) continue;
    if (current.some((t) => inFlight.has(t.id) || LIVE_TASK.has(t.status))) continue;
    const owed = current.find((t) => t.status !== "accepted");
    if (owed) out.push({ kind: "remaining", bug, task: owed.id });
    else out.push({ kind: "unsettled", bug, tasks: current.map((t) => t.id) });
  }
  return out;
}

function currentTasks(bug: Bug) {
  return (bug.tasks ?? []).filter((t) => t.current);
}
