import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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

  // B-455: a fresh installation landed on an empty Now with no door.
  it("opens on the first-run page when the engine has no project", async () => {
    history.replaceState({}, "", "/?engine=http%3A%2F%2Fengine.test&token=t");
    localStorage.removeItem("ducklab.project");
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ items: [] }))));
    vi.stubGlobal("EventSource", EventSourceStub);
    render(<App />);
    expect(await screen.findByTestId("first-run")).toBeInTheDocument();
    act(() => EventSourceStub.latest!.onopen?.({}));
    expect(screen.getByTestId("first-run-engine")).toHaveTextContent("engine connected");
    expect(screen.getByTestId("first-run-create")).toHaveTextContent("Create your first project");
  });

  // The guided start is the door for every new project, not only the first:
  // #/new renders it with projects present, and a started project becomes the
  // selected one and lands on its drafting run.
  it("opens the guided start at #/new with projects present and selects the started project", async () => {
    history.replaceState({}, "", "/?engine=http%3A%2F%2Fengine.test&token=t#/new");
    localStorage.setItem("ducklab.project", "old");
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/v1/projects/start") && init?.method === "POST") {
        return new Response(JSON.stringify({ project: { id: "calc", name: "calc", path: "/p/calc" }, run_id: "r-new" }));
      }
      if (url.includes("/v1/projects") && !url.includes("/v1/projects/")) {
        return new Response(JSON.stringify({ items: [{ id: "old", name: "Old", path: "/p/old" }, { id: "calc", name: "calc", path: "/p/calc" }] }));
      }
      return new Response(JSON.stringify({ items: [] }));
    }));
    vi.stubGlobal("EventSource", EventSourceStub);
    render(<App />);
    expect(await screen.findByTestId("new-project")).toBeInTheDocument();
    fireEvent.change(screen.getByTestId("start-name"), { target: { value: "calc" } });
    fireEvent.click(screen.getByTestId("start-submit"));
    await waitFor(() => expect(location.hash).toBe("#/runs/r-new"));
    expect(localStorage.getItem("ducklab.project")).toBe("calc");
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
