import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import * as attention from "../lib/attention";
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

describe("AppControl auto-open (B-553)", () => {
  const base = { configured: true, command: "npm run dev", smoke_expectation: "live", smoke_timeout_s: 3 };

  it("opens main's app once it prints its address after the person's Launch", async () => {
    const open = vi.spyOn(attention, "openExternal").mockImplementation(() => {});
    try {
      const appStatus = vi.fn(() => Promise.resolve({ ...base, running: false }));
      const client = { appStatus, appStart: vi.fn(() => Promise.resolve({ ...base, running: true })) } as unknown as EngineClient;
      render(<AppControl client={client} projectId="demo" />);
      appStatus.mockResolvedValue({ ...base, running: true, url: "http://localhost:5173/", url_source: "output" } as never);
      fireEvent.click(await screen.findByTestId("app-launch"));
      await waitFor(() => expect(open).toHaveBeenCalledWith("http://localhost:5173/"));
      expect(open).toHaveBeenCalledTimes(1);
    } finally {
      open.mockRestore();
    }
  });

  it("leaves a run's app to its own card", async () => {
    const open = vi.spyOn(attention, "openExternal").mockImplementation(() => {});
    try {
      const appStatus = vi.fn(() => Promise.resolve({ ...base, running: false }));
      const client = { appStatus, appStart: vi.fn(() => Promise.resolve({ ...base, running: true })) } as unknown as EngineClient;
      render(<AppControl client={client} projectId="demo" />);
      appStatus.mockResolvedValue({ ...base, running: true, run_id: "r-1", url: "http://localhost:41666/" } as never);
      fireEvent.click(await screen.findByTestId("app-launch"));
      await screen.findByTestId("app-stop");
      await new Promise((r) => setTimeout(r, 700));
      expect(open).not.toHaveBeenCalled();
    } finally {
      open.mockRestore();
    }
  });
});
