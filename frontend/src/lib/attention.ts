/**
 * Attention: deciding when the person should be called back, and calling them.
 *
 * The engine models "waiting for a human" precisely, and the client rendered it
 * as three passive indicators on three screens — so the one person the product
 * serves polled views to find out whether anything had happened. Runs take
 * minutes and run unattended; the moment one pauses is the moment the product
 * has something to say, and it is the only moment worth interrupting for.
 */
import type { Run } from "../api/client";

/** One thing worth interrupting a person for. */
export type Interruption = {
  runId: string;
  title: string;
  body: string;
};

/**
 * What changed between two snapshots of the runs that merits an interruption.
 *
 * Pure, so it is testable without a desktop. The rules:
 * - A run newly paused for a human: yes. That is the product's whole reason to
 *   speak.
 * - A run newly failed: yes. The person decides whether to relaunch.
 * - A run that finished without needing anything: NO. Silence is information;
 *   spending it on non-decisions teaches the person to ignore the sound.
 * - The first snapshot (prev unknown): nothing. The app just opened and the
 *   person is already looking at it.
 * - The same state seen twice: nothing. An interruption repeated is an alarm.
 */
export function interruptions(
  prev: Record<string, Run> | null,
  next: Record<string, Run>,
): Interruption[] {
  if (prev === null) return [];
  const out: Interruption[] = [];
  for (const run of Object.values(next)) {
    const before = prev[run.id];
    const label = run.task_id || run.stage || run.id;

    const waitsNow = run.status === "paused" && !!run.pending_kind;
    const waitedBefore = !!before && before.status === "paused" && !!before.pending_kind;
    if (waitsNow && !waitedBefore) {
      out.push({
        runId: run.id,
        title: `${label} needs you`,
        body:
          run.pending_kind === "question"
            ? "a duckling asked a question"
            : `${run.verdict || "finished"} — waiting at the gate`,
      });
      continue;
    }

    const failedNow = run.status === "failed";
    const failedBefore = !!before && before.status === "failed";
    if (failedNow && !failedBefore) {
      out.push({
        runId: run.id,
        title: `${label} failed`,
        // The first line of the reason. The full text is one click away; a
        // notification that scrolls is a notification nobody reads.
        body: (run.failure ?? "").split("\n")[0]?.slice(0, 120) || run.verdict || "failed",
      });
    }
  }
  return out;
}

const byName = () => window.wails?.Call?.ByName;

/** Deliver one OS notification. Failures degrade to silence: a missing
 * notification daemon must never become an error a person has to dismiss. */
export function deliver(i: Interruption): void {
  const fqn = window.ducklab?.notify;
  const call = byName();
  if (!fqn || !call) return;
  void call(fqn, i.runId, i.title, i.body).catch(() => {});
}

/** Put the waiting count where a task switcher can see it. */
export function setBadge(count: number): void {
  const fqn = window.ducklab?.setBadge;
  const call = byName();
  if (fqn && call) {
    void call(fqn, count).catch(() => {});
  }
  // The in-page title too: costs nothing, and in a browser tab (dev mode) it
  // is the only badge there is.
  document.title = count > 0 ? `ducklab ● ${count}` : "ducklab";
}
