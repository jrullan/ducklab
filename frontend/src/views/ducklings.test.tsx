import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Ducklings } from "./Ducklings";
import type { Duckling, EngineClient, ProviderView } from "../api/client";

const provider = (o: Partial<ProviderView> & { id: string }): ProviderView => ({
  kind: "openai", base_url: `https://${o.id}/v1`, key_present: true, ...o,
});
const duckling = (o: Partial<Duckling> & { id: string }): Duckling => ({
  provider: "local", model: "qwen", ...o,
});

function clientWith(ducklings: Duckling[], providers: ProviderView[]) {
  return {
    ducklings: vi.fn(() => Promise.resolve(ducklings)),
    providers: vi.fn(() => Promise.resolve(providers)),
    providerSet: vi.fn(() => Promise.resolve({})),
    providerRemove: vi.fn(() => Promise.resolve({})),
    ducklingSet: vi.fn(() => Promise.resolve({})),
    ducklingRemove: vi.fn(() => Promise.resolve({})),
  } as unknown as EngineClient;
}

describe("Ducklings", () => {
  it("shows what a duckling is and how it speaks", async () => {
    const client = clientWith(
      [duckling({ id: "pato-local", caps: { native_tools: false, context_tokens: 65536 } })],
      [provider({ id: "local", key_present: true })],
    );
    render(<Ducklings client={client} />);
    const card = await screen.findByTestId("duckling-card-pato-local");
    expect(card.textContent).toContain("text protocol");
    expect(card.textContent).toContain("65,536");
  });

  // The commonest reason a hosted duckling fails, and invisible otherwise.
  it("warns on the duckling whose provider has no key", async () => {
    const client = clientWith(
      [duckling({ id: "pato-sonnet", provider: "openrouter" })],
      [provider({ id: "openrouter", api_key_env: "OPENROUTER_API_KEY", key_present: false })],
    );
    render(<Ducklings client={client} />);
    expect((await screen.findByTestId("duckling-card-pato-sonnet")).textContent).toContain(
      "OPENROUTER_API_KEY not set",
    );
    expect((await screen.findByTestId("provider-row-openrouter")).textContent).toContain("not set");
  });

  it("says nothing about keys for a local provider that needs none", async () => {
    const client = clientWith([], [provider({ id: "local" })]);
    render(<Ducklings client={client} />);
    expect((await screen.findByTestId("provider-row-local")).textContent).toContain("no key needed");
  });

  // I10 in the interface, not only in the engine: there is nowhere to type a
  // key, so nobody can be led into pasting one.
  it("offers no field for a key, only for the variable's name", async () => {
    const client = clientWith([], []);
    render(<Ducklings client={client} />);
    fireEvent.click(await screen.findByTestId("provider-add"));
    const form = screen.getByTestId("provider-form");
    expect(form.textContent).toContain("name");
    expect(screen.getByTestId("provider-key-env")).toBeTruthy();
    for (const input of Array.from(form.querySelectorAll("input"))) {
      expect(input.getAttribute("type")).not.toBe("password");
      expect((input.getAttribute("aria-label") ?? "").toLowerCase()).not.toMatch(/^api key$/);
    }
  });

  it("adds a provider", async () => {
    const client = clientWith([], []);
    render(<Ducklings client={client} />);
    fireEvent.click(await screen.findByTestId("provider-add"));
    fireEvent.change(screen.getByTestId("provider-id"), { target: { value: "openrouter" } });
    fireEvent.change(screen.getByTestId("provider-url"), {
      target: { value: "https://openrouter.ai/api/v1" },
    });
    fireEvent.change(screen.getByTestId("provider-key-env"), {
      target: { value: "OPENROUTER_API_KEY" },
    });
    fireEvent.click(screen.getByTestId("provider-save"));
    await waitFor(() =>
      expect(client.providerSet).toHaveBeenCalledWith("openrouter", {
        base_url: "https://openrouter.ai/api/v1",
        api_key_env: "OPENROUTER_API_KEY",
        kind: "openai",
      }),
    );
  });

  // A duckling is a model reached through a provider, so offering the button
  // before there is one would promise something that cannot work.
  it("will not add a duckling before there is a provider", async () => {
    const client = clientWith([], []);
    render(<Ducklings client={client} />);
    await waitFor(() =>
      expect(screen.getByTestId("duckling-add").hasAttribute("disabled")).toBe(true),
    );
    expect(screen.getByText(/Add a provider first/)).toBeTruthy();
  });

  it("adds a duckling with roles and capabilities", async () => {
    const client = clientWith([], [provider({ id: "openrouter" })]);
    render(<Ducklings client={client} />);
    fireEvent.click(await screen.findByTestId("duckling-add"));
    fireEvent.change(screen.getByTestId("duckling-id"), { target: { value: "pato-sonnet" } });
    fireEvent.change(screen.getByTestId("duckling-model"), {
      target: { value: "anthropic/claude-sonnet-4.5" },
    });
    fireEvent.change(screen.getByTestId("duckling-context"), { target: { value: "200000" } });
    fireEvent.change(screen.getByTestId("duckling-cost-out"), { target: { value: "15" } });
    fireEvent.click(screen.getByTestId("duckling-role-reviewer"));
    fireEvent.click(screen.getByTestId("duckling-save"));

    await waitFor(() =>
      expect(client.ducklingSet).toHaveBeenCalledWith("pato-sonnet", {
        provider: "openrouter",
        model: "anthropic/claude-sonnet-4.5",
        roles: ["reviewer"],
        caps: { native_tools: true, context_tokens: 200000 },
        cost: { input_per_mtok: 0, output_per_mtok: 15 },
      }),
    );
  });

  // Runs and reports are recorded under the id, so changing it would orphan
  // every measurement already taken.
  it("will not let an existing duckling be renamed", async () => {
    const client = clientWith([duckling({ id: "pato-local" })], [provider({ id: "local" })]);
    render(<Ducklings client={client} />);
    fireEvent.click(await screen.findByTestId("duckling-edit-pato-local"));
    expect(screen.getByTestId("duckling-id").hasAttribute("disabled")).toBe(true);
  });

  it("shows the engine's refusal rather than failing silently", async () => {
    const client = clientWith([], [provider({ id: "local" })]);
    (client.providerRemove as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error('provider "local" is used by [pato-local]'),
    );
    render(<Ducklings client={client} />);
    fireEvent.click(await screen.findByTestId("provider-remove-local"));
    expect((await screen.findByTestId("fleet-error")).textContent).toContain("is used by");
  });
});
