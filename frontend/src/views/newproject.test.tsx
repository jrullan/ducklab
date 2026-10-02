import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { NewProject } from "./NewProject";
import { parseRoute, routeHref } from "../app/routes";
import type { Duckling, EngineClient, ProviderView } from "../api/client";

const provider = (over: Partial<ProviderView>): ProviderView => ({ id: "or", kind: "openai", base_url: "https://x", key_present: true, ...over });
const duckling = (over: Partial<Duckling>): Duckling => ({ id: "luna", provider: "or", model: "m", ...over } as Duckling);

function client(ducklings: Duckling[], start: () => Promise<unknown> = vi.fn(async () => ({ project: { id: "calc", name: "calc", path: "/p/calc" }, run_id: "r-1" })), providers: ProviderView[] = [provider({})]) {
  return {
    providers: vi.fn(async () => providers),
    ducklings: vi.fn(async () => ducklings),
    projectStart: start,
    projectPresets: vi.fn(async () => []),
    projectDefaults: vi.fn(async () => ({ projects_dir: "", effective: "/home/ada/Ducklab" })),
  } as unknown as EngineClient;
}

// Jose: the guided first-run form is how EVERY new project starts, not only
// the first; it has its own address so the sidebar and Settings can lead to it.
describe("NewProject", () => {
  it("has its own route", () => {
    expect(parseRoute("#/new")).toEqual({ name: "new-project" });
    expect(routeHref({ name: "new-project" })).toBe("#/new");
  });

  it("is the same guided form, and hands the started project on", async () => {
    const onStarted = vi.fn();
    const c = client([duckling({})]);
    render(<NewProject client={c} onStarted={onStarted} />);
    expect(screen.getByTestId("new-project")).toHaveTextContent("add an existing folder");
    fireEvent.change(screen.getByTestId("start-brief"), { target: { value: "A calculator" } });
    fireEvent.change(screen.getByTestId("start-name"), { target: { value: "calc" } });
    fireEvent.click(screen.getByTestId("start-submit"));
    await waitFor(() => expect(onStarted).toHaveBeenCalledWith(expect.objectContaining({ run_id: "r-1" })));
    expect(screen.queryByTestId("start-model-warning")).toBeNull();
  });

  it("warns only when no model can draft, and keeps a project whose drafting could not start", async () => {
    const start = vi.fn(async () => ({ project: { id: "calc", name: "calc", path: "/p/calc" }, intake_error: "no model" }));
    const onStarted = vi.fn();
    render(<NewProject client={client([], start)} onStarted={onStarted} />);
    expect(await screen.findByTestId("start-model-warning")).toHaveTextContent("no model on it is configured");
    fireEvent.change(screen.getByTestId("start-name"), { target: { value: "calc" } });
    fireEvent.click(screen.getByTestId("start-submit"));
    expect(await screen.findByTestId("new-project-stalled")).toHaveTextContent("could not start: no model");
    fireEvent.click(screen.getByTestId("new-project-continue"));
    expect(onStarted).toHaveBeenCalled();
  });

  // Review of #130: a configured model whose provider key is missing is not
  // "no model"; the advice names the key, not "add a model".
  it("names the missing key when a configured model cannot reach its provider", async () => {
    render(<NewProject client={client([duckling({})], undefined, [provider({ api_key_env: "OPENROUTER_API_KEY", key_present: false })])} onStarted={vi.fn()} />);
    const warning = await screen.findByTestId("start-model-warning");
    expect(warning).toHaveTextContent("or needs OPENROUTER_API_KEY");
    expect(warning).not.toHaveTextContent("Add one in Settings → ducklings");
  });

  it("names a provider that does not exist", async () => {
    render(<NewProject client={client([duckling({ provider: "gone" })])} onStarted={vi.fn()} />);
    expect(await screen.findByTestId("start-model-warning")).toHaveTextContent("providers that are not configured");
  });
});
