import { create } from "zustand";
import type { Run } from "../api/client";
import type { DucklabEvent, ConnectionState } from "../api/events";

/** One run's spend so far, as the engine reports it while the run is going. */
export type LiveSpend = {
  usd: number;
  tokens: number;
  turns: number;
  wallclock_s: number;
  limit: { usd: number; tokens: number; turns: number; wallclock_s: number };
  /** Keyed by duckling, because "the run has spent 300k" does not say which
   * model to change. */
  ducklings: Record<
    string,
    { calls: number; tokens: number; cost_usd: number; estimated?: boolean }
  >;
};

/**
 * Run state for the UI.
 *
 * The rule that shapes this store: the engine owns the truth (I11). Nothing
 * here may invent state the engine has not confirmed — in particular an accept
 * stays PENDING until the engine returns a commit sha, so the UI can never
 * show a commit that does not exist (AC-34).
 */

export type AcceptState =
  | { kind: "idle" }
  | { kind: "pending" }
  | { kind: "committed"; sha: string }
  | { kind: "error"; message: string };

export interface RunsState {
  runs: Record<string, Run>;
  events: Record<string, DucklabEvent[]>;
  /** Streamed text per run, keyed by duckling. Display only, never persisted. */
  deltas: Record<string, Record<string, string>>;
  /** Streamed thinking, keyed exactly like deltas. Kept apart from the answer
   * all the way to the screen: a model that reasons pays for those tokens
   * whether or not anyone sees them, but folding them into the reply would show
   * its false starts as what it said. */
  reasoning: Record<string, Record<string, string>>;
  /** What each run has spent so far, and which duckling spent it. The totals
   * only reached the run record when the run ended, so the meter read zero for
   * however long the work took and then jumped to the final number. */
  spend: Record<string, LiveSpend>;
  connection: ConnectionState;
  acceptState: Record<string, AcceptState>;
  /** True after an overflow, until the caller refetches. */
  needsResync: boolean;

  applyEvent: (e: DucklabEvent) => void;
  /** Applies a frame's worth of streamed text in one update (AC-33). */
  applyDeltaBatch: (byRun: Map<string, Map<string, string>>) => void;
  setRuns: (runs: Run[]) => void;
  setRun: (run: Run) => void;
  /** Replaces a run's events wholesale after an overflow resync. */
  resyncRun: (run: Run, events: DucklabEvent[]) => void;
  setConnection: (s: ConnectionState) => void;
  markOverflow: () => void;
  clearResync: () => void;
  beginAccept: (runId: string) => void;
  confirmAccept: (runId: string, sha: string) => void;
  failAccept: (runId: string, message: string) => void;
  reset: () => void;
}

const MAX_EVENTS_PER_RUN = 20000;
/** Display-only stream buffers must stay bounded during long live turns. */
export const MAX_STREAM_BUFFER_BYTES = 64 * 1024;

function appendBounded(existing: string, text: string): string {
  const combined = existing + text;
  const bytes = new TextEncoder().encode(combined);
  if (bytes.byteLength <= MAX_STREAM_BUFFER_BYTES) return combined;
  let start = bytes.byteLength - MAX_STREAM_BUFFER_BYTES;
  const decoder = new TextDecoder();
  // Start on a UTF-8 boundary: decoding from inside a multibyte code point
  // inserts U+FFFD, which can itself push the reconstructed string past cap.
  while (start < bytes.length) {
    const value = decoder.decode(bytes.slice(start));
    if (new TextEncoder().encode(value).byteLength <= MAX_STREAM_BUFFER_BYTES) return value;
    start += 1;
  }
  return "";
}

function discardTurn(
  buffers: Record<string, Record<string, string>>,
  runId: string,
  key: string,
): Record<string, Record<string, string>> {
  const forRun = buffers[runId];
  if (!forRun || !(key in forRun)) return buffers;
  const { [key]: _discarded, ...remaining } = forRun;
  if (Object.keys(remaining).length === 0) {
    const { [runId]: _run, ...otherRuns } = buffers;
    return otherRuns;
  }
  return { ...buffers, [runId]: remaining };
}

export const useRuns = create<RunsState>((set) => ({
  runs: {},
  events: {},
  deltas: {},
  reasoning: {},
  spend: {},
  connection: "connecting",
  acceptState: {},
  needsResync: false,

  applyEvent: (e) =>
    set((state) => {
      const runId = e.run_id ?? "";
      if (!runId) return state;

      // token_delta is display state: it accumulates text but is never added
      // to the event log, which mirrors the engine never persisting it.
      if (e.type === "token_delta") {
        // Keyed by turn, not by duckling. A council takes two architect
        // turns, and keyed by duckling the second one appended to the first
        // one's text and both lanes showed the concatenation.
        const key = deltaKey(e.data);
        const text = String(e.data?.text ?? "");
        const forRun = { ...(state.deltas[runId] ?? {}) };
        forRun[key] = appendBounded(forRun[key] ?? "", text);
        return { ...state, deltas: { ...state.deltas, [runId]: forRun } };
      }
      if (e.type === "reasoning_delta") {
        const key = deltaKey(e.data);
        const text = String(e.data?.text ?? "");
        const forRun = { ...(state.reasoning[runId] ?? {}) };
        forRun[key] = appendBounded(forRun[key] ?? "", text);
        return { ...state, reasoning: { ...state.reasoning, [runId]: forRun } };
      }
      if (e.type === "budget") {
        const d = e.data ?? {};
        return {
          ...state,
          spend: { ...state.spend, [runId]: d as unknown as LiveSpend },
        };
      }
      if (e.type === "heartbeat") return state;

      const existing = state.events[runId] ?? [];
      // Deduplicate by seq: a reconnect may overlap with the backlog.
      if (typeof e.seq === "number" && existing.some((x) => x.seq === e.seq)) {
        return state;
      }
      const appended = [...existing, e];
      const trimmed =
        appended.length > MAX_EVENTS_PER_RUN
          ? appended.slice(appended.length - MAX_EVENTS_PER_RUN)
          : appended;

      // Reflect status changes the engine reports, without inventing any.
      const run = state.runs[runId];
      let runs = state.runs;
      if (!run && e.type === "run_start") {
        // A run that begins while this client is connected. Without this the
        // store only ever updated runs it already knew, so starting a run from
        // the CLI with the desktop open left it invisible until a refetch —
        // and the desktop exists to watch runs happen.
        //
        // The record is provisional and made only of what the event states;
        // the engine corrects it on the next fetch.
        runs = {
          ...runs,
          [runId]: {
            id: runId,
            project_id: String(e.project_id ?? ""),
            stage: String(e.data?.stage ?? "build"),
            mode: String(e.data?.mode ?? ""),
            task_id: String(e.data?.task_id ?? ""),
            status: "running",
            verdict: "",
            // The engine's own stamp when the event carries it; otherwise the
            // bus timestamp NORMALIZED to UTC-Z — it arrives with a local
            // offset, and a lexical sort against UTC-Z strings buried the
            // provisional record hours away from where it belonged.
            started_at: String(
              e.data?.started_at ?? (e.ts ? new Date(String(e.ts)).toISOString().replace(/\.\d+Z$/, "Z") : ""),
            ),
          },
        };
      } else if (run) {
        if (e.type === "run_queued") {
          // The engine parked it — another run holds the project's tree, or
          // every slot is taken. The store ignored this, so a batch of TDD
          // launches all read "running" in Now while only one of them was.
          runs = { ...runs, [runId]: { ...run, status: "queued" } };
        } else if (e.type === "run_started" && run.status === "queued") {
          // The queue promoted it. Only a queued run can be promoted; on any
          // other status this is a stale or replayed frame and changing
          // state on it would invent a transition the engine never made.
          runs = { ...runs, [runId]: { ...run, status: "running" } };
        } else if (e.type === "human_needed") {
          runs = { ...runs, [runId]: { ...run, status: "paused", pending_kind: String(e.data?.kind ?? "") } };
        } else if (e.type === "human" && run.status === "paused") {
          // Answering a human gate resumes the run. Remove every pending
          // field, not just the kind — and the offered actions: next
          // [resume, abort] kept the decision card open over a run that was
          // already working again.
          const { pending_kind: _pendingKind, pending_since: _pendingSince, pending_data: _pendingData, next: _next, ...resumed } = run;
          runs = { ...runs, [runId]: { ...resumed, status: "running" } };
        } else if (e.type === "checkpoint" && String((e.data as { reason?: string })?.reason ?? "") === "resume" && run.status === "paused") {
          // Resume's own checkpoint. The engine clears the record it holds;
          // without this branch the store's copy stayed paused with
          // next=[resume, abort], and the "Waiting for your decision" card —
          // and Now's inbox count — outlived the click that answered them.
          const { pending_kind: _pk, pending_since: _ps, pending_data: _pd, next: _n, failure: _f, ...resumed } = run;
          runs = { ...runs, [runId]: { ...resumed, status: "running" } };
        } else if (e.type === "error") {
          // The engine emits `error` only on the fatal paths, with the reason.
          // The store used to drop it — so a run watched LIVE failed with
          // "response truncated" and the person watched a frozen lane, found
          // "running" in Now, and learned the truth minutes later from a
          // refetch. The failure notification keys on this transition too.
          runs = {
            ...runs,
            [runId]: { ...run, status: "failed", failure: String(e.data?.error ?? run.failure ?? "") },
          };
        } else if (e.type === "run_end") {
          // done and failed are different ends: a tournament with no winner
          // ends done with verdict FAILED. The error event above is what
          // marks a harness failure, so run_end preserves it.
          const status = run.status === "failed" ? "failed" : "done";
          runs = { ...runs, [runId]: { ...run, status, verdict: String(e.data?.verdict ?? run.verdict) } };
        }
      }

      // A structure repair and a resumed checkpoint both restart the same
      // round:turn coordinate. Their first token must begin a fresh live
      // buffer; otherwise attempt three visibly continues attempt one.
      const key = e.type === "turn_end" || e.type === "turn_start" ? deltaKey(e.data) : "";
      return {
        ...state,
        events: { ...state.events, [runId]: trimmed },
        runs,
        // The durable message event is in events; streamed answer/thinking are
        // only live display buffers and must not survive a completed turn.
        deltas: key ? discardTurn(state.deltas, runId, key) : state.deltas,
        reasoning: key ? discardTurn(state.reasoning, runId, key) : state.reasoning,
      };
    }),

  // One store update per frame regardless of how many tokens arrived in it.
  applyDeltaBatch: (byRun) =>
    set((state) => {
      if (byRun.size === 0) return state;
      const deltas = { ...state.deltas };
      for (const [runId, perDuckling] of byRun) {
        const forRun = { ...(deltas[runId] ?? {}) };
        for (const [duckling, text] of perDuckling) {
          forRun[duckling] = appendBounded(forRun[duckling] ?? "", text);
        }
        deltas[runId] = forRun;
      }
      return { ...state, deltas };
    }),

  setRuns: (list) =>
    set((state) => ({
      ...state,
      runs: Object.fromEntries(list.map((r) => [r.id, r])),
    })),

  setRun: (run) => set((state) => ({ ...state, runs: { ...state.runs, [run.id]: run } })),

  // A resync REPLACES rather than merges: after an overflow we do not know
  // which events we missed, and appending would leave a gap in the middle
  // that looks like a complete transcript.
  resyncRun: (run, events) =>
    set((state) => ({
      ...state,
      runs: { ...state.runs, [run.id]: run },
      events: {
        ...state.events,
        [run.id]: events.filter(
          (e) => e && e.type !== "token_delta" && e.type !== "reasoning_delta",
        ),
      },
    })),

  setConnection: (connection) => set((state) => ({ ...state, connection })),

  markOverflow: () => set((state) => ({ ...state, needsResync: true })),
  clearResync: () => set((state) => ({ ...state, needsResync: false })),

  beginAccept: (runId) =>
    set((state) => ({ ...state, acceptState: { ...state.acceptState, [runId]: { kind: "pending" } } })),

  confirmAccept: (runId, sha) =>
    set((state) => ({
      ...state,
      acceptState: { ...state.acceptState, [runId]: { kind: "committed", sha } },
      runs: state.runs[runId]
        ? { ...state.runs, [runId]: { ...state.runs[runId]!, accepted: true, commit_sha: sha, status: "done" } }
        : state.runs,
    })),

  failAccept: (runId, message) =>
    set((state) => ({
      ...state,
      acceptState: { ...state.acceptState, [runId]: { kind: "error", message } },
    })),

  reset: () =>
    set({
      runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {}, connection: "connecting",
      acceptState: {}, needsResync: false,
      applyEvent: useRuns.getState().applyEvent,
      applyDeltaBatch: useRuns.getState().applyDeltaBatch,
      setRuns: useRuns.getState().setRuns,
      setRun: useRuns.getState().setRun,
      resyncRun: useRuns.getState().resyncRun,
      setConnection: useRuns.getState().setConnection,
      markOverflow: useRuns.getState().markOverflow,
      clearResync: useRuns.getState().clearResync,
      beginAccept: useRuns.getState().beginAccept,
      confirmAccept: useRuns.getState().confirmAccept,
      failAccept: useRuns.getState().failAccept,
      reset: useRuns.getState().reset,
    }),
}));

/** Runs waiting on a person, newest wait first — the human-gate inbox. */
export function pendingForHuman(runs: Record<string, Run>): Run[] {
  return Object.values(runs)
    // Chat is a conversation, not a human-gated run. In particular, an ended
    // or aborted chat must never be routed through the accept/reject inbox.
    .filter((r) => r.status === "paused" && !!r.pending_kind && r.stage !== "chat")
    .sort((a, b) => (a.pending_since ?? "").localeCompare(b.pending_since ?? ""));
}

/** The key a streamed fragment accumulates under: one turn of one run. */
export function deltaKey(data: Record<string, unknown> | undefined): string {
  return `${Number(data?.round ?? 1)}:${Number(data?.turn ?? 0)}`;
}
