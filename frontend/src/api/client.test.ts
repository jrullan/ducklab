import { describe, it, expect } from "vitest";
import { EngineClient, ApiError } from "./client";

const clientAnswering = (status: number, body: string, type = "text/plain", headers: Record<string, string> = {}) =>
  new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async () =>
      new Response(body, { status, headers: { "Content-Type": type, ...headers } })) as unknown as typeof fetch,
  });

// A bench launch against an engine started before the route existed failed
// with "POST /v1/bench/start failed" — the client had everything it needed to
// say what was wrong and what to do, and said neither.
describe("summary board requests", () => {
  it("requests summary task and bug lists, but leaves existing list callers unchanged", async () => {
    const paths: string[] = [];
    const c = new EngineClient({
      baseUrl: "http://engine",
      token: "t",
      fetchFn: (async (url: string) => {
        paths.push(String(url).replace("http://engine", ""));
        return new Response(JSON.stringify({ items: [], total: 0 }), {
          headers: { "Content-Type": "application/json" },
        });
      }) as unknown as typeof fetch,
    });

    // The boolean is deliberately an API option rather than a different route:
    // regular callers retain their complete-list contract.
    await c.tasks("p");
    await c.bugs("p");
    await (c.tasks as unknown as (id: string, summary: boolean) => Promise<unknown>)("p", true);
    await (c.bugs as unknown as (id: string, openOnly: boolean, summary: boolean) => Promise<unknown>)("p", false, true);

    expect(paths).toEqual([
      "/v1/projects/p/tasks",
      "/v1/projects/p/bugs",
      "/v1/projects/p/tasks?summary=true",
      "/v1/projects/p/bugs?summary=true",
    ]);
  });

  it("fetches a selected bug's full detail from its item URL", async () => {
    let path = "";
    const c = new EngineClient({
      baseUrl: "http://engine",
      token: "t",
      fetchFn: (async (url: string) => {
        path = String(url).replace("http://engine", "");
        return new Response(JSON.stringify({ id: "B-7", title: "detail", body: "full body", history: [] }), {
          headers: { "Content-Type": "application/json" },
        });
      }) as unknown as typeof fetch,
    });

    const bug = await (c as unknown as { bug: (projectId: string, bugId: string) => Promise<{ id: string; body: string }> }).bug("p", "B-7");
    expect(path).toBe("/v1/projects/p/bugs/B-7");
    expect(bug.body).toBe("full body");
  });
});

describe("what an error names", () => {
  it("names the stale engine when a route is unknown, and the fix", async () => {
    const c = clientAnswering(404, "404 page not found", "text/plain", { "X-Ducklab-Unknown-Route": "true" });
    const err = await c.benchStart({ ducklings: ["luna"], modes: ["solo"] }).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.message).toContain("older than this app");
    expect(err.message).toContain("Restart the engine");
  });

  it("recognizes Go's newline-terminated legacy 404 body as an older engine", async () => {
    let stale: string | false = false;
    const c = new EngineClient({
      baseUrl: "http://engine",
      token: "t",
      onStale: (reason) => { stale = reason; },
      fetchFn: (async () => new Response("404 page not found\n", {
        status: 404,
        headers: { "Content-Type": "text/plain" },
      })) as unknown as typeof fetch,
    });

    const err = await c.benchStart({ ducklings: ["luna"], modes: ["solo"] }).catch((e) => e);
    expect(stale).toBe("older");
    expect(err).toBeInstanceOf(ApiError);
    expect(err.message).toContain("older than this app");
  });

  it("does not infer staleness from a data 404", async () => {
    let stale = false;
    const c = new EngineClient({
      baseUrl: "http://engine",
      token: "t",
      onStale: () => { stale = true; },
      fetchFn: (async () => new Response(JSON.stringify({ error: { code: "not_found", message: "project missing" } }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      })) as unknown as typeof fetch,
    });
    const err = await c.projects().catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(stale).toBe(false);
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

// The Flock showed "no runs yet" for a duckling with 264 runs: the hand-written
// Scorecards() asked /v1/scorecards, the engine serves /v1/ducklings/scorecards,
// and the 404 was swallowed. The generated route table is the truth.
describe("scorecards request", () => {
  it("asks the path the engine registers for Scorecards", async () => {
    const { OPERATIONS } = await import("./generated");
    const declared = OPERATIONS.find((o) => o.id === "Scorecards")!.path;
    const paths: string[] = [];
    const c = new EngineClient({
      baseUrl: "http://engine",
      token: "t",
      fetchFn: (async (url: string) => {
        paths.push(String(url).replace("http://engine", ""));
        return new Response(JSON.stringify({ items: [{ id: "glm52", measured: { runs: 264 } }], total: 1 }), {
          headers: { "Content-Type": "application/json" },
        });
      }) as unknown as typeof fetch,
    });
    const cards = await c.Scorecards();
    expect(paths).toEqual([declared]);
    expect(cards[0]?.measured?.runs).toBe(264);
  });
});

// A committed accept may report a retryable publication failure. Status carries
// the independent ahead/behind evidence a client needs to offer that retry.
describe("publication API", () => {
  it("returns accept warning and publication status from their declared routes", async () => {
    const paths: string[] = [];
    const c = new EngineClient({
      baseUrl: "http://engine",
      token: "t",
      fetchFn: (async (url: string) => {
        const path = String(url).replace("http://engine", "");
        paths.push(path);
        const body = path.endsWith("/accept")
          ? { commit_sha: "abc123", warning: "committed as abc123; push failed: denied" }
          : { ahead: 2, behind: 0 };
        return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
      }) as unknown as typeof fetch,
    });

    const accepted = await c.accept("run");
    const status = await c.projectStatus("project");
    expect(accepted).toEqual({ commit_sha: "abc123", warning: "committed as abc123; push failed: denied" });
    expect(status).toEqual({ ahead: 2, behind: 0 });
    expect(paths).toEqual(["/v1/runs/run/accept", "/v1/projects/project/status"]);
  });
});
