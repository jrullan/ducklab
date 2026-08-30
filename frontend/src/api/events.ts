/**
 * SSE subscriber for the engine's event stream.
 *
 * Three behaviours it must get right, each of which has bitten the engine
 * side already:
 *
 *  - resume with Last-Event-ID so a dropped connection loses nothing;
 *  - treat `overflow` as "refetch the snapshot", not as an error;
 *  - never blank the UI on a drop — the caller keeps its last known state and
 *    only the connection indicator changes (AC-30).
 */

export interface DucklabEvent {
  type: string;
  run_id?: string;
  project_id?: string;
  seq?: number;
  /** RFC3339, as the engine stamps it. */
  ts?: string;
  data?: Record<string, unknown>;
}

export type ConnectionState = "connecting" | "open" | "reconnecting" | "closed";

export interface SubscriberOptions {
  baseUrl: string;
  token: string;
  runId?: string;
  projectId?: string;
  fromSeq?: number;
  onEvent: (e: DucklabEvent) => void;
  onState?: (s: ConnectionState) => void;
  /** Called when the server drops us for overflow; the caller must resync. */
  onOverflow?: () => void;
  /** Called after a reconnect succeeds, so callers can refresh snapshots that
   * may have changed while the stream was unavailable. */
  onReconnect?: () => void;
  /** How long a quiet stream may remain open before it is considered stale. */
  staleAfterMs?: number;
  /** Injected for tests. Defaults to the global EventSource. */
  eventSourceFactory?: (url: string) => EventSourceLike;
  /** Injected for tests so backoff does not make suites slow. */
  setTimeoutFn?: (fn: () => void, ms: number) => unknown;
  clearTimeoutFn?: (handle: unknown) => void;
  /** Injected for tests; defaults to Math.random. */
  random?: () => number;
}

/** The slice of EventSource this subscriber uses. */
export interface EventSourceLike {
  addEventListener(type: string, fn: (e: MessageEvent) => void): void;
  close(): void;
  onerror: ((e: unknown) => void) | null;
  onopen: ((e: unknown) => void) | null;
}

const BASE_DELAY_MS = 500;
const MAX_DELAY_MS = 8000;
const DEFAULT_STALE_AFTER_MS = 30_000;

/** Exponential backoff with jitter, capped (07 §7.2). */
export function backoffDelay(attempt: number, random: () => number = Math.random): number {
  const raw = Math.min(BASE_DELAY_MS * 2 ** attempt, MAX_DELAY_MS);
  // ±20% jitter, so many clients reconnecting after an engine restart do not
  // arrive in lockstep.
  const jitter = raw * 0.2 * (random() * 2 - 1);
  return Math.max(0, Math.round(raw + jitter));
}

/** Builds the stream URL. The token rides in the query because EventSource
 * cannot set headers; the engine is loopback-only and the token is per-start. */
export function streamUrl(o: {
  baseUrl: string;
  token: string;
  runId?: string;
  projectId?: string;
  fromSeq?: number;
}): string {
  const params = new URLSearchParams();
  if (o.runId) params.set("run", o.runId);
  if (o.projectId) params.set("project", o.projectId);
  params.set("from_seq", String(o.fromSeq ?? 0));
  params.set("token", o.token);
  return `${o.baseUrl}/v1/events?${params.toString()}`;
}

export class EventSubscriber {
  private opts: SubscriberOptions;
  private source: EventSourceLike | null = null;
  private lastSeq: number;
  private attempt = 0;
  private stopped = false;
  private timer: unknown = null;
  private staleTimer: unknown = null;
  private streamOpen = false;

  constructor(opts: SubscriberOptions) {
    this.opts = opts;
    this.lastSeq = opts.fromSeq ?? 0;
  }

  /** Highest persisted seq seen, which is where a reconnect resumes from. */
  get resumeFrom(): number {
    return this.lastSeq;
  }

  start(): void {
    this.stopped = false;
    this.connect();
  }

  stop(): void {
    this.stopped = true;
    this.clearTimer();
    this.clearStaleTimer();
    this.source?.close();
    this.source = null;
    this.streamOpen = false;
    this.opts.onState?.("closed");
  }

  private clearTimer(): void {
    if (this.timer !== null) {
      (this.opts.clearTimeoutFn ?? clearTimeout)(this.timer as never);
      this.timer = null;
    }
  }

  private clearStaleTimer(): void {
    if (this.staleTimer !== null) {
      (this.opts.clearTimeoutFn ?? clearTimeout)(this.staleTimer as never);
      this.staleTimer = null;
    }
  }

  private armStaleTimer(): void {
    this.clearStaleTimer();
    // Keep the low-level subscriber compatible with callers that only want
    // EventSource's native reconnect; the desktop opts into liveness polling.
    if (this.opts.staleAfterMs === undefined) return;
    const timeout = this.opts.staleAfterMs ?? DEFAULT_STALE_AFTER_MS;
    this.staleTimer = (this.opts.setTimeoutFn ?? setTimeout)(() => {
      if (this.stopped || !this.source) return;
      // EventSource can remain open after its underlying connection has died.
      // Close it ourselves so its normal bounded reconnect path is used.
      this.source.close();
      this.source = null;
      this.streamOpen = false;
      this.opts.onState?.("reconnecting");
      const delay = backoffDelay(this.attempt, this.opts.random);
      this.attempt += 1;
      this.timer = (this.opts.setTimeoutFn ?? setTimeout)(() => this.connect(), delay);
    }, timeout);
  }

  private connect(): void {
    if (this.stopped) return;
    this.opts.onState?.(this.attempt === 0 ? "connecting" : "reconnecting");

    const url = streamUrl({ ...this.opts, fromSeq: this.lastSeq });
    const factory =
      this.opts.eventSourceFactory ??
      ((u: string) => new EventSource(u) as unknown as EventSourceLike);
    const src = factory(url);
    this.source = src;
    this.streamOpen = false;

    src.onopen = () => {
      const wasReconnect = this.attempt > 0;
      this.attempt = 0;
      this.streamOpen = true;
      this.opts.onState?.("open");
      this.armStaleTimer();
      if (wasReconnect) this.opts.onReconnect?.();
    };

    src.addEventListener("message", (e) => this.handle(e));
    for (const type of KNOWN_EVENT_TYPES) {
      src.addEventListener(type, (e) => this.handle(e));
    }

    src.onerror = () => {
      if (this.stopped) return;
      this.clearStaleTimer();
      src.close();
      this.source = null;
      this.streamOpen = false;
      // Do NOT clear caller state: the UI keeps rendering what it has and
      // only the indicator changes (AC-30).
      this.opts.onState?.("reconnecting");
      const delay = backoffDelay(this.attempt, this.opts.random);
      this.attempt += 1;
      this.timer = (this.opts.setTimeoutFn ?? setTimeout)(() => this.connect(), delay);
    };
  }

  private handle(e: MessageEvent): void {
    let parsed: DucklabEvent;
    try {
      parsed = JSON.parse(e.data as string) as DucklabEvent;
    } catch {
      return; // a malformed frame is dropped, never guessed at
    }

    // Heartbeats and all other valid frames prove the socket is alive. Reset
    // the watchdog before dispatching so a quiet period is measured from the
    // last event, not merely from connection establishment.
    if (this.streamOpen) this.armStaleTimer();

    if (parsed.type === "overflow") {
      // The server dropped us because we fell behind. This is a resync
      // instruction, not an error: refetch the snapshot and carry on.
      this.opts.onOverflow?.();
      return;
    }

    // Only persisted events advance the resume point. token_delta,
    // reasoning_delta and
    // heartbeat carry no seq and must not move it, or a reconnect would skip
    // real events.
    if (typeof parsed.seq === "number" && parsed.seq > this.lastSeq) {
      this.lastSeq = parsed.seq;
    }
    this.opts.onEvent(parsed);
  }
}

/** Event types the engine emits.
 *
 * EventSource dispatches by name, so anything missing here is dropped in
 * silence — the stream carries it and no listener ever runs. Three types added
 * to the engine in one afternoon (message, round_gate, gate_resolved) were
 * invisible to the desktop until this list caught up.
 *
 * `message` also arrives through the generic "message" listener, since that is
 * SSE's default event name; the rest have no such safety net. When you add an
 * event to the engine, add it here in the same change. */
export const KNOWN_EVENT_TYPES = [
  "run_start",
  "run_end",
  "run_queued",
  "run_started",
  "restart_request",
  "restart_abandoned",
  "recovery",
  "remote_warning",
  "round_start",
  "turn_start",
  "turn_end",
  "turn_interrupted",
  "llm_call",
  "tool_call",
  "tool_call_started",
  "policy_violation",
  // Provider weather as it happens: a transient failure per attempt, so a
  // stalled upstream reads as "retrying (2)" instead of as death.
  "provider_retry",
  "repetition_loop",
  "gate",
  "round_gate",
  "gate_started",
  "reply_call",
  "seat_failover",
  "gate_resolved",
  "gate_reproduced",
  "render",
  // An accept whose commit did not reproduce from a clean checkout takes
  // the commit back; the diff stays in the tree, uncommitted (B-069).
  "commit_withdrawn",
  // Which reference documents a stage loaded, and what its caps dropped.
  "references_loaded",
  "reference_digested",
  "sections_folded",
  "message",
  "proposal",
  "verdict",
  "candidate",
  "escalation_suggestion",
  "distress_evidence",
  "triage_failed",
  "triage",
  "triage_applied",
  "bug_fixed",
  "tdd_build_started",
  "tree_restored",
  // Emitted by modes and the gate. Registered even where nothing renders them
  // yet: an event the desktop does not know cannot be stored or replayed, and
  // the run log a client rebuilds would be missing what actually happened.
  "settled",
  "structure_check",
  "revision_identical",
  "pause_requested",
  "dedupe",
  "no_gate",
  "toolchain_missing",
  "draft_carried",
  "contestant_failed",
  "no_changes",
  "tests_modified",
  "test_retired",
  "budget_lifted",
  "findings_filed",
  "sections_removed",
  "survey_inventory",
  "survey_coverage",
  "sections_gutted",
  "task_history_rewritten",
  "task_body_amendment",
  "advice_started",
  "advice",
  "advice_taken",
  "advice_failed",
  // The rubber duck: the positioned advisor consult and its stop verdict.
  "advisor_consult",
  "advisor_stop",
  "advisor_retry",
  // The deliverables checklist: the implementer's report, and an approve
  // over items it reported undelivered.
  "deliverables_report",
  "deliverables_gap",
  "skill_problems",
  "release_drafted",
  "pr_body_draft",
  "pr_body_drafted",
  "pr_body_fallback",
  "review_written",
  "split_result",
  "seam_round",
  "integrated",
  "decomposition_rejected",
  "decomposition",
  "resolution",
  "human",
  // An advisor auto-answer is an operator-facing correction opportunity.
  "notification",
  "human_needed",
  "checkpoint",
  "warning",
  "error",
  "budget",
  "token_delta",
  "reasoning_delta",
  "budget",
  "heartbeat",
  "overflow",
  "engine_recovered",
  "config_amendment",
] as const;
