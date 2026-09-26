import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { EngineClient } from "../api/client";
import { AppControl } from "./AppControl";

describe("AppControl build provenance", () => {
  it("shows which HEAD the engine built before launching", async () => {
    const client = {
      appStatus: vi.fn(() => Promise.resolve({
        configured: true,
        command: "./build/app",
        running: true,
        pid: 42,
        built_sha: "abcdef1234567890",
        built_at: "2026-09-25T12:00:00Z",
      })),
    } as unknown as EngineClient;

    render(<AppControl client={client} projectId="demo" />);

    const provenance = await screen.findByTestId("app-built-head");
    expect(provenance).toHaveTextContent("built HEAD abcdef12 before launch");
    expect(provenance).toHaveAttribute("title", "2026-09-25T12:00:00Z");
  });
});
