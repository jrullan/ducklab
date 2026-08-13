import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { App } from "./App";
import { useRuns } from "../store/runs";

// A stale engine used to be a sentence buried in whichever view hit it first,
// telling the person to open a terminal. The one action that fixes it is now a
// button beside the words, and clicking it reconnects in place.
describe("the engine restart banner", () => {
  beforeEach(() => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
    window.ducklab = {
      baseUrl: "http://e1",
      token: "t1",
      restartEngine: "shell.RestartEngine",
      reconnectEngine: "shell.ReconnectEngine",
    };
    (window as unknown as { wails: unknown }).wails = {
      Call: { ByName: vi.fn(() => Promise.resolve({ baseUrl: "http://e2", token: "t2" })) },
    };
    (window as unknown as { EventSource: unknown }).EventSource = class {
      onopen: unknown; onerror: unknown;
      addEventListener() {}
      close() {}
    };
  });
  afterEach(() => {
    delete window.ducklab;
  });

  it("appears when a response reveals a stale engine, and reconnects on click", async () => {
    // The old engine (e1) answers unknown-route 404s; the fresh one (e2)
    // knows everything, so the banner must not come back after reconnecting.
    const fetchFn = vi.fn((url: string) => {
      const u = String(url);
      if (u.includes("/v1/health")) return Promise.resolve(new Response(JSON.stringify({ version: "x" }), { status: 200 }));
      if (u.startsWith("http://e2")) return Promise.resolve(new Response(JSON.stringify({ items: [] }), { status: 200 }));
      return Promise.resolve(new Response("404 page not found", { status: 404 }));
    }) as unknown as typeof fetch;
    vi.stubGlobal("fetch", fetchFn);

    render(<App />);
    expect(screen.queryByTestId("stale-banner")).toBeNull();

    // The App's own project-list/runs fetch trips the stale classifier.
    await waitFor(() => expect(screen.getByTestId("stale-banner")).toBeTruthy(), { timeout: 3000 });

    fireEvent.click(screen.getByTestId("restart-engine"));
    await waitFor(() =>
      expect(
        (window as unknown as { wails: { Call: { ByName: ReturnType<typeof vi.fn> } } }).wails.Call.ByName,
      ).toHaveBeenCalledWith("shell.RestartEngine"),
    );
    // The banner clears once the fresh connection is in hand.
    await waitFor(() => expect(screen.queryByTestId("stale-banner")).toBeNull());
    vi.unstubAllGlobals();
  });

  // The inverse of the older-engine case, and the one that actually bit
  // twice: the engine restarted OUTSIDE the app, this window's token died
  // with it, and every request 401ed into fifteen Load Errors with no
  // explanation. Same banner, other remedy — reconnect adopts the running
  // engine's fresh connection without touching the process.
  it("offers Reconnect when the engine was restarted outside the app", async () => {
    const fetchFn = vi.fn((url: string) => {
      const u = String(url);
      if (u.includes("/v1/health")) return Promise.resolve(new Response(JSON.stringify({ version: "x" }), { status: 200 }));
      if (u.startsWith("http://e2")) return Promise.resolve(new Response(JSON.stringify({ items: [] }), { status: 200 }));
      return Promise.resolve(new Response(JSON.stringify({ error: { message: "invalid token" } }), { status: 401 }));
    }) as unknown as typeof fetch;
    vi.stubGlobal("fetch", fetchFn);

    render(<App />);
    await waitFor(() => expect(screen.getByTestId("stale-banner")).toBeTruthy(), { timeout: 3000 });
    expect(screen.getByTestId("stale-banner").textContent).toContain("restarted outside the app");
    expect(screen.queryByTestId("restart-engine")).toBeNull();

    fireEvent.click(screen.getByTestId("reconnect-engine"));
    await waitFor(() =>
      expect(
        (window as unknown as { wails: { Call: { ByName: ReturnType<typeof vi.fn> } } }).wails.Call.ByName,
      ).toHaveBeenCalledWith("shell.ReconnectEngine"),
    );
    await waitFor(() => expect(screen.queryByTestId("stale-banner")).toBeNull());
    vi.unstubAllGlobals();
  });
});

// The shell injects window.ducklab via a script the webview runs around page
// load — sometimes after the bundle. Reading it once turned that race into a
// permanent "no engine connection details" that a relaunch usually won:
// red on some starts, fine on the next. The app now waits for the injection.
describe("the connection-details race at startup", () => {
  beforeEach(() => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
    delete window.ducklab; // the bundle woke first
    (window as unknown as { EventSource: unknown }).EventSource = class {
      onopen: unknown; onerror: unknown;
      addEventListener() {}
      close() {}
    };
    vi.stubGlobal("fetch", vi.fn(() =>
      Promise.resolve(new Response(JSON.stringify({ items: [], version: "x" }), { status: 200 })),
    ) as unknown as typeof fetch);
  });

  it("connects when the injection lands late instead of erroring forever", async () => {
    render(<App />);
    // Too early to condemn: the poller is still waiting.
    expect(screen.queryByText(/no engine connection details/)).toBeNull();

    // The shell's script lands a beat later.
    window.ducklab = { baseUrl: "http://late", token: "tok" };
    await waitFor(() => {
      expect(screen.queryByText(/no engine connection details/)).toBeNull();
      expect(screen.queryByTestId("app-shell") ?? document.querySelector("nav")).toBeTruthy();
    });
  });
});
