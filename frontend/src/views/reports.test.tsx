import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Reports } from "./Reports";
import type { EngineClient } from "../api/client";

const row = (o: Partial<Record<string, unknown>> & { key: string }) => ({
  runs: 0, passed: 0, unverified: 0, failed: 0,
  tokens: 0, cost_usd: 0, wallclock_ms: 0, estimated: false, ...o,
});

const clientWith = (modes: unknown[], deltas: unknown[], ducklings: unknown[] = []) =>
  ({
    report: vi.fn((_p: string, by: string) =>
      Promise.resolve(
        by === "mode"
          ? { by, baseline: "solo", rows: modes, deltas, rendered: "" }
          : { by, baseline: "solo", rows: ducklings, deltas: [], rendered: "" },
      ),
    ),
  }) as unknown as EngineClient;

describe("Reports", () => {
  // The headline is the question the project exists to answer.
  it("leads with the delta against solo, not a chart", async () => {
    const client = clientWith(
      [row({ key: "solo", runs: 10, passed: 6, failed: 4 }), row({ key: "pair", runs: 5, passed: 5 })],
      [{ key: "pair", pass_rate: 100, points_vs_baseline: 40, n: 5 }],
    );
    render(<Reports client={client} projectId="p" />);
    const hero = await screen.findByTestId("hero-number");
    expect(hero.textContent).toBe("+40.0 pts");
    expect(screen.getByTestId("hero").textContent).toContain("n = 5 runs");
  });

  // A number with nothing behind it is worse than saying there is nothing yet.
  it("says why there is no comparison when solo has not run", async () => {
    const client = clientWith([row({ key: "pair", runs: 3, passed: 3 })], []);
    render(<Reports client={client} projectId="p" />);
    const hero = await screen.findByTestId("hero");
    expect(hero.textContent).toContain("No solo runs yet");
    expect(screen.queryByTestId("hero-number")).toBeNull();
  });

  it("says so when only solo has run", async () => {
    const client = clientWith([row({ key: "solo", runs: 4, passed: 3, failed: 1 })], []);
    render(<Reports client={client} projectId="p" />);
    expect((await screen.findByTestId("hero")).textContent).toContain("Only solo has run");
  });

  it("draws the baseline on the pass-rate chart", async () => {
    const client = clientWith(
      [row({ key: "solo", runs: 10, passed: 6, failed: 4 }), row({ key: "pair", runs: 5, passed: 5 })],
      [{ key: "pair", pass_rate: 100, points_vs_baseline: 40, n: 5 }],
    );
    render(<Reports client={client} projectId="p" />);
    await screen.findByTestId("hero-number");
    expect(screen.getByTestId("baseline")).toBeTruthy();
    expect(screen.getByTestId("bar-pair")).toBeTruthy();
  });

  // Every chart carries the numbers behind it (08 §4.7).
  it("swaps any chart for the same data as a table", async () => {
    const client = clientWith(
      [row({ key: "solo", runs: 10, passed: 6, unverified: 1, failed: 3 })],
      [],
    );
    render(<Reports client={client} projectId="p" />);
    await screen.findByTestId("outcome-mix");

    const toggles = screen.getAllByTestId("table-toggle");
    fireEvent.click(toggles[0]!);
    await waitFor(() => expect(screen.queryByTestId("outcome-mix")).toBeNull());
    expect(toggles[0]!.getAttribute("aria-pressed")).toBe("true");
  });

  // Estimated counts are never presented as measured (04 §7).
  it("marks an estimated token count", async () => {
    const client = clientWith(
      [row({ key: "solo", runs: 2, passed: 2 })],
      [],
      [
        row({ key: "pato-atom", runs: 2, passed: 2, tokens: 1000, estimated: true }),
        row({ key: "pato-local", runs: 2, passed: 1, failed: 1, tokens: 2000 }),
      ],
    );
    render(<Reports client={client} projectId="p" />);
    const estimated = await screen.findByTestId("duckling-row-pato-atom");
    expect(estimated.textContent).toContain("~");
    expect(screen.getByTestId("duckling-row-pato-local").textContent).not.toContain("~");
  });

  it("asks the engine for a narrower window when a range is picked", async () => {
    const client = clientWith([row({ key: "solo", runs: 1, passed: 1 })], []);
    render(<Reports client={client} projectId="p" />);
    await screen.findByTestId("hero");
    fireEvent.click(screen.getByTestId("range-30d"));
    await waitFor(() =>
      expect(client.report).toHaveBeenCalledWith("p", "mode", "30d"),
    );
  });

  it("says there is nothing to measure rather than drawing empty charts", async () => {
    render(<Reports client={clientWith([], [])} projectId="p" />);
    expect((await screen.findByText(/nothing to measure/)).textContent).toBeTruthy();
  });
});
