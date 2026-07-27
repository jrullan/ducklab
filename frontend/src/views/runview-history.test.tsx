import { describe, it, expect } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";
import { EngineClient } from "../api/client";

// The store is fed by the live event stream, so a run that finished before
// this client connected had no conversation, no gate and no tool timeline —
// a blank lane beside a header saying the run passed. Clients hold no state
// (I11), so the record has to be fetched.
describe("RunView on a run it did not watch live", () => {
  it("fetches the run's history when opened", async () => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, acceptState: {}, needsResync: false, connection: "open" });

    const asked: string[] = [];
    const client = new EngineClient({
      baseUrl: "http://engine",
      token: "t",
      fetchFn: (async (url: string) => {
        const path = String(url).replace("http://engine", "");
        asked.push(path);
        if (path === "/v1/runs/r-past") {
          return new Response(
            JSON.stringify({
              run: {
                id: "r-past", project_id: "p", stage: "build", mode: "pair",
                task_id: "T-001", status: "done", verdict: "PASSED",
                started_at: "2026-07-26T00:00:00Z",
              },
              events: [
                { seq: 1, type: "turn_start", run_id: "r-past", data: { duckling: "pato-uno", role: "implementer", turn: 0 } },
                { seq: 2, type: "message", run_id: "r-past", data: { role: "implementer", content: "Fixed add.go." } },
                { seq: 3, type: "turn_end", run_id: "r-past", data: { role: "implementer", turn: 0 } },
              ],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } });
      }) as unknown as typeof fetch,
    });

    render(<RunView runId="r-past" client={client} />);

    await waitFor(() => expect(asked).toContain("/v1/runs/r-past"));
    await waitFor(() => expect(useRuns.getState().events["r-past"]?.length).toBe(3));
  });
});
