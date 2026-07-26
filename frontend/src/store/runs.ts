import { create } from "zustand";
import type { Run } from "../api/client";
import type { DucklabEvent, ConnectionState } from "../api/events";

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

export const useRuns = create<RunsState>((set) => ({
  runs: {},
  events: {},
  deltas: {},
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
        const duckling = String(e.data?.duckling ?? "unknown");
        const text = String(e.data?.text ?? "");
        const forRun = { ...(state.deltas[runId] ?? {}) };
        forRun[duckling] = (forRun[duckling] ?? "") + text;
        return { ...state, deltas: { ...state.deltas, [runId]: forRun } };
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
      if (run) {
        if (e.type === "human_needed") {
          runs = { ...runs, [runId]: { ...run, status: "paused", pending_kind: String(e.data?.kind ?? "") } };
        } else if (e.type === "run_end") {
          runs = { ...runs, [runId]: { ...run, status: "done", verdict: String(e.data?.verdict ?? run.verdict) } };
        }
      }

      return { ...state, events: { ...state.events, [runId]: trimmed }, runs };
    }),

  // One store update per frame regardless of how many tokens arrived in it.
  applyDeltaBatch: (byRun) =>
    set((state) => {
      if (byRun.size === 0) return state;
      const deltas = { ...state.deltas };
      for (const [runId, perDuckling] of byRun) {
        const forRun = { ...(deltas[runId] ?? {}) };
        for (const [duckling, text] of perDuckling) {
          forRun[duckling] = (forRun[duckling] ?? "") + text;
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
      events: { ...state.events, [run.id]: events.filter((e) => e && e.type !== "token_delta") },
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
      runs: {}, events: {}, deltas: {}, connection: "connecting",
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
    .filter((r) => r.status === "paused" && !!r.pending_kind)
    .sort((a, b) => (a.pending_since ?? "").localeCompare(b.pending_since ?? ""));
}
