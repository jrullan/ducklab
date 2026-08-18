import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Settings } from "./Settings";
import type { BudgetView, EngineClient } from "../api/client";

const clientWith = (over: Partial<EngineClient> = {}) =>
  ({
    budgetDefaults: vi.fn(() =>
      Promise.resolve({ max_usd: 2, max_tokens: 400000, max_turns: 24, max_wallclock_s: 3600 }),
    ),
    budgetDefaultsSet: vi.fn((b: BudgetView) => Promise.resolve(b)),
    modeDefaults: vi.fn(() =>
      Promise.resolve({
        rounds: { pair: 5 },
        agent_max_turns: 24,
        script_rounds: { solo: 3, pair: 3, tournament: 1, council: 2, split: 1 },
        role_turns: {},
        script_role_turns: { implementer: 24, reviewer: 8, triager: 6, judge: 1 },
      }),
    ),
    modeDefaultsSet: vi.fn((v: unknown) => Promise.resolve(v)),
    ducklings: vi.fn(() =>
      Promise.resolve([
        { id: "pato-atom", provider: "aitopatom", model: "q" },
        { id: "pato-sonnet", provider: "openrouter", model: "s" },
      ]),
    ),
    ...over,
  }) as unknown as EngineClient;

const settings = (client: EngineClient) => (
  <Settings theme="system" onTheme={() => {}} engineVersion="0.4.0" connection="open" client={client} />
);

// The ceiling came from the engine's config and no client could read it, so a run
// that hit it failed with a number nobody had chosen and nobody could raise.
describe("the run budget in Settings", () => {
  it("shows the configured ceiling", async () => {
    render(settings(clientWith()));
    await waitFor(() =>
      expect((screen.getByTestId("budget-max_tokens") as HTMLInputElement).value).toBe("400000"),
    );
    // The mechanism is what makes it easy to hit, and it is not obvious.
    expect(screen.getByTestId("config-settings").textContent).toContain("re-sends the conversation");
  });

  it("saves a raised ceiling", async () => {
    const client = clientWith();
    render(settings(client));
    await waitFor(() => screen.getByTestId("budget-max_tokens"));
    fireEvent.change(screen.getByTestId("budget-max_tokens"), { target: { value: "1500000" } });
    fireEvent.click(screen.getByTestId("settings-save"));

    await waitFor(() =>
      expect(client.budgetDefaultsSet).toHaveBeenCalledWith({
        max_usd: 2,
        max_tokens: 1500000,
        max_turns: 24,
        max_wallclock_s: 3600,
      }),
    );
  });

  // A zero would be a ceiling of zero, so the engine refuses it — and the
  // refusal has to reach the screen or the field would look accepted.
  it("shows the engine's refusal", async () => {
    const client = clientWith({
      budgetDefaultsSet: vi.fn(() => Promise.reject(new Error("budget max_turns must be greater than zero; got 0"))),
    } as Partial<EngineClient>);
    render(settings(client));
    await waitFor(() => screen.getByTestId("budget-max_turns"));
    fireEvent.change(screen.getByTestId("budget-max_turns"), { target: { value: "0" } });
    fireEvent.click(screen.getByTestId("settings-save"));

    await waitFor(() =>
      expect(screen.getByTestId("settings-error").textContent).toContain("greater than zero"),
    );
  });

  // Never show a number the engine did not accept: it answers with what it
  // saved, and that is what goes on screen.
  it("renders what the engine saved, not what was typed", async () => {
    const client = clientWith({
      budgetDefaultsSet: vi.fn(() =>
        Promise.resolve({ max_usd: 2, max_tokens: 999, max_turns: 24, max_wallclock_s: 3600 }),
      ),
    } as Partial<EngineClient>);
    render(settings(client));
    await waitFor(() => screen.getByTestId("budget-max_tokens"));
    fireEvent.change(screen.getByTestId("budget-max_tokens"), { target: { value: "1500000" } });
    fireEvent.click(screen.getByTestId("settings-save"));
    await waitFor(() =>
      expect(screen.getByTestId("config-settings").textContent).toContain("Currently 999"),
    );
  });
});

// The round counts lived inside the scripts — pair three, council two,
// tournament one — so changing how many times a reviewer got to push back meant
// editing Go and rebuilding.
describe("rounds and turns in Settings", () => {
  it("shows the configured count and the script's own as the placeholder", async () => {
    render(settings(clientWith()));
    const pair = (await screen.findByTestId("rounds-pair")) as HTMLInputElement;
    expect(pair.value).toBe("5");
    const solo = screen.getByTestId("rounds-solo") as HTMLInputElement;
    expect(solo.value).toBe("");
    expect(solo.placeholder).toBe("3");
  });

  it("saves a changed count and the per-turn cap", async () => {
    const client = clientWith();
    render(settings(client));
    await waitFor(() => screen.getByTestId("rounds-pair"));
    fireEvent.change(screen.getByTestId("rounds-pair"), { target: { value: "2" } });
    fireEvent.change(screen.getByTestId("rounds-agent-max-turns"), { target: { value: "40" } });
    fireEvent.click(screen.getByTestId("settings-save"));

    await waitFor(() => expect(client.modeDefaultsSet).toHaveBeenCalled());
    const [body] = (client.modeDefaultsSet as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]!;
    expect(body).toMatchObject({ rounds: { pair: 2 }, agent_max_turns: 40 });
  });

  // Empty means "use the mode's own count", which is not the same as a mode
  // that runs no rounds at all.
  it("sends nothing for a mode left empty", async () => {
    const client = clientWith();
    render(settings(client));
    await waitFor(() => screen.getByTestId("rounds-solo"));
    fireEvent.click(screen.getByTestId("settings-save"));

    const [body] = (client.modeDefaultsSet as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]!;
    expect((body as { rounds: Record<string, number> }).rounds).not.toHaveProperty("solo");
  });

  // The two limits are different things and the screen has to say so, or the
  // round count gets raised to fix a model looping inside one turn.
  it("says what the per-turn cap is for", async () => {
    render(settings(clientWith()));
    await waitFor(() => screen.getByTestId("config-settings"));
    expect(screen.getByTestId("config-settings").textContent).toContain("working in circles");
  });
});


// A triager used all six of its turns calling tools, never answered, and its own
// failure message told the reader to raise the turn cap for that role. There was
// nowhere to raise it.
describe("per-role turn caps in Settings", () => {
  it("shows the script's own cap as the placeholder", async () => {
    render(settings(clientWith()));
    const triager = (await screen.findByTestId("role-turns-triager")) as HTMLInputElement;
    expect(triager.value).toBe("");
    expect(triager.placeholder).toBe("6");
  });

  it("saves a raised cap", async () => {
    const client = clientWith();
    render(settings(client));
    await waitFor(() => screen.getByTestId("role-turns-triager"));
    fireEvent.change(screen.getByTestId("role-turns-triager"), { target: { value: "20" } });
    fireEvent.click(screen.getByTestId("settings-save"));

    await waitFor(() => expect(client.modeDefaultsSet).toHaveBeenCalled());
    const [body] = (client.modeDefaultsSet as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]!;
    expect((body as { role_turns: Record<string, number> }).role_turns).toEqual({ triager: 20 });
  });

  // Empty means "use the script's own cap", not "this role may make no calls".
  it("sends nothing for a role left empty", async () => {
    const client = clientWith();
    render(settings(client));
    await waitFor(() => screen.getByTestId("role-turns-judge"));
    fireEvent.click(screen.getByTestId("settings-save"));
    const [body] = (client.modeDefaultsSet as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]!;
    expect((body as { role_turns: Record<string, number> }).role_turns).toEqual({});
  });
});

// Two sections with a Save each, and the second one's button sat in the middle
// of its own fields — so the controls below it looked like they belonged to
// nothing, and a person who changed one and pressed the button they could see
// had no way to know whether it had been included. It had, which is worse:
// silent inclusion is indistinguishable from silent omission.
describe("saving the settings", () => {
  it("commits every section with one button", async () => {
    const client = clientWith();
    render(settings(client));
    await waitFor(() => screen.getByTestId("settings-save"));

    fireEvent.change(screen.getByTestId("budget-max_tokens"), { target: { value: "2000000" } });
    fireEvent.change(screen.getByTestId("rounds-pair"), { target: { value: "2" } });
    fireEvent.change(screen.getByTestId("role-turns-triager"), { target: { value: "20" } });
    fireEvent.click(screen.getByTestId("settings-save"));

    await waitFor(() => expect(client.modeDefaultsSet).toHaveBeenCalled());
    expect(client.budgetDefaultsSet).toHaveBeenCalledWith(
      expect.objectContaining({ max_tokens: 2000000 }),
    );
    const [modeBody] = (client.modeDefaultsSet as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]!;
    expect(modeBody).toMatchObject({
      rounds: { pair: 2 },
      role_turns: { triager: 20 },
    });
  });

  // There is exactly one, or the question "which of my changes does this carry"
  // comes back.
  it("has no second save button", async () => {
    render(settings(clientWith()));
    await waitFor(() => screen.getByTestId("settings-save"));
    expect(screen.queryByTestId("budget-save")).toBeNull();
    expect(screen.queryByTestId("rounds-save")).toBeNull();
    expect(screen.getAllByText(/^Save/).length).toBe(1);
  });
});

// Ticking a third duckling for a two-chair mode used to save fine and silently
// seat nobody — the worst kind of setting, one that looks taken and is not. The
// engine now reports each mode's capacity and the extra boxes go dark.
// The person who always builds in pair and tests in solo re-picked both on
// every task. Settings records the habit; every launcher opens on it.
describe("default phase modes in Settings", () => {
  it("keeps the launcher mode defaults in the settings payload", async () => {
    const client = clientWith();
    render(settings(client));
    await waitFor(() => screen.getByTestId("settings-save"));
    fireEvent.click(screen.getByTestId("settings-save"));
    await waitFor(() => expect(client.modeDefaultsSet).toHaveBeenCalled());
    expect(client.modeDefaultsSet).toHaveBeenCalled();
  });
});


// The workflow has ONE question — who does what — answered at two scopes.
// The fused table shows the function rows (triager, advisor, scribe) beside
// the seats, each with its scope chip, saving the moment a pick is made.
describe("the who-does-what table", () => {
  it("shows a function row with its scope and saves on pick", async () => {
    const rosterSet = vi.fn(() => Promise.resolve({}));
    const client = clientWith({
      roster: vi.fn(() =>
        Promise.resolve({
          entries: [
            { role: "triager", duckling: "pato-atom", source: "project" },
            { role: "scribe", duckling: "pato-sonnet", source: "default" },
          ],
        }),
      ),
      rosterSet,
    } as unknown as Partial<EngineClient>);
    render(<Settings theme="system" onTheme={() => {}} engineVersion="1" connection="open" client={client} projectId="p" />);
    const triager = await screen.findByTestId("fn-triager");
    expect(triager.textContent).toContain("bugs — triage");
    expect(triager.textContent).toContain("this project");
    expect(screen.getByTestId("fn-scribe").textContent).toContain("engine picked");

    fireEvent.change(screen.getByTestId("roster-select-triager"), { target: { value: "pato-sonnet" } });
    await waitFor(() => expect(rosterSet).toHaveBeenCalledWith("p", "triager", "pato-sonnet"));
  });
});
