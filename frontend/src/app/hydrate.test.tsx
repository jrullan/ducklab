import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, waitFor, act, screen } from "@testing-library/react";
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

  // B-495 (TI-36X, 2026-10-03): the list fetched while T-015's build was
  // running carried next=[abort]. The gate pause kept it, the hydrator saw a
  // run that "had" offers and skipped it, and Now's card offered only Abort
  // over a PASSED build — while GET /v1/runs/{id} already said accept/reject.
  it("replaces a running run's [abort] with the gate's offers on Now", async () => {
    let paused = false;
    const runRecord = () => ({
      id: "r-20261003-160620-w3xq", project_id: "p1", stage: "build", mode: "pair", task_id: "T-015",
      started_at: "2026-10-03T16:06:20Z",
      ...(paused
        ? { status: "paused", verdict: "PASSED", pending_kind: "gate", pending_since: "2026-10-03T16:20:00Z", next: ["accept", "reject"] }
        : { status: "running", verdict: "", next: ["abort"] }),
    });
    const fetchFn = vi.fn((url: string) => {
      const u = String(url);
      const json = (body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status: 200 }));
      if (u.includes("/v1/runs/r-20261003-160620-w3xq")) return json({ run: runRecord(), events: [] });
      if (u.includes("/v1/health")) return json({ version: "x" });
      if (u.endsWith("/v1/projects")) return json({ items: [{ id: "p1", name: "ti-36x-pro", path: "/p/ti" }] });
      if (u.includes("/v1/runs")) return json({ items: [runRecord()] });
      return json({ items: [] });
    });
    vi.stubGlobal("fetch", fetchFn as unknown as typeof fetch);

    render(<App />);
    await waitFor(() => expect(useRuns.getState().runs["r-20261003-160620-w3xq"]?.next).toEqual(["abort"]));

    paused = true;
    act(() => {
      useRuns.getState().applyEvent({
        type: "human_needed", run_id: "r-20261003-160620-w3xq", project_id: "p1", seq: 40,
        ts: "2026-10-03T16:20:00Z", data: { kind: "gate", verdict: "PASSED" },
      } as DucklabEvent);
    });

    expect(await screen.findByTestId("now-accept")).toBeTruthy();
    expect(screen.getByTestId("now-reject")).toBeTruthy();
    expect(screen.queryByTestId("now-abort")).toBeNull();
  });

  // The hydrator remembered runs, not pauses: a run that resumed and paused
  // again was never fetched again and kept the card it was given — or none.
  it("hydrates a run again when it pauses a second time", async () => {
    let detail: Record<string, unknown> = { status: "paused", pending_kind: "question", next: ["answer", "abort"] };
    const fetchFn = vi.fn((url: string) => {
      const u = String(url);
      const json = (body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status: 200 }));
      const base = { id: "r-9", project_id: "p1", stage: "build", mode: "pair", task_id: "T-010", verdict: "", started_at: "2026-08-06T11:55:00Z" };
      if (u.includes("/v1/runs/r-9")) return json({ run: { ...base, ...detail }, events: [] });
      if (u.includes("/v1/health")) return json({ version: "x" });
      if (u.includes("/v1/runs")) return json({ items: [{ ...base, status: "running" }] });
      return json({ items: [] });
    });
    vi.stubGlobal("fetch", fetchFn as unknown as typeof fetch);

    render(<App />);
    await waitFor(() => expect(useRuns.getState().runs["r-9"]?.status).toBe("running"));
    act(() => {
      useRuns.getState().applyEvent({ type: "human_needed", run_id: "r-9", seq: 1, data: { kind: "question" } } as DucklabEvent);
    });
    await waitFor(() => expect(useRuns.getState().runs["r-9"]?.next).toEqual(["answer", "abort"]));

    act(() => {
      useRuns.getState().applyEvent({ type: "human", run_id: "r-9", seq: 2, data: { answer: "src/a.go" } } as DucklabEvent);
    });
    expect(useRuns.getState().runs["r-9"]?.status).toBe("running");

    detail = { status: "paused", pending_kind: "gate", verdict: "PASSED", next: ["accept", "reject"] };
    act(() => {
      useRuns.getState().applyEvent({ type: "human_needed", run_id: "r-9", seq: 3, data: { kind: "gate" } } as DucklabEvent);
    });
    await waitFor(() => expect(useRuns.getState().runs["r-9"]?.next).toEqual(["accept", "reject"]));
    const runFetches = fetchFn.mock.calls.filter((c) => String(c[0]).includes("/v1/runs/r-9"));
    expect(runFetches.length).toBe(2);
  });
});
