import { describe, it, expect } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";
import { EngineClient } from "../api/client";

function clientFor(run: Record<string, unknown>) {
  return new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async (url: string) => {
      if (String(url).replace("http://engine", "") === `/v1/runs/${run.id}`) {
        return new Response(JSON.stringify({ run, events: [] }), { headers: { "Content-Type": "application/json" } });
      }
      return new Response("{}", { status: 404 });
    }) as unknown as typeof fetch,
  });
}

function reset() {
  useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, acceptState: {}, needsResync: false, connection: "open" });
}

describe("RunView worktree surface", () => {
  it("shows a live worktree's branch and path", async () => {
    reset();
    const run = { id: "r-worktree", project_id: "p", stage: "build", mode: "solo", task_id: "T-115", status: "running", verdict: "", started_at: "2026-08-24T00:00:00Z", branch: "ducklab/T-115-worktree", worktree_path: "/state/worktrees/p/r-worktree" };
    render(<RunView runId="r-worktree" client={clientFor(run)} />);
    await waitFor(() => expect(screen.getByTestId("worktree-badge")).toHaveTextContent("ducklab/T-115-worktree"));
    expect(screen.getByTestId("worktree-badge")).toHaveTextContent("/state/worktrees/p/r-worktree");
  });

  it("lists a paused rebase conflict and its only lawful options", async () => {
    reset();
    const run = { id: "r-conflict", project_id: "p", stage: "build", mode: "solo", task_id: "T-115", status: "paused", verdict: "", started_at: "2026-08-24T00:00:00Z", pending_kind: "gate", worktree_path: "/state/worktrees/p/r-conflict", pending_data: { worktree: "/state/worktrees/p/r-conflict", base_sha: "abc123", default_sha: "def456", conflicting_files: ["internal/service/queue.go", "frontend/src/views/RunView.tsx"] } };
    render(<RunView runId="r-conflict" client={clientFor(run)} />);
    await waitFor(() => expect(screen.getByTestId("worktree-conflict")).toHaveTextContent("internal/service/queue.go"));
    const card = screen.getByTestId("worktree-conflict");
    expect(card).toHaveTextContent("frontend/src/views/RunView.tsx");
    expect(card).toHaveTextContent("abc123");
    expect(card).toHaveTextContent("def456");
    expect(card).toHaveTextContent("resolve by hand");
    expect(card).toHaveTextContent("reject");
  });
});
