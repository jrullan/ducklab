import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Bench } from "./Bench";
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

  // The average says which model is expensive to run once; the total says
  // where the project's money went. A cheap model called constantly can
  // out-spend an expensive one used sparingly, and neither column shows that
  // alone.
  it("shows each duckling's total cost beside its average", async () => {
    const client = clientWith(
      [row({ key: "solo", runs: 2, passed: 2 })],
      [],
      [row({ key: "pato-sonnet", runs: 4, passed: 4, cost_usd: 0.9483 })],
    );
    render(<Reports client={client} projectId="p" />);
    const total = await screen.findByTestId("duckling-total-pato-sonnet");
    expect(total.textContent).toBe("$0.9483");
    // And the same row's average is the total over its runs.
    expect(screen.getByTestId("duckling-row-pato-sonnet").textContent).toContain("$0.2371");
  });

  // "Sort by total cost" is the question the column exists for: who is the
  // project's biggest spender. Numbers open descending, the name ascending,
  // and a second click flips the order.
  it("sorts the duckling table by a clicked column, and flips on a second click", async () => {
    const client = clientWith(
      [row({ key: "solo", runs: 2, passed: 2 })],
      [],
      [
        row({ key: "pato-atom", runs: 10, passed: 9, failed: 1, cost_usd: 0.9 }),
        row({ key: "pato-luna", runs: 2, passed: 2, cost_usd: 0.01 }),
        row({ key: "pato-sonnet", runs: 3, passed: 3, cost_usd: 0.5 }),
      ],
    );
    render(<Reports client={client} projectId="p" />);
    await screen.findByTestId("duckling-table");

    const order = () =>
      Array.from(document.querySelectorAll("[data-testid^='duckling-row-']")).map((el) =>
        el.getAttribute("data-testid")!.replace("duckling-row-", ""),
      );
    // Engine order (alphabetical) until a column is chosen.
    expect(order()).toEqual(["pato-atom", "pato-luna", "pato-sonnet"]);

    fireEvent.click(screen.getByTestId("duckling-sort-total-cost"));
    expect(order()).toEqual(["pato-atom", "pato-sonnet", "pato-luna"]);

    fireEvent.click(screen.getByTestId("duckling-sort-total-cost"));
    expect(order()).toEqual(["pato-luna", "pato-sonnet", "pato-atom"]);

    // The name column opens ascending instead.
    fireEvent.click(screen.getByTestId("duckling-sort-duckling"));
    expect(order()).toEqual(["pato-atom", "pato-luna", "pato-sonnet"]);
  });

  // Every other number on the page is relative — points, rates, averages.
  // The one absolute a person budgeting needs is what the project has cost.
  it("says what the project has cost in the selected window", async () => {
    const client = clientWith(
      [
        row({ key: "solo", runs: 3, passed: 3, cost_usd: 1.25 }),
        row({ key: "pair", runs: 2, passed: 2, cost_usd: 0.5 }),
      ],
      [],
    );
    render(<Reports client={client} projectId="p" />);
    const cost = await screen.findByTestId("project-cost");
    expect(cost.textContent).toContain("$1.75");
    expect(cost.textContent).toContain("5 finished runs");
    // An estimated row marks the total as an estimate, never as measured.
    expect(cost.textContent).not.toContain("~");
  });

  // Badges over the table: who wins each question it can answer. And they
  // stay away entirely until two models have enough runs to compete.
  it("crowns the leaderboard from the duckling rows", async () => {
    const client = clientWith(
      [row({ key: "solo", runs: 5, passed: 5 })],
      [],
      [
        row({ key: "luna", runs: 10, passed: 9, failed: 1, tokens: 900000, cost_usd: 0.05, wallclock_ms: 3600000 }),
        row({ key: "pato-sonnet", runs: 5, passed: 4, failed: 1, tokens: 300000, cost_usd: 2.0, wallclock_ms: 600000 }),
      ],
    );
    render(<Reports client={client} projectId="p" />);
    const board = await screen.findByTestId("leaderboard");
    expect(screen.getByTestId("award-performant").textContent).toContain("luna");
    expect(screen.getByTestId("award-performant").textContent).toContain("90% passed");
    expect(screen.getByTestId("award-fastest").textContent).toContain("pato-sonnet");
    expect(board.textContent).toContain("n=");
  });

  it("shows no leaderboard while only one model has real history", async () => {
    const client = clientWith(
      [row({ key: "solo", runs: 5, passed: 5 })],
      [],
      [row({ key: "luna", runs: 10, passed: 9 })],
    );
    render(<Reports client={client} projectId="p" />);
    await screen.findByTestId("duckling-table");
    expect(screen.queryByTestId("leaderboard")).toBeNull();
  });

  // Pick two models, get the verdict — and the metric rows to check it
  // against, because a summary nobody can audit is an opinion.
  it("compares two chosen models head to head", async () => {
    const client = clientWith(
      [row({ key: "solo", runs: 5, passed: 5 })],
      [],
      [
        row({ key: "luna", runs: 10, passed: 9, failed: 1, tokens: 900000, cost_usd: 0.05, wallclock_ms: 3600000 }),
        row({ key: "pato-sonnet", runs: 5, passed: 4, failed: 1, tokens: 300000, cost_usd: 2.0, wallclock_ms: 600000 }),
      ],
    );
    render(<Reports client={client} projectId="p" />);
    await screen.findByTestId("head-to-head");
    fireEvent.change(screen.getByTestId("compare-a"), { target: { value: "luna" } });
    fireEvent.change(screen.getByTestId("compare-b"), { target: { value: "pato-sonnet" } });

    expect(screen.getByTestId("compare-summary").textContent).toBe(
      "luna is both more effective and more efficient on this history.",
    );
    // The winner of each row is marked where the reader can check the claim.
    expect(screen.getByTestId("compare-pass-rate").textContent).toContain("✓");
    expect(screen.getByTestId("compare-table").textContent).toContain("90%");
    // Enough runs on both sides: no thin-evidence warning.
    expect(screen.queryByTestId("compare-thin")).toBeNull();
  });

  it("warns when one side's history is an anecdote", async () => {
    const client = clientWith(
      [row({ key: "solo", runs: 5, passed: 5 })],
      [],
      [
        row({ key: "luna", runs: 10, passed: 9, tokens: 900000 }),
        row({ key: "fresh", runs: 2, passed: 2, tokens: 100000 }),
      ],
    );
    render(<Reports client={client} projectId="p" />);
    await screen.findByTestId("head-to-head");
    fireEvent.change(screen.getByTestId("compare-a"), { target: { value: "luna" } });
    fireEvent.change(screen.getByTestId("compare-b"), { target: { value: "fresh" } });
    expect(screen.getByTestId("compare-thin").textContent).toContain("anecdote");
  });

  // The report's destination is an email or a wiki page, not this window:
  // built on demand, rendered readable, copyable as Markdown.
  it("builds and shows the development report on demand", async () => {
    const client = clientWith([row({ key: "solo", runs: 1, passed: 1 })], []);
    (client as unknown as { traceReport: unknown }).traceReport = vi.fn(() =>
      Promise.resolve({ rendered: "# calc — development report\n\n## The software\n\nProse here." }),
    );
    render(<Reports client={client} projectId="p" />);
    fireEvent.click(await screen.findByTestId("dev-report-generate"));
    await waitFor(() => expect(screen.getByTestId("dev-report-body")).toBeTruthy());
    expect(screen.getByTestId("dev-report-body").textContent).toContain("Prose here.");
    expect(screen.getByTestId("dev-report-copy")).toBeTruthy();
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

describe("Reports — Bench tab", () => {
  const cell = (o: Partial<Record<string, unknown>> & { task: string }) => ({
    duckling: "pato-atom", mode: "solo", run_id: "r", verdict: "PASSED",
    tokens: 1000, estimated: false, cost_usd: 0, wallclock_ms: 60000, ...o,
  });

  const benchClient = (cells: unknown[]) =>
    ({
      report: vi.fn(() => Promise.resolve({ by: "mode", baseline: "solo", rows: [], deltas: [], rendered: "" })),
      ducklings: vi.fn(() => Promise.resolve([{ id: "luna", provider: "openrouter", model: "l" }])),
      benchStart: vi.fn(() => Promise.resolve({ started: true, suite: "std", cells: 9 })),
      runs: vi.fn(() => Promise.resolve([])),
      benchList: vi.fn(() =>
        Promise.resolve([
          { suite: "std", suite_version: 2, started_at: "", stamp: "20260729-130000", cells: cells.length, passed: cells.length, errors: 0 },
        ]),
      ),
      benchGet: vi.fn(() =>
        Promise.resolve({
          result: { suite: "std", suite_version: 2, started_at: "", ducklings: ["pato-atom", "pato-local"], modes: ["solo"], cells },
          rendered: "",
        }),
      ),
    }) as unknown as EngineClient;

  // A benchmark everyone passes compares as little as one nobody passes. A
  // wall of 100% reads as a triumph unless the page says otherwise — which is
  // exactly what the first real bench looked like.
  it("says when a suite stopped telling ducklings apart", async () => {
    const client = benchClient([
      cell({ task: "B-001", duckling: "pato-atom" }),
      cell({ task: "B-001", duckling: "pato-local" }),
    ]);
    render(<Bench client={client} />);
    const warning = await screen.findByTestId("no-discrimination");
    expect(warning.textContent).toContain("does not tell these ducklings apart");
    // And it points at what still differs rather than stopping at the bad news.
    expect(warning.textContent).toContain("What they spent");
    expect(screen.getByTestId("chart-Tokens spent")).toBeTruthy();
  });

  it("says nothing of the sort when the ducklings differ", async () => {
    const client = benchClient([
      cell({ task: "B-001", duckling: "pato-atom" }),
      cell({ task: "B-001", duckling: "pato-local", verdict: "FAILED" }),
    ]);
    render(<Bench client={client} />);
    await screen.findByTestId("cell-table");
    expect(screen.queryByTestId("no-discrimination")).toBeNull();
  });

  // A harness failure and a model failure are different findings.
  it("shows a cell that could not run as its own outcome", async () => {
    const client = benchClient([cell({ task: "B-001", verdict: "", error: "engine died" })]);
    render(<Bench client={client} />);
    const rows = await screen.findAllByTestId("cell-row");
    expect(rows[0]!.textContent).toContain("could not run");
  });

  it("marks an estimated token count", async () => {
    const client = benchClient([cell({ task: "B-001", estimated: true })]);
    render(<Bench client={client} />);
    const rows = await screen.findAllByTestId("cell-row");
    expect(rows[0]!.textContent).toContain("~");
  });

  // One home: the tab became a room under Records, and Reports points at it
  // rather than duplicating it — the same data reachable two ways is a
  // question nobody should have to ask.
  it("points at Bench instead of embedding a second copy", async () => {
    const client = benchClient([cell({ task: "B-001" })]);
    render(<Reports client={client} projectId="p" />);
    const link = await screen.findByTestId("reports-bench-link");
    expect(link.getAttribute("href")).toBe("#/bench");
    expect(screen.queryByTestId("reports-tab-bench")).toBeNull();
  });

// "I have a new model and want to see how best to use it" had no facility at
// all: the view only showed past results and its empty state pointed at the
// CLI. The premise — a bench blocks for an afternoon — was true; the
// conclusion — therefore no button — was not. It starts without blocking.
describe("starting a bench from the desktop", () => {
  it("offers the fleet, the modes, and says how big the matrix is", async () => {
    render(<Bench client={benchClient([cell({ task: "B-001" })])} />);
    fireEvent.click(await screen.findByTestId("bench-duckling-luna"));
    fireEvent.click(screen.getByTestId("bench-mode-pair"));
    expect(screen.getByTestId("bench-cells").textContent).toContain("18 cells");
  });

  it("starts it and says the cells are watchable as runs", async () => {
    const client = benchClient([cell({ task: "B-001" })]);
    render(<Bench client={client} />);
    fireEvent.click(await screen.findByTestId("bench-duckling-luna"));
    fireEvent.click(screen.getByTestId("bench-start"));
    await waitFor(() =>
      expect(client.benchStart).toHaveBeenCalledWith({ ducklings: ["luna"], modes: ["solo"] }),
    );
    expect(screen.getByTestId("bench-running").textContent).toContain("progress below");
  });

  // The in-flight state lived only in the component: leaving for another view
  // and coming back showed a launcher at rest while nine cells burned tokens
  // in the background. The run was fine; the view had amnesia.
  it("rediscovers a bench already in flight when the view mounts", async () => {
    const client = benchClient([cell({ task: "B-001" })]);
    (client as unknown as { runs: unknown }).runs = vi.fn(() =>
      Promise.resolve([
        { id: "r-1", project_id: "ducklab-bench-abc", task_id: "B-001", mode: "solo", status: "done", verdict: "PASSED" },
        { id: "r-2", project_id: "ducklab-bench-abc", task_id: "B-002", mode: "solo", status: "running", verdict: "" },
        { id: "r-3", project_id: "calculator", task_id: "T-001", mode: "solo", status: "running", verdict: "" },
      ]),
    );
    render(<Bench client={client} />);
    const banner = await screen.findByTestId("bench-running");
    expect(banner.textContent).toContain("a bench is running");
    // Progress without a total: the planned count left with the old mount.
    expect(screen.getByTestId("bench-progress-count").textContent).toContain("1 cells done");
    // Only bench cells listed — the project run is not part of the matrix.
    expect(screen.getAllByTestId("bench-cell-run")).toHaveLength(2);
  });

  it("stays at rest when nothing bench-shaped is running", async () => {
    const client = benchClient([cell({ task: "B-001" })]);
    (client as unknown as { runs: unknown }).runs = vi.fn(() =>
      Promise.resolve([
        { id: "r-9", project_id: "ducklab-bench-old", task_id: "B-009", mode: "solo", status: "done", verdict: "PASSED" },
      ]),
    );
    render(<Bench client={client} />);
    await screen.findByTestId("bench-launcher");
    expect(screen.queryByTestId("bench-running")).toBeNull();
  });

  it("shows the engine's refusal before anything has run", async () => {
    const client = benchClient([cell({ task: "B-001" })]);
    (client as unknown as { benchStart: unknown }).benchStart = vi.fn(() =>
      Promise.reject(new Error('duckling "lunna": not found')),
    );
    render(<Bench client={client} />);
    fireEvent.click(await screen.findByTestId("bench-duckling-luna"));
    fireEvent.click(screen.getByTestId("bench-start"));
    await waitFor(() =>
      expect(screen.getByTestId("bench-start-error").textContent).toContain("lunna"),
    );
  });

  // Each cell runs in a throwaway project, so the desktop's project-scoped
  // runs list never shows one. This screen said "watchable in Records ▸ Runs"
  // and was wrong; the progress lives where the person already is.
  it("shows the cells' own progress while it runs", async () => {
    const client = benchClient([cell({ task: "B-001" })]);
    // Nothing bench-shaped at mount — the rediscovery pass sees an idle
    // engine — and the cells appear once this launch starts them.
    const cells = [
      { id: "r-c1", project_id: "ducklab-bench-x1", task_id: "B-001", mode: "solo",
        status: "done", verdict: "PASSED", started_at: "" },
      { id: "r-c2", project_id: "ducklab-bench-x2", task_id: "B-002", mode: "solo",
        status: "running", verdict: "", started_at: "" },
      { id: "r-p", project_id: "calculator", task_id: "T-001", mode: "solo",
        status: "running", verdict: "", started_at: "" },
    ];
    const runsFn = vi.fn(() => Promise.resolve(cells));
    runsFn.mockResolvedValueOnce([]);
    (client as unknown as { runs: unknown }).runs = runsFn;
    render(<Bench client={client} />);
    fireEvent.click(await screen.findByTestId("bench-duckling-luna"));
    fireEvent.click(screen.getByTestId("bench-start"));

    const progress = await screen.findByTestId("bench-progress");
    expect(screen.getByTestId("bench-progress-count").textContent).toContain("1 of 9 cells done");
    // The project's own runs are not bench cells and do not belong here.
    expect(progress.textContent).not.toContain("T-001");
    expect(progress.textContent).toContain("B-002");
  });

  // The empty state IS the launcher: a person with no bench yet is exactly the
  // person trying to start their first. And it no longer names a CLI command.
  it("greets an empty history with the launcher, not a command", async () => {
    const client = benchClient([]);
    (client as unknown as { benchList: unknown }).benchList = vi.fn(() => Promise.resolve([]));
    render(<Bench client={client} />);
    expect(await screen.findByTestId("bench-launcher")).toBeTruthy();
    expect(screen.getByTestId("bench-view").textContent).not.toContain("ducklab bench");
  });
});
});

