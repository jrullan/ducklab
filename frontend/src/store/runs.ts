import { create } from "zustand";
import type { ApprovalRequest, Run } from "../api/client";
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
    { calls: number; tokens: number; cost_usd: number; estimated?: boolean; reasoning_tokens?: number; content_tokens?: number }
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
  /** verifying: the engine is reproducing the gate from a clean checkout —
   * announced by its gate_started, or by an in_progress answer from an
   * accept that outlived the request (B-529). Still pending: no commit is
   * shown until the engine confirms one (accept_settled). */
  | { kind: "pending"; verifying?: boolean; attempt?: string }
  /** warning/info: the accept's publication consequences (a failed push,
   * a local-only commit), carried however the settlement arrived — the
   * HTTP answer, the live accept_settled, or last_accept on a run read. */
  | { kind: "committed"; sha: string; warning?: string; info?: string }
  /** interrupted: abort or engine shutdown stopped the accept's replay. Not
   * a failure of the work — nothing was decided (B-529). */
  | { kind: "error"; message: string; interrupted?: boolean };

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
  /** Moves on every overflow or reconnect. needsResync is one flag that the
   * route's run clears at once; a surface holding a run OUTSIDE the route
   * (the consultant pane) missed it and kept a transcript with a permanent
   * hole (Codex on #166). Such surfaces refetch when this number changes. */
  resyncEpoch: number;
  /** Moves on every vision_evidence event (B-515): a run's image request
   * verified or refuted a duckling's vision, so every surface holding the
   * fleet's vision_status refetches it. */
  fleetEpoch: number;
  /** Moves on every bulk replacement of the run list (setRuns: the
   * reconnect and overflow refresh, a project switch). A list payload does
   * not carry accept_in_progress / last_accept, so an accept still verifying
   * refetches its run on this — the only way an accept answered in_progress
   * settles when the stream that would have carried accept_settled dropped
   * it (review of #181). */
  runsEpoch: number;

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
  /** A resync for run-holding surfaces without the overflow flag (reconnect). */
  requestResync: () => void;
  beginAccept: (runId: string) => void;
  /** The engine answered in_progress: keep waiting, now for accept_settled. */
  acceptInProgress: (runId: string, attempt?: string) => void;
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

/**
 * Settles a verifying accept from the run record itself (review of #181).
 * The live accept_settled event is not the only witness: after an overflow
 * the resync replaces events without replaying them, and an accept answered
 * in_progress stayed "Accepting…" forever. The engine's run read carries
 * accept_in_progress and last_accept for exactly this.
 *
 * Only a verifying accept is settled here: the engine has then confirmed the
 * accept exists, so a last_accept with no accept still in progress is its
 * outcome — not a settlement left over from an earlier attempt.
 */
export function reconcileAccept(acceptState: Record<string, AcceptState>, run: Run): Record<string, AcceptState> {
  const st = acceptState[run.id];
  if (st?.kind !== "pending" || !st.verifying || run.accept_in_progress) return acceptState;
  const last = run.last_accept;
  // Review of #181: only THIS attempt's settlement is this accept's outcome.
  // An older attempt's (the engine died mid-accept, or a backlog replays an
  // earlier one) is not, and adopting it reported attempt one's refusal as
  // attempt two's.
  const mine = last && st.attempt && last.attempt === st.attempt ? settlementState(last) : null;
  if (mine) return { ...acceptState, [run.id]: mine };
  // The commit is a fact whoever's attempt made it.
  if (run.accepted && run.commit_sha) {
    return { ...acceptState, [run.id]: settlementState({ outcome: "accepted", commit_sha: run.commit_sha, warning: run.warning })! };
  }
  // The engine has this attempt nowhere in progress and recorded no outcome
  // for it: it will never settle. Say so rather than wait forever. (With no
  // attempt known yet, the accept's own HTTP answer is still to come.)
  if (st.attempt) {
    return {
      ...acceptState,
      [run.id]: {
        kind: "error", interrupted: true,
        message: "the engine no longer has this accept in progress and recorded no outcome for it; nothing is known to have been decided — read the run before accepting again",
      },
    };
  }
  return acceptState;
}

// Approval statuses that are final: a request in one of them never moves
// again (B-530: pending → approved → landed | failed | interrupted, or
// pending → declined | superseded).
const TERMINAL_APPROVAL = new Set(["landed", "failed", "interrupted", "declined", "superseded"]);
const APPROVAL_RANK: Record<string, number> = { pending: 0, approved: 1 };
const approvalRank = (status: string) => (TERMINAL_APPROVAL.has(status) ? 2 : APPROVAL_RANK[status] ?? 0);

/** The later of two copies of one request: a status that has been reached
 * never regresses, whichever copy arrives last (review of #181: an
 * approval_resolved{landed} that beat the approve's HTTP answer was
 * overwritten back to "approved — accept in progress"). */
function laterApproval(cur: ApprovalRequest, next: ApprovalRequest): ApprovalRequest {
  return approvalRank(next.status) >= approvalRank(cur.status) ? next : cur;
}

/** next's requests, with every request kept at its furthest status. */
function keepApprovalProgress(prev: Run | undefined, next: Run): Run {
  const before = prev?.approval_requests;
  if (!before?.length || !next.approval_requests) return next;
  const byId = new Map(before.map((r) => [r.id, r]));
  let changed = false;
  const merged = next.approval_requests.map((r) => {
    const cur = byId.get(r.id);
    if (!cur) return r;
    const kept = laterApproval(cur, r);
    if (kept !== r) changed = true;
    return kept;
  });
  return changed ? { ...next, approval_requests: merged } : next;
}

/**
 * The one reading of an accept settlement (review of #181): the live
 * accept_settled payload and a run read's last_accept are the same object,
 * so every path yields the same state — commit with its warning and info,
 * or the refusal's reason with the interrupted flag. Null when the payload
 * is not a settlement.
 */
export function settlementState(payload: Record<string, unknown> | undefined | null): AcceptState | null {
  const outcome = typeof payload?.outcome === "string" ? payload.outcome : "";
  if (!outcome) return null;
  const text = (v: unknown) => (typeof v === "string" && v ? v : undefined);
  if (outcome === "accepted") {
    const sha = text(payload?.commit_sha);
    if (!sha) return null;
    const st: AcceptState = { kind: "committed", sha };
    const warning = text(payload?.warning);
    const info = text(payload?.info);
    if (warning) st.warning = warning;
    if (info) st.info = info;
    return st;
  }
  const message = text(payload?.error) ?? `accept ${outcome}`;
  return outcome === "interrupted" ? { kind: "error", message, interrupted: true } : { kind: "error", message };
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
  resyncEpoch: 0,
  fleetEpoch: 0,
  runsEpoch: 0,

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
          // B-495: the pause changes what is legal, and the event does not say
          // what. Keeping the running record's next=[abort] showed a gate
          // that had PASSED with only Abort on Now, while the engine already
          // offered accept/reject. Dropping the previous state's offers and
          // pending payload is what makes App hydrate the run from the engine.
          const { next: _next, pending_data: _pendingData, pending_since: _pendingSince, ...waiting } = run;
          runs = { ...runs, [runId]: { ...waiting, status: "paused", pending_kind: String(e.data?.kind ?? "") } };
        } else if (e.type === "human" && run.status === "paused" && String(e.data?.resolution ?? "") !== "plan_revision") {
          // Answering a human gate resumes the run. A plan-revision answer is
          // terminal routing, not a resume; its following run_end owns status.
          // Remove every pending
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
        } else if (APPROVAL_EVENTS.has(e.type) && isApprovalRequest(e.data?.request)) {
          // B-530: every approval event carries the whole request as it now
          // stands, so the run's list — and Now's — follows without a
          // refetch: a request appears the moment an agent files it and
          // leaves the pending list the moment the person answers.
          runs = { ...runs, [runId]: { ...run, approval_requests: upsertApproval(run.approval_requests, e.data.request) } };
        } else if (e.type === "consultant_switched" && typeof e.data?.to === "string" && e.data.to) {
          // B-513: the seat moved. Every composer reads the consultant from
          // the record, so the record must follow without a refetch — or the
          // vision note keeps describing the duckling that left.
          runs = { ...runs, [runId]: { ...run, roster: { ...(run.roster ?? {}), consultant: e.data.to } } };
        } else if (e.type === "error") {
          // The engine emits `error` only on the fatal paths, with the reason.
          // The store used to drop it — so a run watched LIVE failed with
          // "response truncated" and the person watched a frozen lane, found
          // "running" in Now, and learned the truth minutes later from a
          // refetch. The failure notification keys on this transition too.
          // A failed run offers nothing (the task's own list owns relaunch);
          // a running run's [abort] or a paused one's decision must not
          // outlive the failure (B-495's class).
          const { next: _next, pending_kind: _pk, pending_since: _ps, pending_data: _pd, ...failed } = run;
          runs = {
            ...runs,
            [runId]: { ...failed, status: "failed", failure: String(e.data?.error ?? run.failure ?? "") },
          };
        } else if (e.type === "run_end") {
          // done and failed are different ends: a tournament with no winner
          // ends done with verdict FAILED. The error event above is what
          // marks a harness failure, so run_end preserves it. A terminal
          // event is also authoritative about there being no decision left:
          // retaining the paused record's offers kept the gate card visible
          // after Accept had already completed the run.
          const status = run.status === "failed" ? "failed" : "done";
          const {
            pending_kind: _pendingKind,
            pending_since: _pendingSince,
            pending_data: _pendingData,
            next: _next,
            ...finished
          } = run;
          runs = { ...runs, [runId]: { ...finished, status, verdict: String(e.data?.verdict ?? run.verdict), next: [] } };
        }
      }

      // A structure repair and a resumed checkpoint both restart the same
      // round:turn coordinate. Their first token must begin a fresh live
      // buffer; otherwise attempt three visibly continues attempt one.
      const key = e.type === "turn_end" || e.type === "turn_start" ? deltaKey(e.data) : "";

      // B-529: an accept outlives its request. While this client waits on
      // one, the stream says what it is doing (the clean-checkout replay)
      // and how it ended — the only place the outcome of an accept answered
      // in_progress ever arrives. A commit is shown only once settled.
      let acceptState = state.acceptState;
      if (acceptState[runId]?.kind === "pending") {
        const pendingAttempt = (acceptState[runId] as { attempt?: string }).attempt;
        if (e.type === "gate_started" && e.data?.phase === "accept") {
          acceptState = { ...acceptState, [runId]: { ...acceptState[runId], kind: "pending", verifying: true } };
        } else if (e.type === "accept_settled" && pendingAttempt && e.data?.attempt === pendingAttempt) {
          // Only this accept's own attempt settles it (review of #181). Until
          // the engine has named the attempt (its in_progress answer), no
          // settlement is adopted: one arriving then may be an older
          // attempt's, still in transit — this accept's HTTP answer decides.
          const settledState = settlementState(e.data);
          if (settledState) {
            acceptState = { ...acceptState, [runId]: settledState };
            const current = runs[runId];
            if (settledState.kind === "committed" && current) {
              runs = { ...runs, [runId]: { ...current, accepted: true, commit_sha: settledState.sha, status: "done" } };
            }
          }
        }
      }
      return {
        ...state,
        events: { ...state.events, [runId]: trimmed },
        runs,
        acceptState,
        fleetEpoch: e.type === "vision_evidence" ? state.fleetEpoch + 1 : state.fleetEpoch,
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
      runs: Object.fromEntries(list.map((r) => [r.id, keepApprovalProgress(state.runs[r.id], r)])),
      runsEpoch: state.runsEpoch + 1,
    })),

  setRun: (run) =>
    set((state) => ({
      ...state,
      runs: { ...state.runs, [run.id]: keepApprovalProgress(state.runs[run.id], run) },
      acceptState: reconcileAccept(state.acceptState, run),
    })),

  // A resync REPLACES rather than merges: after an overflow we do not know
  // which events we missed, and appending would leave a gap in the middle
  // that looks like a complete transcript.
  resyncRun: (run, events) =>
    set((state) => ({
      ...state,
      runs: { ...state.runs, [run.id]: keepApprovalProgress(state.runs[run.id], run) },
      acceptState: reconcileAccept(state.acceptState, run),
      events: {
        ...state.events,
        [run.id]: events.filter(
          (e) => e && e.type !== "token_delta" && e.type !== "reasoning_delta",
        ),
      },
    })),

  setConnection: (connection) => set((state) => ({ ...state, connection })),

  markOverflow: () => set((state) => ({ ...state, needsResync: true, resyncEpoch: state.resyncEpoch + 1 })),
  clearResync: () => set((state) => ({ ...state, needsResync: false })),
  requestResync: () => set((state) => ({ ...state, resyncEpoch: state.resyncEpoch + 1 })),

  beginAccept: (runId) =>
    set((state) => ({ ...state, acceptState: { ...state.acceptState, [runId]: { kind: "pending" } } })),

  acceptInProgress: (runId, attempt) =>
    set((state) => {
      // The outcome may already have arrived on the stream before the HTTP
      // answer did; a settled accept is never reopened.
      if (state.acceptState[runId]?.kind !== "pending") return state;
      // The stream may have delivered this attempt's settlement before the
      // answer named the attempt; it was held back then (no attempt known)
      // and is adopted now.
      const already = attempt
        ? [...(state.events[runId] ?? [])].reverse().find((e) => e.type === "accept_settled" && e.data?.attempt === attempt)
        : undefined;
      const settledNow = already ? settlementState(already.data) : null;
      const st: AcceptState = settledNow ?? (attempt ? { kind: "pending", verifying: true, attempt } : { kind: "pending", verifying: true });
      return { ...state, acceptState: { ...state.acceptState, [runId]: st } };
    }),

  // The HTTP answer never overwrites a settlement the stream already
  // delivered with a different one (review of #181): only a pending accept
  // — or none — takes it. A new accept starts with beginAccept.
  confirmAccept: (runId, sha) =>
    set((state) => (state.acceptState[runId]?.kind === "error" || state.acceptState[runId]?.kind === "committed" ? state : {
      ...state,
      acceptState: { ...state.acceptState, [runId]: { kind: "committed", sha } },
      runs: state.runs[runId]
        ? { ...state.runs, [runId]: { ...state.runs[runId]!, accepted: true, commit_sha: sha, status: "done" } }
        : state.runs,
    })),

  failAccept: (runId, message) =>
    set((state) => (state.acceptState[runId]?.kind === "committed" ? state : {
      ...state,
      acceptState: { ...state.acceptState, [runId]: { kind: "error", message } },
    })),

  reset: () =>
    set({
      runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {}, connection: "connecting",
      acceptState: {}, needsResync: false, resyncEpoch: 0,
      applyEvent: useRuns.getState().applyEvent,
      applyDeltaBatch: useRuns.getState().applyDeltaBatch,
      setRuns: useRuns.getState().setRuns,
      setRun: useRuns.getState().setRun,
      resyncRun: useRuns.getState().resyncRun,
      setConnection: useRuns.getState().setConnection,
      markOverflow: useRuns.getState().markOverflow,
      clearResync: useRuns.getState().clearResync,
      requestResync: useRuns.getState().requestResync,
      beginAccept: useRuns.getState().beginAccept,
      confirmAccept: useRuns.getState().confirmAccept,
      failAccept: useRuns.getState().failAccept,
      reset: useRuns.getState().reset,
    }),
}));

/** Runs waiting on a person, newest wait first — the human-gate inbox. */
const APPROVAL_EVENTS = new Set(["approval_requested", "approval_granted", "approval_declined", "approval_resolved"]);

function isApprovalRequest(v: unknown): v is ApprovalRequest {
  return !!v && typeof v === "object" && typeof (v as { id?: unknown }).id === "string";
}

/** Replaces the request with the same id, or appends a new one. */
export function upsertApproval(list: ApprovalRequest[] | undefined, req: ApprovalRequest): ApprovalRequest[] {
  const cur = list ?? [];
  return cur.some((r) => r.id === req.id) ? cur.map((r) => (r.id === req.id ? laterApproval(r, req) : r)) : [...cur, req];
}

/** Every request still waiting for the person, across runs, oldest first,
 * each with its run (B-530). */
export function pendingApprovals(runs: Record<string, Run>): { run: Run; request: ApprovalRequest }[] {
  const out: { run: Run; request: ApprovalRequest }[] = [];
  for (const run of Object.values(runs)) {
    for (const request of run.approval_requests ?? []) {
      if (request.status === "pending") out.push({ run, request });
    }
  }
  return out.sort((a, b) => (a.request.requested_at ?? "").localeCompare(b.request.requested_at ?? ""));
}

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
