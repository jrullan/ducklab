import { describe, it, expect, vi } from "vitest";
import {
  EventSubscriber,
  backoffDelay,
  streamUrl,
  type EventSourceLike,
} from "./events";

/** A controllable EventSource stand-in. */
class FakeSource implements EventSourceLike {
  listeners = new Map<string, ((e: MessageEvent) => void)[]>();
  onerror: ((e: unknown) => void) | null = null;
  onopen: ((e: unknown) => void) | null = null;
  closed = false;
  constructor(public url: string) {}

  addEventListener(type: string, fn: (e: MessageEvent) => void) {
    const list = this.listeners.get(type) ?? [];
    list.push(fn);
    this.listeners.set(type, list);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, payload: unknown) {
    for (const fn of this.listeners.get(type) ?? []) {
      fn({ data: JSON.stringify(payload) } as MessageEvent);
    }
  }
  emitRaw(type: string, data: string) {
    for (const fn of this.listeners.get(type) ?? []) {
      fn({ data } as MessageEvent);
    }
  }
}

function harness(opts: Partial<Parameters<typeof makeSub>[0]> = {}) {
  return makeSub(opts);
}

function makeSub(opts: {
  onEvent?: (e: any) => void;
  onOverflow?: () => void;
  onState?: (s: string) => void;
  fromSeq?: number;
}) {
  const sources: FakeSource[] = [];
  const timers: (() => void)[] = [];
  const events: any[] = [];
  const states: string[] = [];

  const sub = new EventSubscriber({
    baseUrl: "http://127.0.0.1:1234",
    token: "t",
    runId: "r-1",
    fromSeq: opts.fromSeq,
    onEvent: opts.onEvent ?? ((e) => events.push(e)),
    onState: opts.onState ?? ((s) => states.push(s)),
    onOverflow: opts.onOverflow,
    eventSourceFactory: (url) => {
      const s = new FakeSource(url);
      sources.push(s);
      return s;
    },
    setTimeoutFn: (fn) => {
      timers.push(fn);
      return timers.length;
    },
    clearTimeoutFn: () => {},
    random: () => 0.5,
  });
  return { sub, sources, timers, events, states };
}

describe("backoffDelay", () => {
  it("grows exponentially and caps at 8s", () => {
    const noJitter = () => 0.5;
    expect(backoffDelay(0, noJitter)).toBe(500);
    expect(backoffDelay(1, noJitter)).toBe(1000);
    expect(backoffDelay(4, noJitter)).toBe(8000);
    expect(backoffDelay(20, noJitter)).toBe(8000);
  });

  it("applies jitter so clients do not reconnect in lockstep", () => {
    const low = backoffDelay(3, () => 0);
    const high = backoffDelay(3, () => 1);
    expect(low).toBeLessThan(high);
    expect(low).toBeGreaterThanOrEqual(0);
  });
});

describe("streamUrl", () => {
  it("carries run, from_seq and token", () => {
    const url = streamUrl({ baseUrl: "http://x", token: "tok", runId: "r-1", fromSeq: 7 });
    expect(url).toContain("run=r-1");
    expect(url).toContain("from_seq=7");
    expect(url).toContain("token=tok");
  });
});

describe("EventSubscriber", () => {
  it("delivers persisted events and tracks the resume point", () => {
    const h = harness();
    h.sub.start();
    const src = h.sources[0]!;
    src.onopen?.(null);

    src.emit("turn_start", { type: "turn_start", run_id: "r-1", seq: 1 });
    src.emit("gate", { type: "gate", run_id: "r-1", seq: 2 });

    expect(h.events).toHaveLength(2);
    expect(h.sub.resumeFrom).toBe(2);
  });

  it("replays advice_started so an advisor drafting indicator can open live", () => {
    const h = harness();
    h.sub.start();
    const src = h.sources[0]!;

    src.emit("advice_started", {
      type: "advice_started", run_id: "r-1", seq: 7,
      data: { question_id: "q1", advisor: "pato-advisor" },
    });

    expect(h.events).toEqual([expect.objectContaining({ type: "advice_started", seq: 7 })]);
    expect(h.sub.resumeFrom).toBe(7);
  });

  // token_delta has no seq. If it moved the resume point, a reconnect would
  // skip real events.
  it("does not let token_delta advance the resume point", () => {
    const h = harness();
    h.sub.start();
    const src = h.sources[0]!;

    src.emit("turn_start", { type: "turn_start", seq: 5 });
    src.emit("token_delta", { type: "token_delta", data: { text: "hi" } });
    src.emit("heartbeat", { type: "heartbeat" });

    expect(h.sub.resumeFrom).toBe(5);
    expect(h.events.map((e) => e.type)).toEqual(["turn_start", "token_delta", "heartbeat"]);
  });

  it("reconnects from the last seq, losing nothing", () => {
    const h = harness();
    h.sub.start();
    const first = h.sources[0]!;
    first.emit("turn_start", { type: "turn_start", seq: 9 });

    first.onerror?.(new Error("dropped"));
    expect(first.closed).toBe(true);
    expect(h.timers).toHaveLength(1);

    h.timers[0]!(); // fire the backoff timer
    const second = h.sources[1]!;
    expect(second.url).toContain("from_seq=9");
  });

  // AC-30: a dropped connection changes the indicator, never the data.
  it("reports reconnecting without emitting any clearing event", () => {
    const h = harness();
    h.sub.start();
    const src = h.sources[0]!;
    src.onopen?.(null);
    src.emit("turn_start", { type: "turn_start", seq: 1 });

    const before = h.events.length;
    src.onerror?.(new Error("dropped"));

    expect(h.states).toContain("reconnecting");
    expect(h.events).toHaveLength(before);
  });

  it("treats overflow as a resync instruction, not an event", () => {
    const onOverflow = vi.fn();
    const h = makeSub({ onOverflow });
    h.sub.start();
    h.sources[0]!.emit("overflow", { type: "overflow" });

    expect(onOverflow).toHaveBeenCalledOnce();
    expect(h.events).toHaveLength(0);
    expect(h.sub.resumeFrom).toBe(0);
  });

  it("drops a malformed frame instead of guessing", () => {
    const h = harness();
    h.sub.start();
    h.sources[0]!.emitRaw("turn_start", "{not json");
    expect(h.events).toHaveLength(0);
  });

  it("stops cleanly and does not reconnect after stop", () => {
    const h = harness();
    h.sub.start();
    const src = h.sources[0]!;
    h.sub.stop();

    expect(src.closed).toBe(true);
    src.onerror?.(new Error("late"));
    expect(h.timers).toHaveLength(0);
    expect(h.states.at(-1)).toBe("closed");
  });

  it("resets backoff after a successful reconnect", () => {
    const h = harness();
    h.sub.start();

    h.sources[0]!.onerror?.(new Error("x"));
    h.timers[0]!();
    h.sources[1]!.onopen?.(null);
    h.sources[1]!.onerror?.(new Error("y"));

    // Two failures, but the second started from attempt 0 again.
    expect(h.sources).toHaveLength(2);
    expect(h.timers).toHaveLength(2);
  });

  it("starts from an explicit fromSeq", () => {
    const h = makeSub({ fromSeq: 42 });
    h.sub.start();
    expect(h.sources[0]!.url).toContain("from_seq=42");
    expect(h.sub.resumeFrom).toBe(42);
  });
});
