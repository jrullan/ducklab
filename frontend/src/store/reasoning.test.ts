import { describe, it, expect, beforeEach } from "vitest";
import { useRuns } from "./runs";

const ev = (type: string, text: string, turn = 0) =>
  ({
    type,
    run_id: "r-1",
    data: { round: 1, turn, role: "implementer", duckling: "pato-deepseek", text },
  }) as never;

describe("streamed thinking", () => {
  beforeEach(() => {
    useRuns.setState({ deltas: {}, reasoning: {}, events: {} });
  });

  // The engine's parser dropped thinking before any event existed. Now that it
  // arrives, it must never land in the answer: the contract parser reads that
  // text, and a lane showing deliberation as the reply is a lie about what the
  // model said.
  it("accumulates apart from the answer", () => {
    const s = useRuns.getState();
    s.applyEvent(ev("reasoning_delta", "Let me check "));
    s.applyEvent(ev("reasoning_delta", "the inequality."));
    s.applyEvent(ev("token_delta", "Done."));

    expect(useRuns.getState().reasoning["r-1"]!["1:0"]).toBe("Let me check the inequality.");
    expect(useRuns.getState().deltas["r-1"]!["1:0"]).toBe("Done.");
  });

  // Keyed by turn, like deltas: a council takes two architect turns, and keyed
  // by duckling the second appended to the first and both lanes showed the
  // concatenation.
  it("keys thinking by turn, not by duckling", () => {
    const s = useRuns.getState();
    s.applyEvent(ev("reasoning_delta", "first turn", 0));
    s.applyEvent(ev("reasoning_delta", "second turn", 1));
    const r = useRuns.getState().reasoning["r-1"]!;
    expect(r["1:0"]).toBe("first turn");
    expect(r["1:1"]).toBe("second turn");
  });

  // Display state, like token_delta: the engine never persists it, so a resync
  // must not carry it into the event log.
  it("is dropped from the event log on resync", () => {
    const run = { id: "r-1" } as never;
    useRuns.getState().resyncRun(run, [
      ev("reasoning_delta", "thinking"),
      ev("token_delta", "answer"),
      { type: "run_start", run_id: "r-1", seq: 1, data: {} } as never,
    ]);
    const kinds = useRuns.getState().events["r-1"]!.map((e) => e.type);
    expect(kinds).toEqual(["run_start"]);
  });
});
