import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, waitFor, act } from "@testing-library/react";
import { App } from "./App";
import { useRuns } from "../store/runs";
import type { DucklabEvent } from "../api/events";

// A run that pauses while the person watches arrives by stream, and the
// stream carries only the transition — not the engine's next list, the
// verdict, or the spend. The decision card renders its buttons from `next`
// alone, so a build that reached its gate live showed a card with nothing to
// click; the person toured Now and the run view to shake the buttons loose.
describe("a pause seen live", () => {
  beforeEach(() => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
    window.ducklab = { baseUrl: "http://e1", token: "t1" };
    (window as unknown as { EventSource: unknown }).EventSource = class {
      onopen: unknown; onerror: unknown;
      addEventListener() {}
      close() {}
    };
  });
  afterEach(() => {
    delete window.ducklab;
    vi.unstubAllGlobals();
  });

  it("hydrates the paused run so its card has buttons and a cost", async () => {
    const fetchFn = vi.fn((url: string) => {
      const u = String(url);
      if (u.includes("/v1/runs/r-9")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              run: {
                id: "r-9", project_id: "p1", stage: "build", mode: "pair",
                task_id: "T-010", status: "paused", verdict: "PASSED",
                pending_kind: "gate", pending_since: "2026-08-06T12:00:00Z",
                started_at: "2026-08-06T11:55:00Z",
                next: ["accept", "reject"], budget: { usd: 0.42 },
              },
              events: [],
            }),
            { status: 200 },
          ),
        );
      }
      if (u.includes("/v1/health")) {
        return Promise.resolve(new Response(JSON.stringify({ version: "x" }), { status: 200 }));
      }
      if (u.includes("/v1/runs")) {
        // The startup list: the run is still going, so it carries no next.
        return Promise.resolve(
          new Response(
            JSON.stringify({
              items: [{
                id: "r-9", project_id: "p1", stage: "build", mode: "pair",
                task_id: "T-010", status: "running", verdict: "",
                started_at: "2026-08-06T11:55:00Z",
              }],
            }),
            { status: 200 },
          ),
        );
      }
      return Promise.resolve(new Response(JSON.stringify({ items: [] }), { status: 200 }));
    });
    vi.stubGlobal("fetch", fetchFn as unknown as typeof fetch);

    render(<App />);
    await waitFor(() => expect(useRuns.getState().runs["r-9"]?.status).toBe("running"));

    // The gate pause arrives on the stream: a transition, nothing more.
    act(() => {
      useRuns.getState().applyEvent({
        type: "human_needed", run_id: "r-9", seq: 7,
        ts: "2026-08-06T12:00:00Z", data: { kind: "gate" },
      } as DucklabEvent);
    });

    // The pause alone triggers the fetch; no view needs visiting.
    await waitFor(() => {
      const r = useRuns.getState().runs["r-9"];
      expect(r?.next).toEqual(["accept", "reject"]);
      expect(r?.budget?.usd).toBe(0.42);
    });
    const runFetches = fetchFn.mock.calls.filter((c) => String(c[0]).includes("/v1/runs/r-9"));
    expect(runFetches.length).toBe(1);
  });
});
