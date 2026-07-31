import { describe, it, expect } from "vitest";
import { EngineClient, ApiError } from "./client";

const clientAnswering = (status: number, body: string, type = "text/plain") =>
  new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async () =>
      new Response(body, { status, headers: { "Content-Type": type } })) as unknown as typeof fetch,
  });

// A bench launch against an engine started before the route existed failed
// with "POST /v1/bench/start failed" — the client had everything it needed to
// say what was wrong and what to do, and said neither.
describe("what an error names", () => {
  it("names the stale engine when a route is unknown, and the fix", async () => {
    const c = clientAnswering(404, "404 page not found");
    const err = await c.benchStart({ ducklings: ["luna"], modes: ["solo"] }).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.message).toContain("older than this app");
    expect(err.message).toContain("Restart the engine");
  });

  it("passes the engine's own words through untouched", async () => {
    const c = clientAnswering(400, JSON.stringify({ error: { message: 'duckling "lunna": not found' } }), "application/json");
    const err = await c.benchStart({ ducklings: ["lunna"], modes: ["solo"] }).catch((e) => e);
    expect(err.message).toContain("lunna");
    expect(err.message).not.toContain("older than this app");
  });

  it("carries the status and body when there is no shaped message", async () => {
    const c = clientAnswering(502, "bad gateway");
    const err = await c.benchStart({ ducklings: ["luna"], modes: ["solo"] }).catch((e) => e);
    expect(err.message).toContain("502");
    expect(err.message).toContain("bad gateway");
  });
});
