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
    render(<Ducklings client={client} projectId="" />);
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
    render(<Ducklings client={client} projectId="" />);
    expect((await screen.findByTestId("duckling-card-pato-sonnet")).textContent).toContain(
      "OPENROUTER_API_KEY not set",
    );
    expect((await screen.findByTestId("provider-row-openrouter")).textContent).toContain("not set");
  });

  it("says nothing about keys for a local provider that needs none", async () => {
    const client = clientWith([], [provider({ id: "local" })]);
    render(<Ducklings client={client} projectId="" />);
    expect((await screen.findByTestId("provider-row-local")).textContent).toContain("no key needed");
  });

  // I10 in the interface, not only in the engine: there is nowhere to type a
  // key, so nobody can be led into pasting one.
  it("offers no field for a key, only for the variable's name", async () => {
    const client = clientWith([], []);
    render(<Ducklings client={client} projectId="" />);
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
    render(<Ducklings client={client} projectId="" />);
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

  it("shows effective local, remote, and configured provider caps", async () => {
    const client = clientWith([], [
      provider({ id: "local", base_url: "http://192.168.1.4:8080" }),
      provider({ id: "remote", base_url: "https://api.example.com/v1" }),
      provider({ id: "configured", max_concurrent: 1 }),
    ]);
    render(<Ducklings client={client} projectId="" />);
    expect((await screen.findByTestId("provider-row-local")).textContent).toContain("1 at a time — local default");
    expect(screen.getByTestId("provider-row-remote").textContent).toContain("8 at a time — remote default");
    expect(screen.getByTestId("provider-row-configured").textContent).toContain("1 concurrent run");
  });

  it("round-trips a provider concurrency cap while an unrelated field is edited", async () => {
    const existing = {
      ...provider({ id: "openrouter", api_key_env: "OPENROUTER_API_KEY" }),
      max_concurrent: 3,
    } as unknown as ProviderView;
    const client = clientWith([], [existing]);
    render(<Ducklings client={client} projectId="" />);
    fireEvent.click(await screen.findByTestId("provider-edit-openrouter"));
    const cap = screen.getByTestId("provider-max-concurrent") as HTMLInputElement;
    expect(cap.value).toBe("3");
    expect(screen.getByTestId("provider-form").textContent).toMatch(/concurrent runs.*blank.*1 local.*8 remote/i);
    fireEvent.change(screen.getByTestId("provider-url"), { target: { value: "https://changed.example/v1" } });
    fireEvent.click(screen.getByTestId("provider-save"));
    await waitFor(() =>
      expect(client.providerSet).toHaveBeenCalledWith("openrouter", expect.objectContaining({
        base_url: "https://changed.example/v1",
        max_concurrent: 3,
      })),
    );
  });

  // A duckling is a model reached through a provider, so offering the button
  // before there is one would promise something that cannot work.
  it("will not add a duckling before there is a provider", async () => {
    const client = clientWith([], []);
    render(<Ducklings client={client} projectId="" />);
    await waitFor(() =>
      expect(screen.getByTestId("duckling-add").hasAttribute("disabled")).toBe(true),
    );
    expect(screen.getByText(/Add a provider first/)).toBeTruthy();
  });

  it("adds a duckling with roles and capabilities", async () => {
    const client = clientWith([], [provider({ id: "openrouter" })]);
    render(<Ducklings client={client} projectId="" />);
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
        // Preserved fields ride every save: PUT replaces the whole duckling,
        // and the form once wiped what it did not show.
        notes: "",
        params: {
          max_tokens: null,
          temperature: null,
          top_p: null,
          disable_thinking: false,
          stop: null,
        },
        color: 0,
        caps: { native_tools: true, context_tokens: 200000, vision: false },
        cost: { input_per_mtok: 0, output_per_mtok: 15 },
      }),
    );
  });

  // The engine has accepted sampling params all along. The form sent no
  // `params` at all, so max_tokens and disable_thinking were reachable only by
  // hand-editing config.toml — which is not a thing a desktop-only user does.
  it("sends max tokens and thinking suppression", async () => {
    const client = clientWith([], [provider({ id: "openrouter" })]);
    render(<Ducklings client={client} projectId="" />);
    fireEvent.click(await screen.findByTestId("duckling-add"));
    fireEvent.change(screen.getByTestId("duckling-id"), { target: { value: "pato-deepseek" } });
    fireEvent.change(screen.getByTestId("duckling-model"), { target: { value: "deepseek-v4-pro" } });
    fireEvent.change(screen.getByTestId("duckling-max-tokens"), { target: { value: "32000" } });
    fireEvent.click(screen.getByTestId("duckling-disable-thinking"));
    fireEvent.click(screen.getByTestId("duckling-save"));

    await waitFor(() => expect(client.ducklingSet).toHaveBeenCalled());
    const [, body] = (client.ducklingSet as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]!;
    expect((body as { params: Record<string, unknown> }).params).toMatchObject({
      max_tokens: 32000,
      disable_thinking: true,
    });
  });

  // And posting an empty params wiped whatever a hand-edit had put there, so
  // editing a duckling to change its cost silently changed how it generates.
  it("keeps the params it was not asked to change", async () => {
    const existing = duckling({
      id: "pato-deepseek",
      params: { max_tokens: 32000, disable_thinking: true, top_p: 0.95 },
    });
    const client = clientWith([existing], [provider({ id: "local" })]);
    render(<Ducklings client={client} projectId="" />);
    fireEvent.click(await screen.findByTestId("duckling-edit-pato-deepseek"));
    fireEvent.change(screen.getByTestId("duckling-cost-out"), { target: { value: "3" } });
    fireEvent.click(screen.getByTestId("duckling-save"));

    await waitFor(() => expect(client.ducklingSet).toHaveBeenCalled());
    const [, body] = (client.ducklingSet as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]!;
    expect((body as { params: Record<string, unknown> }).params).toMatchObject({
      max_tokens: 32000,
      disable_thinking: true,
      top_p: 0.95,
    });
  });

  // The first thing to check when a run burns tokens and writes nothing, so it
  // belongs on the card and not only behind an edit click.
  it("shows on the card whether thinking is suppressed", async () => {
    const client = clientWith(
      [duckling({ id: "pato-deepseek", params: { disable_thinking: true } })],
      [provider({ id: "local" })],
    );
    render(<Ducklings client={client} projectId="" />);
    const card = await screen.findByTestId("duckling-card-pato-deepseek");
    expect(card.textContent).toContain("suppressed");
    expect(card.textContent).toContain("8,192 (default)");
  });

  // Runs and reports are recorded under the id, so changing it would orphan
  // every measurement already taken.
  it("will not let an existing duckling be renamed", async () => {
    const client = clientWith([duckling({ id: "pato-local" })], [provider({ id: "local" })]);
    render(<Ducklings client={client} projectId="" />);
    fireEvent.click(await screen.findByTestId("duckling-edit-pato-local"));
    expect(screen.getByTestId("duckling-id").hasAttribute("disabled")).toBe(true);
  });

  it("shows the engine's refusal rather than failing silently", async () => {
    const client = clientWith([], [provider({ id: "local" })]);
    (client.providerRemove as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error('provider "local" is used by [pato-local]'),
    );
    render(<Ducklings client={client} projectId="" />);
    fireEvent.click(await screen.findByTestId("provider-remove-local"));
    expect((await screen.findByTestId("fleet-error")).textContent).toContain("is used by");
  });
});

describe("Ducklings", () => {
  // The colour was a duckling's index in whatever list a view had to hand: the
  // run's roster in a transcript, the fleet listing here. So one model came out
  // blue as an architect and orange as an implementer, and the fleet page —
  // ordered by a Go map — reshuffled every colour on reload.
  it("sends the chosen colour slot", async () => {
    const client = clientWith([], [provider({ id: "local" })]);
    render(<Ducklings client={client} projectId="" />);
    fireEvent.click(await screen.findByTestId("duckling-add"));
    fireEvent.change(screen.getByTestId("duckling-id"), { target: { value: "pato-x" } });
    fireEvent.change(screen.getByTestId("duckling-model"), { target: { value: "m" } });
    fireEvent.click(screen.getByTestId("duckling-color-5"));
    fireEvent.click(screen.getByTestId("duckling-save"));

    await waitFor(() => expect(client.ducklingSet).toHaveBeenCalled());
    const [, body] = (client.ducklingSet as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]!;
    expect((body as { color: number }).color).toBe(5);
  });

  it("offers auto as well as the eight slots", async () => {
    const client = clientWith([], [provider({ id: "local" })]);
    render(<Ducklings client={client} projectId="" />);
    fireEvent.click(await screen.findByTestId("duckling-add"));
    expect(screen.getByTestId("duckling-color-0").getAttribute("aria-pressed")).toBe("true");
    expect(screen.queryByTestId("duckling-color-9")).toBeNull();
  });
});

// Vision was preserved but invisible: the flag that decides whether a
// triager SEES a bug's screenshots could not be set anywhere in the UI —
// stored screenshots silently reached no model. The editor now exposes it.
describe("the vision capability", () => {
  it("saves the toggle into caps", async () => {
    const client = clientWith([], [{ id: "openrouter", base_url: "u", key_env: "K", key_set: true } as never]);
    render(<Ducklings client={client} projectId="" />);
    fireEvent.click(await screen.findByTestId("duckling-add"));
    fireEvent.change(screen.getByTestId("duckling-id"), { target: { value: "seer" } });
    fireEvent.change(screen.getByLabelText("model"), { target: { value: "m" } });
    fireEvent.click(screen.getByTestId("duckling-vision"));
    fireEvent.click(screen.getByText("Save"));
    await waitFor(() => expect(client.ducklingSet).toHaveBeenCalled());
    const [, body] = (client.ducklingSet as ReturnType<typeof vi.fn>).mock.calls[0]! as unknown as [
      string,
      { caps: { vision: boolean } },
    ];
    expect(body.caps.vision).toBe(true);
  });
});

// Editing used to replace a card inside the collection, changing the grid
// while the person worked. The drawer keeps the fleet stable and visible.
describe("editing without moving the fleet", () => {
  it("opens the form in a drawer and preserves every card", async () => {
    const client = clientWith(
      [
        { id: "a1", provider: "openrouter", model: "m1" } as never,
        { id: "b2", provider: "openrouter", model: "m2" } as never,
      ],
      [{ id: "openrouter", base_url: "u", key_env: "K", key_set: true } as never],
    );
    render(<Ducklings client={client} projectId="" />);
    fireEvent.click(await screen.findByTestId("duckling-edit-b2"));
    expect(screen.getByTestId("duckling-edit-inplace")).toBeTruthy();
    expect(screen.getByRole("dialog", { name: "Edit b2" })).toBeTruthy();
    expect(screen.getByTestId("duckling-card-b2")).toBeTruthy();
    expect(screen.getByTestId("duckling-card-a1")).toBeTruthy();
  });
});
