import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { FirstRun, readiness } from "./FirstRun";
import type { Duckling, EngineClient, ProviderView } from "../api/client";

const provider = (over: Partial<ProviderView>): ProviderView => ({ id: "or", kind: "openai", base_url: "https://x", key_present: true, ...over });
const duckling = (over: Partial<Duckling>): Duckling => ({ id: "luna", provider: "or", model: "m", ...over } as Duckling);

function clientWith(providers: ProviderView[], ducklings: Duckling[], probe: (id: string) => Promise<unknown> = () => Promise.resolve({})) {
  return {
    providers: vi.fn(() => Promise.resolve(providers)),
    ducklings: vi.fn(() => Promise.resolve(ducklings)),
    ducklingProbe: vi.fn(probe),
  } as unknown as EngineClient;
}

describe("first-run readiness (B-455)", () => {
  it("counts a duckling usable only when its provider exists and has its key", () => {
    const r = readiness(
      [provider({ id: "or", api_key_env: "OR_KEY", key_present: false }), provider({ id: "local", api_key_env: undefined })],
      [duckling({ id: "a", provider: "or" }), duckling({ id: "b", provider: "local" }), duckling({ id: "c", provider: "gone" })],
    );
    expect(r.usable).toEqual(["b"]);
    expect(r.missingKeys).toEqual([{ provider: "or", env: "OR_KEY" }]);
  });
});

describe("FirstRun", () => {
  it("leads with one create action and never calls a configured model ready before it answers", async () => {
    const client = clientWith([provider({})], [duckling({})]);
    render(<FirstRun client={client} connected />);
    expect(screen.getByTestId("first-run-create")).toHaveTextContent("Create your first project");
    expect(screen.getByTestId("first-run-create").getAttribute("href")).toContain("projects");
    expect(await screen.findByText(/1 model configured \(luna\) · not tested yet/)).toBeInTheDocument();
    expect(client.ducklingProbe).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("first-run-test-models"));
    expect(await screen.findByText(/· answering/)).toBeInTheDocument();
    expect(screen.getByTestId("first-run-engine")).toHaveTextContent("engine connected");
  });

  it("says which model did not answer and where to fix it", async () => {
    render(<FirstRun client={clientWith([provider({})], [duckling({})], () => Promise.reject(new Error("connection refused")))} connected />);
    fireEvent.click(await screen.findByTestId("first-run-test-models"));
    const failure = await screen.findByTestId("first-run-test-failure");
    expect(failure).toHaveTextContent("luna did not answer: connection refused");
    expect(failure.querySelector("a")?.getAttribute("href")).toContain("ducklings");
  });

  it("explains what is missing when there is no provider, and links the fix", async () => {
    render(<FirstRun client={clientWith([], [])} connected />);
    const fix = await screen.findByTestId("first-run-model-fix");
    expect(fix).toHaveTextContent("Add one, then add a duckling");
    expect(fix.querySelector("a")?.getAttribute("href")).toContain("ducklings");
    expect(screen.getByText(/You can create the project now/)).toBeInTheDocument();
  });

  it("names the missing key and says keys are never stored", async () => {
    render(<FirstRun client={clientWith([provider({ api_key_env: "OPENROUTER_API_KEY", key_present: false })], [duckling({})])} connected={false} />);
    expect(await screen.findByTestId("first-run-missing-keys")).toHaveTextContent("OPENROUTER_API_KEY");
    expect(screen.getByTestId("first-run-missing-keys")).toHaveTextContent("never stores keys");
    expect(screen.getByTestId("first-run-engine")).toHaveTextContent("live updates connecting");
  });
});
