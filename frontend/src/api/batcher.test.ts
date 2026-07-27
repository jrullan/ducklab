import { describe, it, expect, vi } from "vitest";
import { DeltaBatcher, mergeDeltas } from "./batcher";
import type { DucklabEvent } from "./events";

const delta = (duckling: string, text: string, run = "r-1", round = 1, turn = 0): DucklabEvent =>
  ({ type: "token_delta", run_id: run, data: { duckling, text, round, turn } });

describe("DeltaBatcher", () => {
  // AC-33: one store update per frame, not one per token.
  it("coalesces many deltas into a single flush per frame", () => {
    const flush = vi.fn();
    let frame: (() => void) | null = null;
    const b = new DeltaBatcher(flush, (fn) => { frame = fn; });

    for (let i = 0; i < 40; i++) b.push(delta("pato-uno", `t${i}`));
    expect(flush).not.toHaveBeenCalled();
    expect(b.pendingCount).toBe(40);

    frame!();
    expect(flush).toHaveBeenCalledOnce();
    expect(flush.mock.calls[0]![0]).toHaveLength(40);
  });

  // Persisted events change run state; delaying them would make the UI lag
  // the engine's own record.
  it("passes non-delta events straight through", () => {
    const b = new DeltaBatcher(vi.fn(), () => {});
    expect(b.push({ type: "turn_start", run_id: "r-1", seq: 1 })).toBe(false);
    expect(b.push({ type: "gate", run_id: "r-1", seq: 2 })).toBe(false);
    expect(b.push(delta("a", "x"))).toBe(true);
  });

  it("schedules exactly one frame per burst", () => {
    const schedule = vi.fn();
    const b = new DeltaBatcher(vi.fn(), schedule);
    for (let i = 0; i < 10; i++) b.push(delta("a", "x"));
    expect(schedule).toHaveBeenCalledOnce();
  });

  it("schedules again after a flush", () => {
    const schedule = vi.fn();
    let frame: (() => void) | null = null;
    const b = new DeltaBatcher(vi.fn(), (fn) => { schedule(); frame = fn; });

    b.push(delta("a", "x"));
    frame!();
    b.push(delta("a", "y"));
    expect(schedule).toHaveBeenCalledTimes(2);
  });

  it("drains safely with nothing pending", () => {
    const flush = vi.fn();
    new DeltaBatcher(flush, () => {}).drain();
    expect(flush).not.toHaveBeenCalled();
  });
});

describe("mergeDeltas", () => {
  it("concatenates per run and per turn", () => {
    const merged = mergeDeltas([
      delta("pato-uno", "func ", "r-1", 1, 0),
      delta("pato-dos", "{", "r-1", 1, 1),
      delta("pato-uno", "Add", "r-1", 1, 0),
    ]);
    expect(merged.get("r-1")!.get("1:0")).toBe("func Add");
    expect(merged.get("r-1")!.get("1:1")).toBe("{");
  });

  // A council takes two architect turns. Keyed by duckling, the second one
  // appended to the first and both lanes showed the concatenation.
  it("keeps two turns by the same duckling apart", () => {
    const merged = mergeDeltas([
      delta("pato-uno", "draft", "r-1", 1, 0),
      delta("pato-uno", "revision", "r-1", 1, 3),
    ]);
    expect(merged.get("r-1")!.get("1:0")).toBe("draft");
    expect(merged.get("r-1")!.get("1:3")).toBe("revision");
  });

  it("keeps runs separate", () => {
    const merged = mergeDeltas([delta("a", "x", "r-1"), delta("a", "y", "r-2")]);
    expect(merged.get("r-1")!.get("1:0")).toBe("x");
    expect(merged.get("r-2")!.get("1:0")).toBe("y");
  });

  it("drops a delta with no run id rather than inventing one", () => {
    expect(mergeDeltas([{ type: "token_delta", data: { duckling: "a", text: "x" } }]).size).toBe(0);
  });
});
