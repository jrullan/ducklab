import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { EngineClient } from "../api/client";
import { useRuns } from "../store/runs";
import { RunView } from "./RunView";

// The amendment is emitted by the service only after a config-shaped terminal
// failure. The view must turn that persisted evidence into the consultant door.
describe("RunView configuration failures", () => {
  it("renders the finding and sends it to the consultant", async () => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open" });
    const requests: { path: string; body?: Record<string, unknown> }[] = [];
    const run = {
      id: "r-config", project_id: "p", stage: "build", mode: "solo", task_id: "T-001",
      status: "failed", verdict: "FAILED", started_at: "2026-08-01T00:00:00Z",
    };
    const events = [{
      seq: 1, type: "config_amendment", run_id: "r-config",
      data: { key: "verify.link_deps", new: "frontend/node_modules", why: "dependencies are unavailable" },
    }];
    const client = new EngineClient({
      baseUrl: "http://engine",
      token: "t",
      fetchFn: (async (url: string, init?: RequestInit) => {
        const path = String(url).replace("http://engine", "");
        requests.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
        if (path === "/v1/runs/r-config") return new Response(JSON.stringify({ run, events }), { headers: { "Content-Type": "application/json" } });
        if (path === "/v1/ducklings") return new Response(JSON.stringify({ items: [{ id: "consultant", provider: "fake", model: "test", caps: { native_tools: false, context_tokens: 1 } }] }), { headers: { "Content-Type": "application/json" } });
        if (path === "/v1/projects/p/tasks") return new Response(JSON.stringify({ items: [] }), { headers: { "Content-Type": "application/json" } });
        if (path === "/v1/projects/p/reports?by=mode" || path === "/v1/projects/p/reports?by=duckling") return new Response(JSON.stringify({ rows: [] }), { headers: { "Content-Type": "application/json" } });
        if (path === "/v1/projects/p/chats") return new Response(JSON.stringify({ id: "chat-1" }), { headers: { "Content-Type": "application/json" } });
        return new Response(JSON.stringify({}), { headers: { "Content-Type": "application/json" } });
      }) as never,
    });

    render(<RunView runId="r-config" client={client} />);
    await waitFor(() => expect(screen.getByTestId("config-failure-card")).toHaveTextContent("verify.link_deps"));
    expect(screen.queryByTestId("config-amendment-card")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("chat-about"));
    fireEvent.change(screen.getByTestId("chat-duckling"), { target: { value: "consultant" } });
    fireEvent.click(screen.getByTestId("chat-start"));

    await waitFor(() => expect(requests.find((request) => request.path === "/v1/projects/p/chats")?.body).toMatchObject({
      duckling: "consultant",
      about_kind: "ducklab",
      about_id: "configuration",
      message: expect.stringContaining("verify.link_deps → frontend/node_modules"),
    }));
  });
});
