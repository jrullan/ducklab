import { useEffect, useState } from "react";
import { applyTheme, saveTheme, type Theme } from "../app/theme";
import { seatLabel } from "../lib/seats";
import { StatusChip } from "../components/StatusChip";
import type { BudgetView, EngineClient, ModeDefaultsView } from "../api/client";

/**
 * Settings. Secrets are never displayed: a key field shows whether it is set
 * and the env var it reads, never the value (07 §4.9).
 */
export function Settings({
  theme, onTheme, engineVersion, connection, client,
}: {
  theme: Theme;
  onTheme: (t: Theme) => void;
  engineVersion: string;
  connection: string;
  /** Optional so a Settings screen still renders with no engine to ask. */
  client?: EngineClient;
}) {
  const change = (t: Theme) => {
    applyTheme(t);
    saveTheme(t);
    onTheme(t);
  };
  return (
    <div className="p-4" data-testid="settings">
      <section>
        <h2 className="text-sm text-ink-muted">theme</h2>
        <div className="mt-1 flex gap-2">
          {(["light", "dark", "system"] as Theme[]).map((t) => (
            <button
              key={t}
              type="button"
              onClick={() => change(t)}
              data-testid={`theme-${t}`}
              aria-pressed={theme === t}
              className={`rounded border border-hairline px-2 py-1 text-sm ${theme === t ? "text-ink" : "text-ink-muted"}`}
            >
              {t}
            </button>
          ))}
        </div>
      </section>

      {client && <ConfigSection client={client} />}

      <section className="mt-4">
        <h2 className="text-sm text-ink-muted">engine</h2>
        <div className="mt-1 flex items-center gap-3">
          <StatusChip
            role={connection === "open" ? "good" : connection === "reconnecting" ? "warning" : "critical"}
            label={connection}
          />
          <span className="text-ink-secondary">version {engineVersion || "unknown"}</span>
        </div>
        <p className="mt-2 text-sm text-ink-muted">
          API keys are read from environment variables and are never stored or displayed here.
        </p>
      </section>
    </div>
  );
}

/**
 * Everything the engine keeps, on one page, saved by one button.
 *
 * It used to be two sections with a Save each, and the second one's button sat
 * in the middle of its own fields — so the controls below it looked like they
 * belonged to nothing, and a person who changed one of them and pressed the
 * button they could see had no way to know whether it had been included.
 *
 * A settings page is one decision: this is how I want it. Splitting the commit
 * of that decision across buttons makes the reader work out which of their
 * changes each one carries, and being wrong is silent.
 */
function ConfigSection({ client }: { client: EngineClient }) {
  const [budget, setBudget] = useState<BudgetView | null>(null);
  const [modes, setModes] = useState<ModeDefaultsView | null>(null);
  const [fleet, setFleet] = useState<string[]>([]);
  // Drafts, so nothing is sent until Save and a half-typed number is never a
  // ceiling of zero.
  const [b, setB] = useState<Record<string, string>>({});
  const [rounds, setRounds] = useState<Record<string, string>>({});
  const [roleTurns, setRoleTurns] = useState<Record<string, string>>({});
  const [agentTurns, setAgentTurns] = useState("");
  const [lineups, setLineups] = useState<Record<string, string[]>>({});
  const [buildMode, setBuildMode] = useState("");
  const [testMode, setTestMode] = useState("");
  // Extra columns a person opened on an open-seated mode, beyond what the
  // saved line-up needs. UI state only; empty seats are dropped on save.
  const [extraCols, setExtraCols] = useState<Record<string, number>>({});
  const [state, setState] = useState<{ kind: "idle" | "saving" | "saved" | "error"; message?: string }>({
    kind: "idle",
  });

  const touched = () => setState({ kind: "idle" });

  // What the engine kept, applied to the drafts. Used on load and after a save,
  // so the values on screen are always the ones it has and never the ones that
  // were typed.
  const applyBudget = (v: BudgetView) => {
    setBudget(v);
    setB({
      max_usd: String(v.max_usd),
      max_tokens: String(v.max_tokens),
      max_turns: String(v.max_turns),
      max_wallclock_s: String(v.max_wallclock_s),
    });
  };
  const applyModes = (v: ModeDefaultsView) => {
    setModes(v);
    const r: Record<string, string> = {};
    for (const mode of Object.keys(v.script_rounds ?? {})) {
      r[mode] = v.rounds[mode] ? String(v.rounds[mode]) : "";
    }
    setRounds(r);
    const rt: Record<string, string> = {};
    for (const role of Object.keys(v.script_role_turns ?? {})) {
      rt[role] = v.role_turns?.[role] ? String(v.role_turns[role]) : "";
    }
    setRoleTurns(rt);
    setAgentTurns(String(v.agent_max_turns));
    setLineups(v.ducklings ?? {});
    setBuildMode(v.build_mode ?? "");
    setTestMode(v.test_mode ?? "");
  };

  const load = () => {
    client.budgetDefaults().then(applyBudget).catch((e) => setState({ kind: "error", message: String(e) }));
    client.modeDefaults().then(applyModes).catch((e) => setState({ kind: "error", message: String(e) }));
    client
      .ducklings()
      .then((ds) => setFleet(ds.map((d) => d.id)))
      .catch(() => setFleet([]));
  };

  useEffect(load, [client]);

  if (!budget || !modes) {
    return (
      <section className="mt-4" data-testid="config-settings">
        <p className="text-sm text-ink-muted">
          {state.kind === "error" ? `could not read the settings: ${state.message}` : "reading…"}
        </p>
      </section>
    );
  }

  const numbersOnly = (o: Record<string, string>) =>
    Object.fromEntries(
      Object.entries(o)
        .filter(([, v]) => v.trim() !== "")
        .map(([k, v]) => [k, Number(v) || 0]),
    );

  const save = () => {
    setState({ kind: "saving" });
    // Both, then re-read. A page with one button must not leave half of itself
    // applied, and what ends up on screen is what the engine kept — never what
    // was typed.
    Promise.all([
      client.budgetDefaultsSet({
        max_usd: Number(b.max_usd) || 0,
        max_tokens: Number(b.max_tokens) || 0,
        max_turns: Number(b.max_turns) || 0,
        max_wallclock_s: Number(b.max_wallclock_s) || 0,
      }),
      client.modeDefaultsSet({
        rounds: numbersOnly(rounds),
        agent_max_turns: Number(agentTurns) || 0,
        // Empty seats are UI scaffolding, not preferences.
        build_mode: buildMode,
        test_mode: testMode,
        ducklings: Object.fromEntries(
          Object.entries(lineups).map(([m, ids]) => [m, ids.filter(Boolean)]),
        ),
        role_turns: numbersOnly(roleTurns),
      }),
    ])
      .then(([savedBudget, savedModes]) => {
        applyBudget(savedBudget);
        applyModes(savedModes);
        setState({ kind: "saved" });
      })
      .catch((e) => setState({ kind: "error", message: e instanceof Error ? e.message : String(e) }));
  };

  const num = (
    value: string,
    onChange: (v: string) => void,
    label: string,
    testid: string,
    placeholder?: string,
    width = "w-28",
  ) => (
    <label className="flex items-center gap-1">
      {label}
      <input
        aria-label={label}
        data-testid={testid}
        placeholder={placeholder}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          touched();
        }}
        className={`${width} rounded border border-hairline bg-surface2 px-2 py-1`}
      />
    </label>
  );

  return (
    <section className="mt-4" data-testid="config-settings">
      <h2 className="text-sm text-ink-muted">run budget</h2>
      <div className="mt-1 flex flex-wrap items-center gap-3 text-sm text-ink-secondary">
        {num(b.max_tokens ?? "", (v) => setB({ ...b, max_tokens: v }), "tokens", "budget-max_tokens")}
        {num(b.max_usd ?? "", (v) => setB({ ...b, max_usd: v }), "USD", "budget-max_usd")}
        {num(b.max_turns ?? "", (v) => setB({ ...b, max_turns: v }), "turns", "budget-max_turns")}
        {num(b.max_wallclock_s ?? "", (v) => setB({ ...b, max_wallclock_s: v }), "seconds", "budget-max_wallclock_s")}
      </div>
      <p className="mt-1 text-sm text-ink-muted">
        tokens counts prompt and completion together, and every round re-sends the
        conversation — so a long task spends most of its budget on input.
        Currently {budget.max_tokens.toLocaleString()} per run.
      </p>

      <h2 className="mt-4 text-sm text-ink-muted">rounds per mode</h2>
      <div className="mt-1 flex flex-wrap items-center gap-3 text-sm text-ink-secondary">
        {Object.keys(modes.script_rounds ?? {})
          .sort()
          .map((mode) =>
            num(
              rounds[mode] ?? "",
              (v) => setRounds({ ...rounds, [mode]: v }),
              mode,
              `rounds-${mode}`,
              String(modes.script_rounds?.[mode] ?? ""),
              "w-16",
            ),
          )}
      </div>

      <h2 className="mt-4 text-sm text-ink-muted">model calls per turn</h2>
      <div className="mt-1 flex flex-wrap items-center gap-3 text-sm text-ink-secondary">
        {Object.keys(modes.script_role_turns ?? {})
          .sort()
          .map((role) =>
            num(
              roleTurns[role] ?? "",
              (v) => setRoleTurns({ ...roleTurns, [role]: v }),
              role,
              `role-turns-${role}`,
              String(modes.script_role_turns?.[role] ?? ""),
              "w-16",
            ),
          )}
        {num(agentTurns, setAgentTurns, "fallback", "rounds-agent-max-turns", undefined, "w-20")}
      </div>

      {fleet.length > 1 && (
        <>
          <h2 className="mt-4 text-sm text-ink-muted">default modes</h2>
          {/* The person who always builds in pair and tests in solo re-picked
              both on every task; the launcher should open on their habit. */}
          <div className="mt-1 flex flex-wrap items-end gap-3 text-sm text-ink-secondary" data-testid="default-modes">
            <label className="flex flex-col gap-0.5 text-xs text-ink-muted">
              build
              <select
                data-testid="default-build-mode"
                value={buildMode}
                onChange={(e) => { setBuildMode(e.target.value); touched(); }}
                className="rounded border border-hairline bg-surface2 px-2 py-1 text-sm text-ink-secondary"
              >
                <option value="">solo (unset)</option>
                {Object.keys(modes.script_rounds ?? {}).sort().map((m) => (
                  <option key={m} value={m}>{m}</option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-0.5 text-xs text-ink-muted">
              test first
              <select
                data-testid="default-test-mode"
                value={testMode}
                onChange={(e) => { setTestMode(e.target.value); touched(); }}
                className="rounded border border-hairline bg-surface2 px-2 py-1 text-sm text-ink-secondary"
              >
                <option value="">solo (unset)</option>
                <option value="solo">solo</option>
                <option value="pair">pair</option>
              </select>
            </label>
          </div>

          <h2 className="mt-4 text-sm text-ink-muted">ducklings per mode</h2>
          {/* One dropdown per SEAT, not one checkbox per duckling: a fleet of
              ten models made the old row a wall of boxes, and the row grew
              with every duckling added. Seats are the stable dimension — solo
              has one, pair exactly two, the open modes start at two and grow
              one seat at a time. Each seat names the role its position
              carries, because the position IS the assignment. */}
          <div className="mt-1 space-y-1" data-testid="mode-lineups">
            {Object.keys(modes.script_rounds ?? {})
              .sort()
              .map((mode) => {
                const seats = modes.seats?.[mode] ?? 0;
                const picked = lineups[mode] ?? [];
                const cols =
                  seats > 0
                    ? seats
                    : Math.max(2, picked.length, extraCols[mode] ?? 0);
                const setSeat = (i: number, id: string) => {
                  const next = [...picked];
                  while (next.length <= i) next.push("");
                  next[i] = id;
                  setLineups({ ...lineups, [mode]: next });
                  touched();
                };
                return (
                  <div key={mode} className="flex flex-wrap items-center gap-2 text-sm text-ink-secondary">
                    <span className="w-24 shrink-0">{mode}</span>
                    {Array.from({ length: cols }, (_, i) => (
                      <label key={i} className="flex flex-col gap-0.5 text-xs text-ink-muted">
                        {seatLabel(mode, i)}
                        <select
                          value={picked[i] ?? ""}
                          onChange={(e) => setSeat(i, e.target.value)}
                          data-testid={`seat-${mode}-${i}`}
                          className="rounded border border-hairline bg-surface2 px-1 py-0.5 text-sm text-ink-secondary"
                        >
                          <option value="">—</option>
                          {fleet
                            .filter((id) => id === picked[i] || !picked.includes(id))
                            .map((id) => (
                              <option key={id} value={id}>
                                {id}
                              </option>
                            ))}
                        </select>
                      </label>
                    ))}
                    {seats === 0 && (
                      <button
                        type="button"
                        data-testid={`seat-add-${mode}`}
                        onClick={() => setExtraCols({ ...extraCols, [mode]: cols + 1 })}
                        className="self-end rounded border border-hairline px-2 py-0.5 text-xs"
                        title="add a seat"
                      >
                        +
                      </button>
                    )}
                  </div>
                );
              })}
          </div>
        </>
      )}

      <p className="mt-2 text-sm text-ink-muted">
        A round is one pass over every participant, so pair spends two turns on
        each. "Model calls per turn" is the separate limit on one participant
        chaining tool calls — a model working in circles is stopped by that, not
        by the round count. Empty uses the built-in value shown in the box. Each
        seat names the role its position carries: solo seats one model, pair an
        implementer and its reviewer, council a drafter and a critic per
        further seat you fill.
      </p>

      {/* One button, at the end, after everything it carries. The page used to
          have two, and the second sat in the middle of its own fields. */}
      <div className="mt-3 flex items-center gap-3">
        <button
          type="button"
          onClick={save}
          data-testid="settings-save"
          disabled={state.kind === "saving"}
          className="rounded border border-hairline px-3 py-1 text-sm disabled:opacity-40"
        >
          {state.kind === "saving" ? "Saving…" : "Save settings"}
        </button>
        {state.kind === "saved" && <span className="text-sm text-good">saved</span>}
        {state.kind === "error" && (
          <span className="text-sm text-critical" data-testid="settings-error">
            {state.message}
          </span>
        )}
      </div>
    </section>
  );
}

