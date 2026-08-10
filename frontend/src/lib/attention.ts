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

/** Open a URL in the system browser. In the desktop the webview swallows
 * target=_blank, so the shell does it; in a plain browser, window.open. */
export function openExternal(url: string): void {
  const fqn = window.ducklab?.openURL;
  const call = byName();
  if (fqn && call) {
    void call(fqn, url).catch(() => {});
    return;
  }
  window.open(url, "_blank", "noreferrer");
}

// One shared context: browsers cap how many may exist, and a quack per pause
// would exhaust the allowance in an afternoon of work.
let audioCtx: AudioContext | null = null;

/** The quack. Synthesized — two short falling sawtooth bursts through a
 * lowpass is recognisably a duck — so no binary asset rides the bundle.
 *
 * Runs take minutes and run unattended; the person goes to do something else,
 * and an OS notification on another workspace is a card nobody saw. Sound is
 * the one channel that crosses workspaces. Off switch in Settings
 * (ducklab.quack = "off"), because a sound that cannot be silenced teaches
 * the person to mute the whole machine. */
export function quack(): void {
  try {
    if (localStorage.getItem("ducklab.quack") === "off") return;
    const AC = window.AudioContext;
    if (!AC) return;
    audioCtx = audioCtx ?? new AC();
    if (audioCtx.state === "suspended") void audioCtx.resume();
    const t0 = audioCtx.currentTime + 0.01;
    const burst = (start: number, dur: number, f0: number, f1: number) => {
      const ctx = audioCtx!;
      const osc = ctx.createOscillator();
      const filt = ctx.createBiquadFilter();
      const gain = ctx.createGain();
      osc.type = "sawtooth";
      osc.frequency.setValueAtTime(f0, start);
      osc.frequency.exponentialRampToValueAtTime(f1, start + dur);
      filt.type = "lowpass";
      filt.frequency.value = 1100;
      gain.gain.setValueAtTime(0.0001, start);
      gain.gain.exponentialRampToValueAtTime(0.35, start + 0.02);
      gain.gain.exponentialRampToValueAtTime(0.0001, start + dur);
      osc.connect(filt);
      filt.connect(gain);
      gain.connect(ctx.destination);
      osc.start(start);
      osc.stop(start + dur + 0.05);
    };
    burst(t0, 0.18, 560, 280);
    burst(t0 + 0.23, 0.15, 500, 250);
  } catch {
    // No audio device, no permission, no AudioContext: silence, never an
    // error a person has to dismiss.
  }
}

/** Deliver one OS notification. Failures degrade to silence: a missing
 * notification daemon must never become an error a person has to dismiss. */
export function deliver(i: Interruption): void {
  // The quack goes first and unconditionally-of-bindings: the OS notification
  // needs the desktop shell, but the sound works anywhere the webview does.
  quack();
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
