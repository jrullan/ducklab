import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";

class EventSourceStub {
  static latest: EventSourceStub | null = null;
  onerror: ((e: unknown) => void) | null = null;
  onopen: ((e: unknown) => void) | null = null;

  constructor() { EventSourceStub.latest = this; }
  addEventListener() {}
  close() {}
}

describe("App browser engine connection", () => {
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
    delete window.ducklab;
    history.replaceState({}, "", "/");
  });

  it("uses dev query connection details when the desktop host is absent", async () => {
    delete window.ducklab;
    history.replaceState({}, "", "/?engine=http%3A%2F%2Fengine.test&token=dev-token");
    const fetchMock = vi.fn((url: string) => {
      if (url.endsWith("/v1/projects")) {
        return Promise.resolve(new Response(JSON.stringify({ items: [] }), { status: 200 }));
      }
      return Promise.resolve(new Response(JSON.stringify({ ok: true, version: "test" }), { status: 200 }));
    });
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("EventSource", EventSourceStub);

    render(<App />);

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "http://engine.test/v1/projects",
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: "Bearer dev-token" }),
        }),
      );
    });
    expect(screen.queryByTestId("app-error")).not.toBeInTheDocument();
  });

  it("reports missing connection details when neither desktop nor query sources exist", () => {
    vi.useFakeTimers();
    delete window.ducklab;
    history.replaceState({}, "", "/");
    vi.stubGlobal("EventSource", EventSourceStub);

    render(<App />);
    act(() => {
      vi.advanceTimersByTime(10_000);
    });

    expect(screen.getByTestId("app-error")).toHaveTextContent(
      "no engine connection details were provided by the host",
    );
  });

  it.each([
    ["older", { status: 404, body: "404 page not found", headers: { "X-Ducklab-Unknown-Route": "true" } }],
    ["env", { status: 200, body: JSON.stringify({ ok: true }) }],
  ] as const)("shows a non-empty banner when the %s stale trigger dims MAIN", async (kind, response) => {
    history.replaceState({}, "", "/?engine=http%3A%2F%2Fengine.test&token=t");
    if (kind === "env") window.ducklab = { baseUrl: "http://engine.test", token: "t", engineMissingKeys: ["OPENAI_API_KEY"] };
    vi.stubGlobal("fetch", vi.fn(async () => new Response(response.body, { status: response.status, headers: "headers" in response ? response.headers : undefined })));
    vi.stubGlobal("EventSource", EventSourceStub);

    render(<App />);

    await waitFor(() => expect(screen.getByTestId("main-disabled-banner")).toBeInTheDocument());
    expect(screen.getByTestId("main-disabled-banner").textContent?.trim()).toBeTruthy();
    expect(screen.getByTestId("stale-read-only")).toHaveClass("pointer-events-none");
  });

  it("shows a non-empty reconnecting banner and clears the dim when open", async () => {
    history.replaceState({}, "", "/?engine=http%3A%2F%2Fengine.test&token=t");
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ items: [] }))));
    vi.stubGlobal("EventSource", EventSourceStub);
    render(<App />);

    await waitFor(() => expect(EventSourceStub.latest).not.toBeNull());
    act(() => EventSourceStub.latest!.onerror?.(new Error("disconnected")));
    await waitFor(() => expect(screen.getByTestId("main-disabled-banner").textContent?.trim()).toBeTruthy());
    expect(screen.getByRole("main")).not.toHaveClass("pointer-events-none");

    act(() => EventSourceStub.latest!.onopen?.({}));
    await waitFor(() => expect(screen.queryByTestId("main-disabled-banner")).not.toBeInTheDocument());
  });
});
