import { describe, expect, it } from "vitest";
import type { Run } from "../api/client";
import { isRateLimited, plainFailure } from "./RunView";

const run = { id: "r", project_id: "p", stage: "intake", mode: "council", status: "failed", verdict: "FAILED", started_at: "" } as Run;

// Jose's intake (2026-10-02): an upstream 429 read "Ducklab stopped this run
// before it could finish". The summary now says what happened and what to do.
describe("run failure summary", () => {
  const terminal = `provider chat: chat stream: 429 Too Many Requests: {"error":{"message":"Provider returned error","code":429,"metadata":{"raw":"moonshotai/kimi-k3 is temporarily rate-limited upstream."}}}`;
  const paused = "provider unavailable: after 3 attempts: rate limited: chat stream: 429 Too Many Requests: {}";

  it("recognises a rate limit in the engine's words and in the raw status", () => {
    expect(isRateLimited(terminal)).toBe(true);
    expect(isRateLimited(paused)).toBe(true);
    expect(isRateLimited("rate limited")).toBe(true);
  });

  it("does not mistake other failures for a rate limit", () => {
    for (const other of [
      "provider unavailable: dial tcp 127.0.0.1:8080: connection refused",
      "provider unavailable: after 3 attempts: chat stream: 503 Service Unavailable",
      "contract parse failed: verdict contract (role reviewer)",
      "the implementer ran out of calls per reply",
      "context length 4290 exceeded", // a number that merely contains 429
      undefined,
    ]) {
      expect(isRateLimited(other)).toBe(false);
    }
  });

  it("explains a rate limit as temporary, ahead of the generic provider message", () => {
    expect(plainFailure(terminal, run)).toContain("rate-limiting requests (HTTP 429)");
    expect(plainFailure(paused, run)).toContain("rate-limiting requests (HTTP 429)");
    expect(plainFailure("provider unavailable: after 3 attempts: chat stream: 503", run)).toBe("The model provider stopped responding after Ducklab retried.");
  });
});
