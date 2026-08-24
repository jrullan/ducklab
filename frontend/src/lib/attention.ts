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
import type { DucklabEvent } from "../api/events";

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

/** Advisor auto-answers are not state transitions: yolo resumes the run before
 * the next snapshot. Notice the new recorded notification independently. */
export function advisorAutoAnswerInterruptions(
  prev: readonly DucklabEvent[] | null,
  next: readonly DucklabEvent[],
  runs: Record<string, Run>,
): Interruption[] {
  if (prev === null) return [];
  const seen = new Set(prev.map((event) => `${event.run_id ?? ""}:${event.seq ?? ""}`));
  return next.flatMap((event) => {
    const key = `${event.run_id ?? ""}:${event.seq ?? ""}`;
    if (seen.has(key) || event.type !== "notification" || event.data?.kind !== "advisor_auto_answer") return [];
    const run = runs[event.run_id ?? ""];
    const label = run?.task_id || run?.stage || event.run_id || "run";
    const author = String(event.data?.author ?? "advisor").replace(/^advisor:/, "").replace(" (yolo)", "");
    return [{
      runId: event.run_id ?? "",
      title: `${label} auto-answered under yolo`,
      body: `${author} answered: ${String(event.data?.question ?? "a question")} — ${String(event.data?.answer ?? "")}`.slice(0, 240),
    }];
  });
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

/** A real duck, recorded. The first quack was synthesized — two falling
 * sawtooth bursts through a lowpass — which was "recognisably a duck" to its
 * author and to nobody else; the person asked for a real quack before
 * letting the repo out. This one is a single quack from an actual duck
 * (freesound #418509 by Mikes-MultiMedia, extracted from ducks1.wav by
 * squashy555 — both CC0), trimmed to 0.38s, normalized, 24kHz mono WAV,
 * ~18KB embedded so the bundle stays self-contained.
 *
 * Runs take minutes and run unattended; the person goes to do something else,
 * and an OS notification on another workspace is a card nobody saw. Sound is
 * the one channel that crosses workspaces. Off switch in Settings
 * (ducklab.quack = "off"), because a sound that cannot be silenced teaches
 * the person to mute the whole machine. */
const QUACK_WAV_B64 =
  "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ewB9AGkAoADSABoB5gCmAGoASAAlADcASAAPAOf/w/8LAGMAfQB7AJQAlQB5AKAAlACjAIcAYABjAIoAnQCMAFcAewCVALUA4gC8AFoASgBsAF4AawB7AJoA0ADxABEBHgEuAc8AkQCRAJoAqgCoAJwAugDxAB8BPAEPAQ0B/wAjAWoBXwFYARoBBgE3AWEBPQEzAS8BTwF+AYsBWAEGAQcBEQFGAWUBigHOAdIBAwLOAfMBhgHuAKAAoABCAUMBPAE6AYUBKwJHAhgCwQFtAWEBhQGlAawBswHHAb8B0gH0AdEBXQE5AQsBHwE0ATMBLgFYAQQCZAKXAnUCIQIOAhMCHwIBAroBrQHcARoCUQIzAhACoQFcAX4BfwFuATMBdgG8Af0BLAL9AeMB3QHFAdYBeAH+ALUApgC2AM0AEwFNAV0BngHeAdwBSwHpALEAmADeABIBQwFVAaYBqQF4AVAB1wC0AHcApAC9ANkA0wCJAKEAwADmAMwAwAC8ANUAzwCoAK0A1QCxAIgAlwCZAKMAoQDFAOEAEAECAdYAyQC8ALAAiwBpAHYAUQBNAI8AfwBpAEIABQDx//T/+f/6//X/MABvAHAAVgAmAA4A/f8nADoAXwCKAH0ATAA/AEoASAATANL/7f8mACYAPgAxAAsA5/8QAAsA8f/X//L/KQAhADEABACw/8r/7/8XADkAGAA0ACAA9//O/4f/ff+g/8P/3v/3//f/5P8XAFgAYwBbAEAARABgAGUAgQBtAG8AVQBRAHsAmACHAEgAAADl/ycASQA1ABkACwBBAGgAuwCqAIoAVQBZAFUAbABPAAoADQA3AJUAqQCpAIAAcwBzAI4AuACfAHQAdQCIAKEAvQC2AFUASgC2AOsAugC6AJUANQB4AJgAuQAsAfAA0QA+AVoBkwFYAfwA5ADaAAQBNgEMAaoAggCnAM4AKAH1AK0AngDTACYBKAHYAPYAKwE6AXwBYgH1ALoAygBaATgBxABxACwAVQC2APwAlAAmAGEAjwAWAQwBSQBYAGUAnADMAK8AHgFJAHIABwGoAHkAWwASAFsAAgH6ALIAoQAcAN//HQFpAWsAe/+7/2QA5wDiAEoA7v9bAJcAlwBjAKL/af80AIcAMADg/xIAIQATAE8ANgAWAKj/Yv9k/8X/vf9h/2r/lf+g//n/LAC+/27/b/8+/1f/Y/9V//7/AAD+/wUA//8CAAAAAAD//wAA+/8DAP7/AAACAP//AAD///7//f8CAP7//v8EAP//AQD9/wAABQD//wUAAwADAAYAAwAAAPv/AAACAP//BAABAP//AwABAAAAAgABAAEA/f8CAAEA///9/wUAAwD+/wMA/f8BAAMAAQD//wEA///8/wMA+v8GAAAA/f8EAAAAAQABAAAA/////wAABAD+/wEA/v8AAAIA/v8CAP7/AgD//wIA///8/wQAAQAAAP3///8CAAUA/////wAAAgACAAAAAQD6/wIABgAAAAAA/P8EAAIA/v8CAAAAAAD//wIAAQD+/wEAAgD+//////8BAP//AgD//wIAAQD+/wEA/v8AAP//AgABAAIAAgD//wMA/v8BAP7/AwABAPv/BAD6/wIAAAD+/wIAAQAAAAMAAQD+/wAAAgD///7//v8CAAAAAgADAAAAAAAAAP7///8AAP//AAD//wEAAgD9//3/AwD9////AwD8/wMA//8AAAAAAAABAP3/AgADAP3/AQAAAP//AAD//wMA/v///wIAAAD+/wAAAQAAAAAAAgD//wAAAgD+/wIA//8AAAMA/P8DAAEA/P8DAAAA/f8BAAEA/v8AAP//AQD///////8CAP7///8DAAEA/////wMA+/8BAAIA/f8AAAEAAAAAAP//AQAAAAAAAQABAAEA/v8CAP////8BAP7//f8EAP3/AgAAAP3/AgD//wEA//8AAAEA/v8BAAEA//8CAAEA/v///wEAAAD9/wMA/v8BAAEAAAD//wEAAAD+/wMA/v8BAAAA/v8CAAAA/f8DAP//AAACAP3/AgD/////AgAAAP7/AwD8/wEAAQD+/wMA/v8AAAEA/v8AAAIA/////wEA//8BAP///v8CAP3///8CAPv/BQD9//7/AQABAP//AAADAP7/AAD//wEAAAD//wAAAQD+/wEAAQD+/wEAAQAAAAEA//8BAP7/AgAAAP7/AQAAAAAAAAD//wAAAAD///////8AAP7/AwD+////AwD9/wEA//8AAAAA//8CAP//AgD//wAAAgD/////AgD+/wIAAQD+/wIA//8BAAEA/f8DAAAA//8BAAEA//8AAAAAAAD/////AAAAAAAAAQD/////AQADAPv/AwABAP3/AgD//wAA//8AAP7/AgD+/wAAAQD+/wEAAAABAAAA/v8BAAEA/f///wIA/////wEAAgD//wAAAQAAAP////8BAP////8AAAEA/////wMA/v8BAAAAAAAAAAAAAgAAAP7/AQD+/wAA//8AAAAAAAABAP7/AgAAAAAA//8BAAIA/v8BAAIA/v8CAP//AQABAP//AAD//wIA//8AAAEAAgD8/wMAAQD8/wUA/f8AAAIA/v8BAAAAAAAAAAEA//8BAAEA/////wIAAAD9/wIA/////wEA//8BAP//AQAAAAAA//8BAP7/AAABAAAA//8AAAEA//8AAAAAAQD+/wEA//8AAAEA/v8DAP7/AAAAAAEAAAD9/wMA///9/wQA/v///wUA/P8CAAEA/f8AAAAA/v8CAAEA/f8BAAEAAQD9/wEAAgD7/wEA//////7/AgD///7/AgD+/wIA///+/wMA///+/wIAAQD+/wAAAQD+/wIA/v8AAAEA//8AAAAAAAABAP///v8DAP3/AAAAAAAA/////wAAAAABAP//AAD//wMA/f8BAAAAAAABAAAA//8CAP///v8DAP//AQD//wEAAAAAAP//AAAAAP//AQD+/wEAAgD+/wAAAgD//wEA//8BAP//AAABAP///////wEA/v8BAP//AgAAAAEA/v8BAAAAAAD///7/BQD7/wEAAQD9/wIA/v8DAP3/AgABAP//AQD//wIA//8DAP7/AAAAAAAAAQD+/wEAAAACAPz/AwAAAAAAAgD//wIAAAD//wIA/v8AAAAAAAACAP3/AgABAP////8BAAMA/f///wMA/P8CAAEA//8DAAAAAAACAP3/AgABAP3/AQD//wEA/////wEAAAAAAP7/AgAAAAAAAAABAAAAAAAAAP//AgAAAP//AQABAAAA/v8CAP7///8CAP//AAAAAAEAAQD8/wQAAAD+/wIAAQAAAAAAAAAAAAIA/v8AAAIA//8AAAAAAAABAAAA//8AAAAA//8BAP3/AgABAP3/BAD/////AgD9/wIA/v///wEA/v8DAP////8EAP///v8DAP3/AgD/////AgD//wIA/v8BAP////8AAAAAAQAAAAAA/v8AAAAAAAD//wAAAwD+/wEAAAABAP//AAABAP7/AwD+/wAAAgD+/wAAAgD+////AgAAAP7/AwAAAP7/AQAAAAAA/v8CAP//AAACAP3/AwAAAP7/BAD9/wIA/////wIAAAABAP//AwD9/wMA///+/wMA/////wEAAQD//wIA/f8AAAMA/P8CAP7/AAACAP7/AQAAAAAA//8BAAAA//8BAAEA/v8BAAAA/v8CAP7/AAABAP//AAABAAAA//8DAP7/AAAAAAAA//8BAP////8DAP7/AQD//wEA/v8BAAIA+/8DAAAAAAABAP//AwAAAP3/BAAAAP7/AgAAAAAAAQD//wEAAQD9/wAAAwD9////BQD+////AwD//wAAAAAAAAMA/v8BAP7/AgD/////AgABAP7/AQAAAP7/AgD+/wIAAAAAAAAA//8CAAAA/v8DAP7/AAABAP//AAD//wEAAQD//wAAAgD/////AgAAAP7/AQACAP3/AQADAP3/AAACAP////8DAP3/AQABAP//AAAAAAEAAAD//wEAAgD//wEAAAABAP//AAABAAAAAgD//wEAAAAAAP7/AQABAP7/AAAAAAEA//8AAAAAAQD+/wIAAAD//wEAAAAAAAIA/////wIA/v8BAAAAAAAAAAAAAAABAP//AgD//wAAAAACAP7///8DAP3/AAABAAEA/v8CAAEA//8AAAEA//8AAAAAAAABAAAA//8BAAEA/v8CAAEA/v8CAP7/AgD//wAA//8BAP//AAADAPv/AgACAP3/AgD//wIA//8BAP//AAABAP7/AQD//wEA/v8AAAEA//8BAAEA//8BAAAA//8BAAAA/v8BAAAAAAABAP7/AwAAAP3/AgD+////AgD+/wAAAQABAP7/AQABAP7/AwD/////AwD9/wAAAAAAAAAAAAAAAAEA//8AAAEA/////wEA/v8DAP3///8DAP3///8DAP////8CAP3/AwD//wAA//8BAAAA//8CAP//AgD/////AQD//wAAAQD//wIAAQD+/wIAAAD9/wQA/v///wIAAAD9/wMA/v8AAAAA//8BAP//AAAAAAAA//8AAP//AwD8/wEAAgD+/wEAAAAAAAAAAAD/////AQD/////AgD+/wIAAAD//wAAAQD//wEAAAD//wEA//8AAP//AgAAAP7/AAAAAP//AAAAAAAAAAABAAAAAAAAAP//AgD8/wMA/v///wMA/f8AAAEA///+/wMA/v8BAAAA/v8CAP////8BAP////8AAAAAAQACAP3/AQACAPz/AAABAP//AQABAP//AQABAAAA//8AAAIA/v8AAAAAAAD//wAAAAABAP//AAACAP////8BAAAAAQAAAP7/AQACAP3/AAACAP7/AQD//wAAAgD+/wAAAAABAP7/AQABAP7/AgAAAP////8BAAAAAAAAAP7/AQACAP7/AgABAP//AAAAAAEAAAD//wEAAAAAAP////8BAAAA/v8CAAAA//8CAP7/AQAAAP7/AQD//wEA/v8BAAAA/////wEAAAD+/wMA/f8CAAAA//8CAP7/AQAAAAEA/P8BAAIA/P8AAAIA/f8CAAAA/v8DAP7/AQD//wIA/////wMA/v8AAAIAAAD+/wIA/v8BAP//AAABAP7/AAACAP7//v8DAP3/AgD//wAAAQD//wEA//8AAAEA/v8AAAMA/v8AAAAAAQACAP7/AQABAAEA/f8AAAEA///+/wEAAgD9/wIA//8AAAAAAQD//wAA//8CAP///v8DAP7/AgD9/wEAAQD8/wMAAAD//wEAAQABAP7/AQD+/wAAAwD8/wEAAgD//wAA//8BAP7/AQAAAP//AgD9/wEAAQD9/wIA//8AAAAAAQD//wEAAQD//wIA/v8DAAAA//8CAAAAAAAAAAEAAQD+/wIAAAAAAAAA//8BAP//AQD//wAAAAAAAAAAAAABAAAA/v8DAP//AAABAP//AgD//wEA/f8CAP///v8AAAAAAAAAAP7/AgACAPv/AwADAP3//v8FAP7/AAABAAAA/v8DAP///v8EAP7/AQABAAAAAAD//wAAAAAAAAAAAAABAP//AQAAAP7/AgAAAP//AAABAP//AQAAAAAAAgD+/wAAAQABAP////8AAAAAAAD/////AgD//wEA/v8BAAAA/v8BAAAAAAD/////AwD///7/AwD//wAAAQD//wIA/f8BAAEA/v8AAAAAAAD//wMA/f8DAAEA/f8EAPv/AwAAAP3/AwABAP///v8FAP3/AAACAP7/AQD//wEA//8BAP//AgD//wEA/f8DAAAA/P8DAAAA//8AAAEA/f8CAAAA//8BAP//AgAAAP7/AgABAP//AAAAAAAAAgD+/wAAAgD//wAA//8DAP7/AQABAAAAAgD9/wMA/v8AAP//AQD//wEAAAD//wIA/v8AAAIA/v8AAAEA/v///wIAAAAAAAEA/v8EAP7//v8DAP////8AAAEA//8CAAAA//8BAAAAAAD//wIA/f8BAAEA/f8CAAAA/////wMA/v/+/wIAAQD8/wMAAAD+/wMA/P8BAAIA/f8BAAQA/f8AAAIA//8AAAEA//8AAAMA/v8AAAEA/v8CAP3/AgAAAP7/BAD9/wAAAQD/////AQD/////AgAAAP7/AQAAAP7/AwD/////AgD/////AAAAAP//AgD//wAAAAD//wEA//8BAP7/AQABAP3/AgAAAAAAAQD//wEA/v8AAAIA/f8BAAEAAAABAP//AgAAAP//AQAAAP//AAAAAAEA/////wMA/v8AAAIA//8BAP//AgAAAP//AQD+/wIA/////wQA/v8BAAEA/v8BAAAAAAAAAP7/AQACAPz/AgAAAP//AAAAAAAA/f8CAAAA/f8DAP7/AAACAP7/AAABAP3/AQACAP//AQACAAAA//8AAAAAAAAAAAAAAQD+/wEAAAAAAAAAAAABAAAAAAD//wIAAAD+/wAAAAD//wEAAAD/////AwD9////AgD+/wEA//8AAAAAAAAAAAAA//8AAAIA/P8CAAEA/v8BAP//AgD+/wIAAAD9/wIAAQD9/wIAAAD+/wMAAAD9/wIAAAD+/wEA/v8BAAAA/v8BAP//AAD//wAAAgD9/wIAAAAAAP7/BAD+/wAAAQD//wAA//8BAP//AAACAP7/AQAAAAAAAQAAAP//AQADAPv/AwABAP3/AgD+/wIA/////wMA/f8BAAIA/f8CAAAAAAAAAAAAAgD//wAAAAD//wEA/////wMA/f8CAAIA+/8FAP7//v8CAP////8AAAAA//8BAAIA/v8AAAIAAAD+/wEAAQD+/wMA/////wEA//8BAP7/AQACAP7/AQAAAAAAAgD9/wMAAQD9/wUA/v8AAAAAAgD///3/BQD9/wAAAgD+/wEAAAAAAAEA/v8CAAAA//8BAP7/AQAAAP//AQABAP7/AQAAAP//AQAAAAEA///+/wIA///+/wAAAQD+/wIAAAD+/wIA//8BAP////8BAP3/AwD9////AgABAP//AAAAAAAAAAAAAP7/AgD/////AQD+/wEA/v8CAAAA/v8CAP7/AwD+//z/BgD8/wEAAQD+/wIA//8AAP//AgD/////AQD//wEA/////wMA/f8BAAEA/v8CAPz/AwAAAP//AQABAAEAAAABAP//AgAAAAEAAgD+/wIAAgD+/wAAAQD+/wAAAQD+/wIAAAD//wEA//8BAAAA//8CAAAAAAD//wMA/v///wAAAAD+/wAAAQAAAP//AAAAAP7/AgABAP3/AgABAP3/AwAAAP//AQAAAAEA//8BAAAA//8AAAAAAAAAAAAAAQD+/wEAAAAAAP//AAACAP//AAABAAEA//8BAAAAAAAAAAAAAAD//wIAAAAAAAAA//8BAP7/AQAAAP//AQD//wIA//8AAAEA/v8CAAEA/f8EAP7/AwD///7/AQABAAAA/v8CAAEA/v8AAAEAAAD+/wIA/v8AAAIA/v8BAAAAAAD//wEAAAAAAAEA//8BAAEA///+/wIA//8AAAEA//8BAP////8BAP//AAABAAAA//8BAAAAAAAAAP////8CAP////8DAP3/AQACAP7/AQAAAAAAAAABAP//AAAAAAEA//8AAAIA/v8CAP//AAAAAP7/AgD//wAAAQD9/wMA///+/wIAAAACAP7/AgAAAP//AQD//wEAAAD//wIA//8AAAEAAAD/////AQD/////AQABAP3/AgABAP7/AQAAAAAAAAAAAAAAAQD//wEAAQD//wIAAAAAAP//AAAAAP7/AgD//wAAAAABAP7/AgAAAP7/AgD//wEA//8CAP7/AQACAP3/AQACAP//AQD//wIAAAAAAAAA/v8EAPz/AQACAP7/AAABAAEA//8AAAEA//8AAAAA/v8EAP3///8FAPz///8CAAAA/v///wIAAAD//wAAAgD9/wEAAQD//wEAAAAAAAMA/v8BAAEA//8AAAAAAAD//wEA//8DAPv/AQACAPz/AQACAP///v8EAPz/AQABAP//AgAAAAEA//8BAP7/AQD//wEA//8AAAEA//8CAP//AAACAP//AAACAP7/AAACAP7/AgD//wAAAQAAAAAAAAD//wEA/v8CAAAAAAABAP//AQD//wAA//8BAP7/AgABAP7/AgD/////AQD+/wAAAQAAAAAA//8BAP////8CAP7/AgABAP7/AgAAAAAAAAABAP//AAABAP3/AgD/////AgD//wAAAQD+/wEAAQD+/wEAAAD//wEAAAAAAP//AgAAAP7/AQD//wAAAAABAP7/AAABAAAAAQD+/wMA/f8BAAAA//8AAAAAAQD//wEAAAABAAEA//8AAAAAAAD+/wIA/////wEAAAAAAAEA/////wIA/f8CAP7/AAABAAAAAQD//wEAAAAAAP//AQAAAAAAAAAAAAAA//8AAAAA//8BAP//AQD+/wIA/////wEA/v8AAP//AQD+/wAAAgD+/wEAAQD//wAAAQAAAAAA/v8DAP7/AAABAP3/AwAAAAEA//8BAAEA/v8BAAAAAAACAAAA//8CAP////8BAAAAAQABAP//AQAAAP//AAAAAAAAAQABAP7/AgD/////AQD//wAAAAAAAP////8AAAAAAAD//wEAAAAAAAEA//8CAP//AAAAAAAAAAD//wAAAAAAAP//AAAAAAAA//8BAAAA/v8BAAAA//8BAAAA//8DAP7/AQD///////8CAP////8CAP//AQD+/wIA/////wAA/////wAA//8DAP3/AgABAAAAAAAAAAAA//8CAP7/AgAAAAEAAAAAAAAAAAABAP3/AgABAP3/AgD//wEAAQD+/wMA/v8CAP//AAACAP3/BAD/////AgD//wAAAAD//wAA//8BAP//AAABAP7/AQABAP7/AQD//wAAAAD//wIA//8BAAAA/v8CAP////8CAP7/AQD//wEA/////wIA/v8BAP7/AAABAP//AAABAAAAAAACAAAA//8CAP//AQAAAP//AgD+/wAAAAD//wEA//8BAAEA/f8CAAAA/v8CAP//AAABAAAAAAAAAAAAAAAAAP//AAAAAAAAAAD//wAAAQD//wAAAAABAAAA/v8DAP//AAAAAAAAAQAAAP7/AwD+////AQD+/wEAAAD//wIA/v8BAAAAAAAAAP//AQD//wIA/////wEAAAD+/wAAAgD9/wIAAQD+/wEAAQD+/wEA//8AAAEA//8AAAEAAAAAAP//AQD/////AQD/////AQD//wAAAQD//wEA//8BAAEA//8BAAAAAAD//wAAAAAAAP3/AgAAAP////8AAAEA/v8CAP7/AQABAP//AAAAAAEA//8AAAEA//8BAP///v8DAP////8BAP////8BAAAA//8AAAEAAAD+/wEAAAD//wEAAAAAAP//AgD+/wEAAQAAAAAAAAAAAP//AQD//wAAAQAAAAEA//8AAAEA//8AAAAA//8BAAEA//8BAAAAAAAAAAAA//8BAP//AAAAAP//AgD+/wEAAAAAAAAAAAAAAP//AAD//wEA//8BAP7/AQAAAP//AgD9/wIAAAD//wAAAQD//wAAAAD+/wEA//8AAAAAAAAAAP//AgD//wAAAQD//wAAAQAAAP//AQD//wEA/////wEA//8AAAAAAAAAAP//AQAAAAAAAAAAAP//AQD//wAAAQD+/wEAAAD//wAAAAAAAAAA//8BAP////8CAP//AAAAAP//AAABAP//AAABAP//AQD/////AQAAAP7/AAACAP//AAAAAAAAAAD+/wIA//8AAAEA//8BAAAAAAD//wEAAAD//wAAAQD//wEAAQAAAAAAAAABAAAA//8BAAAAAAAAAAAAAAAAAAEA//8BAP//AQAAAP//AQAAAP//AAABAP7/AgD+/wEAAAAAAAEA//8BAAAAAAAAAAEAAAAAAAAAAAABAP//AAABAP//AAABAP//AAABAAAA//8BAAAA//8BAP//AAAAAAAAAQAAAAAAAAAAAAAAAQD//wAAAQAAAAAA//8BAP//AAAAAP//AQD//wAAAQAAAAAAAAD//wAAAAAAAP//AQD//wEAAAAAAAEA//8AAAEAAAABAP//AQAAAP//AQD//wAAAAD//wEA//8AAAEAAAAAAP//AQD/////AQAAAAAAAAAAAAAAAAAAAP//AAAAAAAAAQD//wAAAAAAAAAA//8BAAAAAAABAAAAAAAAAAAAAAAAAP//AQAAAP//AQAAAP//AQAAAAAAAAAAAAEAAAD//wEAAAAAAAAAAAAAAAAAAAAAAAAAAQAAAAAAAQAAAAAAAAD//wEAAAD//wAAAAAAAP//AAAAAAAA//8AAAEAAAD//wEAAAAAAAAAAQD//wAAAAAAAAAA//8BAAAAAAABAP//AQAAAP//AAAAAAEA//8AAAEA//8AAAAA//8AAAAAAAAAAAEA//8BAAAA/v8CAP////8AAAAAAAAAAAAAAAD//wAAAQD//wEAAAAAAAAAAAAAAAAAAQD//wEAAAD//wEAAAD//wEAAAAAAAEA//8AAAAAAQAAAAAAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAAAD//wIA/v8BAAAA//8BAP//AAABAP//AAAAAAAAAAD//wEAAAAAAAAAAAAAAAAAAAAAAAAAAAD//wEAAAAAAAEA//8AAAAA//8AAAAA//8AAAAAAAABAP//AQAAAAAAAAAAAP//AAAAAP//AAAAAAAAAAAAAAAAAQD//wEAAAD//wAAAAD//wAAAAAAAAAAAAAAAP//AAAAAP//AAABAP//AAABAP//AAAAAAAAAAD//wAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEA//8BAAAA//8BAAAAAAABAP//AAAAAAAAAAAAAAAAAAD//wAAAQD//wAAAAAAAP//AQAAAAAAAQAAAAAAAAAAAAAAAAAAAAEA//8AAAAAAAAAAAAAAAAAAAAA//8AAAAAAAAAAAEAAAAAAAEA//8AAAAA//8AAAAAAAAAAP//AAAAAAAAAAAAAAAAAAAAAAAA//8BAAAAAAAAAP//AQD//wAAAAAAAAAAAAAAAAAAAAAAAAAAAQD//wAAAAAAAAAAAAABAAAAAAAAAAAA//8AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAP//AQAAAAAAAQAAAAAAAAD//wAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD//wAAAAAAAAEAAAAAAAAAAAAAAP//AQAAAAAAAQAAAAAAAAABAP//AAAAAAAAAQAAAAAAAAAAAAAAAAAAAAAAAQD//wAAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA//8AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD//wEAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

let quackBuffer: AudioBuffer | null = null;
let quackDecodeFailed = false;

function playBuffer(ctx: AudioContext, buf: AudioBuffer): void {
  const src = ctx.createBufferSource();
  const gain = ctx.createGain();
  src.buffer = buf;
  gain.gain.value = 0.9;
  src.connect(gain);
  gain.connect(ctx.destination);
  src.start();
}

/** The synthesized duck, kept as the fallback for a platform whose WebAudio
 * cannot decode the WAV — a worse quack beats a silent pause. */
function synthQuack(ctx: AudioContext): void {
  const t0 = ctx.currentTime + 0.01;
  const burst = (start: number, dur: number, f0: number, f1: number) => {
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
}

export function quack(): void {
  try {
    if (localStorage.getItem("ducklab.quack") === "off") return;
    const AC = window.AudioContext;
    if (!AC) return;
    audioCtx = audioCtx ?? new AC();
    if (audioCtx.state === "suspended") void audioCtx.resume();
    const ctx = audioCtx;
    if (quackBuffer) {
      playBuffer(ctx, quackBuffer);
      return;
    }
    if (quackDecodeFailed) {
      synthQuack(ctx);
      return;
    }
    // First quack: decode, cache, then play — a notification a few
    // milliseconds late is still a notification.
    const raw = atob(QUACK_WAV_B64);
    const bytes = new Uint8Array(raw.length);
    for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
    ctx.decodeAudioData(
      bytes.buffer,
      (buf) => {
        quackBuffer = buf;
        playBuffer(ctx, buf);
      },
      () => {
        quackDecodeFailed = true;
        synthQuack(ctx);
      },
    );
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
