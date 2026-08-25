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
    engineDefaults: vi.fn(() => Promise.resolve({ max_concurrent_runs: 2, cpu_ceiling: 8 })),
    engineDefaultsSet: vi.fn((v: unknown) => Promise.resolve(v)),
    ducklings: vi.fn(() =>
      Promise.resolve([
        { id: "pato-atom", provider: "aitopatom", model: "q" },
        { id: "pato-sonnet", provider: "openrouter", model: "s" },
      ]),
    ),
    runs: vi.fn(() => Promise.resolve([])),
    ...over,
  }) as unknown as EngineClient;

const settings = (client: EngineClient) => (
  <Settings theme="system" onTheme={() => {}} engineVersion="0.4.0" connection="open" client={client} />
);

describe("Settings roster removal", () => {
  it("does not render roster controls or send role pins", async () => {
    const client = clientWith();
    render(settings(client));
    await waitFor(() => screen.getByTestId("settings-nav-ducklings"));
    expect(screen.queryByTestId("settings-nav-team")).not.toBeInTheDocument();
    expect(screen.queryByTestId(/^fn-/)).not.toBeInTheDocument();
    expect(screen.queryByTestId(/^roster-select-/)).not.toBeInTheDocument();
    expect(screen.getByTestId("settings-nav-ducklings")).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(screen.getByTestId("settings-nav-budgets"));
    await waitFor(() => screen.getByTestId("budget-max_tokens"));
    fireEvent.click(screen.getByTestId("settings-save"));
    await waitFor(() => expect(client.modeDefaultsSet).toHaveBeenCalled());
    for (const call of (client.modeDefaultsSet as unknown as { mock: { calls: unknown[][] } }).mock.calls) {
      expect(JSON.stringify(call[0])).not.toMatch(/role[_-]?pins?/i);
    }
  });

  it("keeps verification in a surviving section", async () => {
    const client = clientWith({
      projectGate: vi.fn(() => Promise.resolve({ command: "make test", setup: "make setup", link_deps: ["frontend"] })),
    } as unknown as Partial<EngineClient>);
    render(<Settings theme="system" onTheme={() => {}} engineVersion="0.4.0" connection="open" client={client} projectId="p" />);
    fireEvent.click(screen.getByTestId("settings-nav-autopilot"));
    await waitFor(() => expect(screen.getByTestId("gate-preparation")).toBeInTheDocument());
    expect(screen.getByTestId("gate-preparation").textContent).toContain("make test");
  });
});

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
  it("shows recent ceiling hits, a suggested adjustment, and where money went", async () => {
    const now = new Date();
    const recent = (status: "done" | "failed", usd: number, accepted?: boolean) => ({
      id: `${status}-${usd}`,
      project_id: "p",
      stage: "build",
      mode: "solo",
      task_id: "T-1",
      status,
      verdict: accepted ? "accepted" : "rejected",
      accepted,
      started_at: new Date(now.getTime() - 2 * 24 * 60 * 60 * 1000).toISOString(),
      budget: {
        usd,
        tokens: 400000,
        turns: 24,
        wallclock_s: 3600,
        limit: { usd: 2, tokens: 400000, turns: 24, wallclock_s: 3600 },
      },
    });
    const client = clientWith({
      runs: vi.fn(() => Promise.resolve([
        recent("done", 1.25, true),
        recent("done", 0.75, false),
        recent("failed", 0.5),
      ])),
    } as Partial<EngineClient>);
    render(settings(client));
    fireEvent.click(screen.getByTestId("settings-nav-budgets"));

    await waitFor(() => expect(screen.getByTestId("budget-money")).toBeInTheDocument());
    expect(screen.getByTestId("budget-hits").textContent).toContain("3 runs hit this ceiling in the last 30 days (tokens)");
    expect(screen.getByTestId("budget-hits").textContent).toContain("Suggested adjustment: consider raising the tokens ceiling.");
    expect(screen.getByTestId("budget-money").textContent).toContain("accepted work $1.25 / rejected work $0.7500 / failed runs $0.5000");
    expect(screen.getByTestId("budget-activity").textContent).toContain("Figures cover finished runs in the last 30 days.");
  });

  it("hides zero-hit ceilings and gates suggestions until two hits", async () => {
    const client = clientWith({
      runs: vi.fn(() => Promise.resolve([{
        id: "one-hit",
        project_id: "p",
        stage: "build",
        mode: "solo",
        task_id: "T-1",
        status: "done",
        verdict: "rejected",
        started_at: new Date().toISOString(),
        budget: {
          usd: 0,
          tokens: 400000,
          turns: 1,
          wallclock_s: 1,
          limit: { usd: 2, tokens: 400000, turns: 24, wallclock_s: 3600 },
        },
      }])) ,
    } as Partial<EngineClient>);
    render(settings(client));
    fireEvent.click(screen.getByTestId("settings-nav-budgets"));

    await waitFor(() => expect(screen.getByTestId("budget-hits")).toBeInTheDocument());
    expect(screen.getByTestId("budget-hits").textContent).toContain("1 runs hit this ceiling in the last 30 days (tokens)");
    expect(screen.getByTestId("budget-hits").textContent).not.toContain("0 runs hit this ceiling");
    expect(screen.getByTestId("budget-hits").textContent).not.toContain("Suggested adjustment");
  });

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
describe("engine concurrency in Settings", () => {
  it("round-trips a raised engine cap through the settings save path", async () => {
    const client = clientWith();
    render(settings(client));
    fireEvent.click(screen.getByTestId("settings-nav-engine"));
    await waitFor(() => screen.getByTestId("engine-max-concurrent"));
    expect((screen.getByTestId("engine-max-concurrent") as HTMLInputElement).value).toBe("2");
    expect(screen.getByTestId("engine-concurrency").textContent).toContain("CPU ceiling is 8");
    fireEvent.change(screen.getByTestId("engine-max-concurrent"), { target: { value: "6" } });
    fireEvent.click(screen.getByTestId("settings-save"));
    await waitFor(() => expect(client.engineDefaultsSet).toHaveBeenCalledWith({
      max_concurrent_runs: 6,
      cpu_ceiling: 8,
    }));
    expect((screen.getByTestId("engine-max-concurrent") as HTMLInputElement).value).toBe("6");
  });
});

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
  it("lets build and test defaults be picked, explains the project fallback, and saves both choices", async () => {
    const client = clientWith({
      modeDefaults: vi.fn(() =>
        Promise.resolve({
          rounds: { pair: 5 },
          agent_max_turns: 24,
          build_mode: "pair",
          test_mode: "solo",
          script_rounds: { solo: 3, pair: 3, tournament: 1, split: 1 },
          role_turns: {},
          script_role_turns: { implementer: 24 },
        }),
      ),
    });
    render(settings(client));

    const build = (await screen.findByLabelText("build runs open in")) as HTMLSelectElement;
    const test = screen.getByLabelText("test runs open in") as HTMLSelectElement;
    expect(build.value).toBe("pair");
    expect(test.value).toBe("solo");
    expect([...build.options].map((option) => option.value)).toEqual(["", "solo", "pair", "tournament", "split"]);
    expect(screen.getByTestId("config-settings").textContent).toContain("config.toml-only for now");

    fireEvent.change(build, { target: { value: "tournament" } });
    fireEvent.change(test, { target: { value: "split" } });
    fireEvent.click(screen.getByTestId("settings-save"));

    await waitFor(() => expect(client.modeDefaultsSet).toHaveBeenCalled());
    const [body] = (client.modeDefaultsSet as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]!;
    expect(body).toMatchObject({ build_mode: "tournament", test_mode: "split" });
  });
});


// Role pins belong exclusively to the Roster board. Settings must neither expose
// a second editor nor include pins when its unrelated defaults are saved.
describe("role assignments are not editable in Settings", () => {
  it("removes the team landing section, keeps verification visible elsewhere, and never saves role pins", async () => {
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
      projectGate: vi.fn(() =>
        Promise.resolve({
          command: "go test ./...",
          setup: "npm ci",
          link_deps: ["frontend"],
        }),
      ) as never,
    } as unknown as Partial<EngineClient>);
    const { container } = render(
      <Settings theme="system" onTheme={() => {}} engineVersion="1" connection="open" client={client} projectId="p" />,
    );

    await waitFor(() => screen.getByTestId("settings-save"));
    expect(screen.queryByTestId("settings-nav-team")).toBeNull();
    expect(screen.getByTestId("settings-nav-ducklings")).toHaveAttribute("aria-pressed", "true");
    expect(container.querySelector('[data-testid^="fn-"]')).toBeNull();
    expect(container.querySelector('[data-testid^="roster-select-"]')).toBeNull();

    // Verification remains usable, rather than disappearing with the team panel.
    const remainingSections = ["ducklings", "fleet", "budgets", "autopilot", "appearance", "engine"];
    let verificationVisible = false;
    for (const section of remainingSections) {
      fireEvent.click(screen.getByTestId(`settings-nav-${section}`));
      const card = screen.getByTestId("gate-preparation");
      if (section === "autopilot") {
        verificationVisible = true;
        expect(card.textContent).toContain("gate: go test ./...");
        expect(card.textContent).toContain("setup: npm ci");
        expect(card.textContent).toContain("link dependencies: frontend");
        break;
      }
    }
    expect(verificationVisible).toBe(true);

    fireEvent.click(screen.getByTestId("settings-save"));
    await waitFor(() => expect(client.modeDefaultsSet).toHaveBeenCalled());
    const [body] = (client.modeDefaultsSet as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]!;
    expect(body).not.toHaveProperty("roster");
    expect(body).not.toHaveProperty("role_pins");
    expect(rosterSet).not.toHaveBeenCalled();
  });
});
