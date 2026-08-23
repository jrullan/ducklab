import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";

class EventSourceStub {
  onerror: ((e: unknown) => void) | null = null;
  onopen: ((e: unknown) => void) | null = null;

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
});
