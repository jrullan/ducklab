import { useEffect, useState } from "react";
import { Ducklings } from "./Ducklings";
import { applyTheme, saveTheme, type Theme } from "../app/theme";
import { CHIP_FACTS, loadChipFacts, saveChipFacts, type ChipFact } from "../lib/chipfacts";
import { quack } from "../lib/attention";
import { seatLabel } from "../lib/seats";
import { StatusChip } from "../components/StatusChip";
import type { BudgetView, EngineClient, ModeDefaultsView, RosterEntry } from "../api/client";

/** Every engine-honored roster role appears here, so project.toml pins never
 * become invisible state. The task-mode selectors describe launch shape; these
 * rows describe the role defaults those launches resolve to. */
const FUNCTION_ORDER = ["architect", "implementer", "reviewer", "judge", "triager", "advisor", "scribe"];

/** The scope, as a pill the eye can file: neutral for the global defaults,
 * green for a choice this project made, amber for one the engine is making
 * on the person's behalf — an invitation to claim it. */
function ScopeChip({ scope }: { scope: "all projects" | "this project" | "engine picked" }) {
  const tone =
    scope === "this project"
      ? "border-good text-good"
      : scope === "engine picked"
        ? "border-warning text-warning"
        : "border-hairline text-ink-muted";
  return (
    <span className={`ml-auto shrink-0 rounded-full border px-2 py-0.5 text-[10px] ${tone}`} data-testid="scope-chip">
      {scope}
    </span>
  );
}

const WORKFLOW_ROWS: Record<string, { stage: string; help: string }> = {
  architect: { stage: "documents — architecture", help: "shapes the project's technical direction" },
  implementer: { stage: "tasks — implementation", help: "writes and verifies the change" },
  reviewer: { stage: "tasks — review", help: "checks the implementation" },
  judge: { stage: "tasks — verdict", help: "adjudicates between implementer and reviewer" },
  triager: { stage: "bugs — triage", help: "classifies reports: severity, duplicates, promotability" },
  advisor: { stage: "gates — advice", help: "drafts an answer while a paused run waits for you" },
  scribe: { stage: "releases — notes", help: "writes the release notes from the accepted work" },
};

/** One titled card per concern. The page was a flat column of ten unrelated
 * headings — a cognitive disaster to scan (the user's words). Cards group by
 * task, ordered by how often each is touched: team first, budgets second,
 * appearance and engine last. */
function SettingsCard({ title, desc, children, testid }: {
  title: string;
  desc?: string;
  children: React.ReactNode;
  testid?: string;
}) {
  return (
    <section className="mb-4 rounded-card border border-hairline p-4" data-testid={testid}>
      <h2 className="text-sm font-medium text-ink">{title}</h2>
      {desc && <p className="mt-0.5 text-xs text-ink-muted">{desc}</p>}
      <div className="mt-3">{children}</div>
    </section>
  );
}

/**
 * Settings. Secrets are never displayed: a key field shows whether it is set
 * and the env var it reads, never the value (07 §4.9).
 */
type SettingsSection = "team" | "ducklings" | "fleet" | "budgets" | "autopilot" | "appearance" | "engine";

const SETTINGS_SECTIONS: { id: SettingsSection; label: string }[] = [
  { id: "team", label: "your team" },
  { id: "ducklings", label: "my ducklings" },
  { id: "fleet", label: "providers" },
  { id: "budgets", label: "budgets & limits" },
  { id: "autopilot", label: "autopilot & autonomy" },
  { id: "appearance", label: "appearance & alerts" },
  { id: "engine", label: "engine" },
];

export function Settings({
  theme, onTheme, engineVersion, connection, client, projectId, onEngine, engineBusy, engineError,
}: {
  theme: Theme;
  onTheme: (t: Theme) => void;
  engineVersion: string;
  connection: string;
  /** Optional so a Settings screen still renders with no engine to ask. */
  client?: EngineClient;
  projectId?: string;
  /** Engine supervision, provided by the desktop shell. The stale banner
   * offers these when something breaks; this is the same pair on demand —
   * restart after a make install without waiting for a failure to say so. */
  onEngine?: (action: "restart" | "reconnect") => void;
  engineBusy?: boolean;
  engineError?: string | null;
}) {
  const change = (t: Theme) => {
    applyTheme(t);
    saveTheme(t);
    onTheme(t);
  };
  const [section, setSection] = useState<SettingsSection>("team");
  return (
    <div className="flex gap-6 p-4" data-testid="settings">
      {/* The sub-menu: one concern on screen at a time (the user's own
          mock). Nothing unmounts except the fleet — config state and its
          one Save survive switching via CSS visibility. */}
      <nav className="w-44 shrink-0 space-y-1" data-testid="settings-nav">
        {SETTINGS_SECTIONS.map((sec) => (
          <button
            key={sec.id}
            type="button"
            data-testid={`settings-nav-${sec.id}`}
            aria-pressed={section === sec.id}
            onClick={() => setSection(sec.id)}
            className={`block w-full rounded px-2 py-1 text-left text-sm ${
              section === sec.id ? "bg-surface2 text-ink" : "text-ink-muted"
            }`}
          >
            {sec.label}
          </button>
        ))}
      </nav>

      <div className="min-w-0 max-w-3xl flex-1">
      {/* The member cards are their own section: inside "your team" they
          buried the who-does-what table a full screen down, and the table is
          what that section exists to answer. */}
      {section === "ducklings" && client && (
        <Ducklings client={client} projectId={projectId ?? ""} only="ducklings" />
      )}
      {client && <ConfigSection client={client} section={section} projectId={projectId} />}
      {section === "fleet" && client && (
        <Ducklings client={client} projectId={projectId ?? ""} only="providers" />
      )}

      <div className={section === "appearance" ? "" : "hidden"}>
      <SettingsCard
        title="appearance & alerts"
        desc="how ducklab looks, and when it speaks up"
      >
        <div className="flex gap-2">
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
        <QuackToggle />
        <ChipFactsPicker />
      </SettingsCard>
      </div>

      <div className={section === "engine" ? "" : "hidden"}>
      <SettingsCard
        title="engine"
        desc="the process that runs everything — API keys are read from environment variables and are never stored or displayed here"
      >
        <div className="flex items-center gap-3">
          <StatusChip
            role={connection === "open" ? "good" : connection === "reconnecting" ? "warning" : "critical"}
            label={connection}
          />
          <span className="text-ink-secondary">version {engineVersion || "unknown"}</span>
        </div>
        {onEngine && (
          <div className="mt-2 flex items-center gap-2">
            <button
              type="button"
              data-testid="settings-restart-engine"
              disabled={engineBusy}
              onClick={() => onEngine("restart")}
              title="stop the engine and start the installed binary (refused while runs are going, or if this app's environment lacks the provider keys)"
              className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-50"
            >
              {engineBusy ? "Working…" : "Restart engine"}
            </button>
            <button
              type="button"
              data-testid="settings-reconnect-engine"
              disabled={engineBusy}
              onClick={() => onEngine("reconnect")}
              title="adopt the engine already running — for a window whose session an external restart left behind"
              className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-50"
            >
              Reconnect
            </button>
            {engineError && (
              <span className="text-xs text-critical" data-testid="settings-engine-error">
                {engineError}
              </span>
            )}
          </div>
        )}
      </SettingsCard>
      </div>
      </div>
    </div>
  );
}

/** The quack: on by default, silenced here — a sound that cannot be turned
 * off teaches the person to mute the whole machine. The test button exists
 * because the first real quack should not be a surprise mid-focus, and
 * because clicking it grants the audio context its user gesture. */
function QuackToggle() {
  const [on, setOn] = useState(localStorage.getItem("ducklab.quack") !== "off");
  return (
    <div className="mt-1 flex items-center gap-3">
      <label className="flex items-center gap-2 text-sm text-ink-secondary">
        <input
          type="checkbox"
          data-testid="quack-toggle"
          checked={on}
          onChange={(e) => {
            const next = e.target.checked;
            setOn(next);
            if (next) localStorage.removeItem("ducklab.quack");
            else localStorage.setItem("ducklab.quack", "off");
          }}
        />
        quack out loud when a run needs you or fails
      </label>
      <button
        type="button"
        data-testid="quack-test"
        onClick={() => quack()}
        className="rounded border border-hairline px-2 py-1 text-xs"
      >
        try it
      </button>
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
function ConfigSection({ client, section, projectId }: { client: EngineClient; section: SettingsSection; projectId?: string }) {
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

  // The autopilot's knobs ride the same single Save (the one-save pin is
  // right: two buttons re-ask "which of my changes does this carry"). Loaded
  // defensively — a client without the method must not blank the page — and
  // included in the save only once actually loaded. Declared HERE, above the
  // loading early-return: a hook below a conditional return renders a
  // different hook count per pass, which React rejects wholesale.
  const [ap, setAp] = useState<{ max_tasks: string; max_fails: string; autonomy: string } | null>(null);
  // The per-project function assignments (triager, advisor, scribe), fused
  // into the same "who does what" table as the global seats — the workflow
  // has ONE question, answered at two scopes, and showing them as two
  // unrelated widgets was the confusion itself.
  const [roster, setRoster] = useState<RosterEntry[]>([]);
  // The project's own autonomy — the level runs and triage consult FIRST.
  // It had no control anywhere; the guidance was "edit the TOML".
  const [projAutonomy, setProjAutonomy] = useState<string | null>(null);
  useEffect(() => {
    if (!projectId) return;
    Promise.resolve()
      .then(() => client.projectAutonomy(projectId))
      .then((r) => setProjAutonomy(r.autonomy))
      .catch(() => {});
  }, [client, projectId]);
  const loadRoster = () => {
    if (!projectId) return;
    Promise.resolve()
      .then(() => client.roster(projectId))
      .then((r) => setRoster(r.entries))
      .catch(() => {});
  };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(loadRoster, [client, projectId]);
  useEffect(() => {
    Promise.resolve()
      .then(() => client.autopilotDefaults())
      .then((d) => setAp({ max_tasks: String(d.max_tasks), max_fails: String(d.max_fails), autonomy: d.autonomy }))
      .catch(() => {});
  }, [client]);

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
      ap
        ? client.autopilotDefaultsSet({
            max_tasks: Number(ap.max_tasks) || 0,
            max_fails: Number(ap.max_fails) || 0,
            autonomy: ap.autonomy,
          })
        : Promise.resolve(null),
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
      .then(([savedAp, savedBudget, savedModes]) => {
        if (savedAp) {
          setAp({ max_tasks: String(savedAp.max_tasks), max_fails: String(savedAp.max_fails), autonomy: savedAp.autonomy });
        }
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
    <div data-testid="config-settings">
      <div className={section === "team" ? "" : "hidden"}>
      {fleet.length > 1 && (
        <SettingsCard
          title="your team"
          desc="who does what, in the order the workflow asks it"
        >
          {/* ONE table, ordered by the workflow itself: documents, tasks,
              bugs, gates, releases. Each stage is a row with a strong name,
              its plain-words detail, and a scope PILL on the right edge —
              the previous draft set every label in the same muted size and
              the page read as fog (the user's verdict: horrible). */}
          <div className="divide-y divide-hairline">

            <section className="py-3 first:pt-0" data-testid="stage-documents">
              <div className="flex items-baseline gap-2">
                <span className="text-sm font-medium text-ink">documents</span>
                <span className="text-xs text-ink-muted">intake → spec → plan</span>
                <ScopeChip scope="all projects" />
              </div>
              <div className="mt-2" data-testid="function-lineups">
                {(() => {
                  const mode = "council";
                  const picked = lineups[mode] ?? [];
                  const cols = Math.max(2, picked.length, extraCols[mode] ?? 0);
                  const setSeat = (i: number, id: string) => {
                    const next = [...picked];
                    while (next.length <= i) next.push("");
                    next[i] = id;
                    setLineups({ ...lineups, [mode]: next });
                    touched();
                  };
                  return (
                    <div className="flex flex-wrap items-end gap-2 text-sm text-ink-secondary">
                      <span
                        className="w-24 shrink-0 self-center text-ink-secondary"
                        title="the council drafts the documents — a drafter plus a critic per further seat"
                      >
                        council
                      </span>
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
                                <option key={id} value={id}>{id}</option>
                              ))}
                          </select>
                        </label>
                      ))}
                      <button
                        type="button"
                        data-testid={`seat-add-${mode}`}
                        onClick={() => setExtraCols({ ...extraCols, [mode]: cols + 1 })}
                        className="rounded border border-hairline px-2 py-0.5 text-xs"
                        title="add a critic seat"
                      >
                        +
                      </button>
                    </div>
                  );
                })()}
              </div>
            </section>

            <section className="py-3" data-testid="stage-tasks">
              <div className="flex items-baseline gap-2">
                <span className="text-sm font-medium text-ink">tasks</span>
                <span className="text-xs text-ink-muted">test-first & build</span>
                <ScopeChip scope="all projects" />
              </div>
              {/* The launcher's opening picks, aligned into the same label
                  column as the seat rows below — "default modes" floated as
                  its own orphan heading before. */}
              <div className="mt-2 flex flex-wrap items-end gap-2 text-sm text-ink-secondary" data-testid="default-modes">
                <span className="w-24 shrink-0 self-center" title="what the launchers open on">defaults</span>
                <label className="flex flex-col gap-0.5 text-xs text-ink-muted">
                  build
                  <select
                    data-testid="default-build-mode"
                    value={buildMode}
                    onChange={(e) => { setBuildMode(e.target.value); touched(); }}
                    className="rounded border border-hairline bg-surface2 px-2 py-1 text-sm text-ink-secondary"
                  >
                    <option value="">solo (unset)</option>
                    {Object.keys(modes.script_rounds ?? {})
                      .filter((m) => m !== "council")
                      .sort()
                      .map((m) => (
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
              <div className="mt-2 space-y-2" data-testid="mode-lineups">
                {Object.keys(modes.script_rounds ?? {})
                  .filter((m) => m !== "council")
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
                      <div key={mode} className="flex flex-wrap items-end gap-2 text-sm text-ink-secondary">
                        <span className="w-24 shrink-0 self-center">{mode}</span>
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
                                  <option key={id} value={id}>{id}</option>
                                ))}
                            </select>
                          </label>
                        ))}
                        {seats === 0 && (
                          <button
                            type="button"
                            data-testid={`seat-add-${mode}`}
                            onClick={() => setExtraCols({ ...extraCols, [mode]: cols + 1 })}
                            className="rounded border border-hairline px-2 py-0.5 text-xs"
                            title="add a seat"
                          >
                            +
                          </button>
                        )}
                      </div>
                    );
                  })}
              </div>
            </section>

            {FUNCTION_ORDER.map((role) => {
              const e = roster.find((r) => r.role === role);
              if (!e || !WORKFLOW_ROWS[role]) return null;
              const row = WORKFLOW_ROWS[role]!;
              return (
                <section key={role} className="py-3" data-testid={`fn-${role}`}>
                  <div className="flex items-baseline gap-2">
                    <span className="text-sm font-medium text-ink">{row.stage.split(" — ")[0]}</span>
                    <span className="text-xs text-ink-muted">{row.stage.split(" — ")[1]}</span>
                    {/* Hidden text keeps the stage's full name greppable and
                        pinned ("bugs — triage") while the visible header
                        splits it into name and detail. */}
                    <span className="sr-only">{row.stage}</span>
                    <ScopeChip scope={e.source === "project" ? "this project" : "engine picked"} />
                  </div>
                  <div className="mt-2 flex flex-wrap items-center gap-2 text-sm text-ink-secondary">
                    <span className="w-24 shrink-0" title={row.help}>{e.role}</span>
                    <span data-testid={`roster-value-${e.role}`}>
                      {e.source === "project" && e.default && e.default !== e.duckling
                        ? `${e.default} — overridden for ${projectId}: ${e.duckling} (project)`
                        : e.duckling}
                    </span>
                    <select
                      aria-label={`duckling for ${e.role}`}
                      data-testid={`roster-select-${e.role}`}
                      value={e.duckling}
                      onChange={(ev) =>
                        void client
                          .rosterSet(projectId!, e.role, ev.target.value)
                          .then(loadRoster)
                          .catch(() => {})
                      }
                      className="rounded border border-hairline bg-surface2 px-2 py-1 text-xs"
                    >
                      {fleet.map((id) => (
                        <option key={id} value={id}>{id}</option>
                      ))}
                    </select>
                    <span className="text-xs text-ink-muted">{row.help}</span>
                  </div>
                </section>
              );
            })}
          </div>

          <p className="mt-3 text-xs text-ink-muted">
            Seat pickers ride the Save button below; the this-project pickers
            (all function roles) apply the moment you choose.
          </p>
        </SettingsCard>
      )}
      </div>

      <div className={section === "autopilot" ? "" : "hidden"}>
        <SettingsCard
          title="autopilot & autonomy"
          desc="the unattended loop's leash, and what autonomy a run gets when nothing names one"
          testid="autopilot-defaults"
        >
          {ap ? (
            <>
            <div className="flex flex-wrap items-end gap-3 text-sm text-ink-secondary">
              <label className="flex flex-col gap-0.5 text-xs text-ink-muted">
                tasks per activation
                <input
                  data-testid="ap-max-tasks"
                  value={ap.max_tasks}
                  onChange={(e) => { setAp({ ...ap, max_tasks: e.target.value }); touched(); }}
                  className="w-24 rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
                />
              </label>
              <label className="flex flex-col gap-0.5 text-xs text-ink-muted">
                failures before stopping
                <input
                  data-testid="ap-max-fails"
                  value={ap.max_fails}
                  onChange={(e) => { setAp({ ...ap, max_fails: e.target.value }); touched(); }}
                  className="w-24 rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
                />
              </label>
            </div>

            {/* One question — how much may a run decide alone — answered at
                two scopes, chips saying which, exactly like who-does-what.
                The project's own level wins when set; it saves on pick like
                every this-project control. */}
            <h4 className="mt-4 flex items-center gap-2 text-xs text-ink-muted">
              autonomy <ScopeChip scope="all projects" />
            </h4>
            <div className="mt-1 flex items-center gap-2 text-sm text-ink-secondary">
              <span className="w-24 shrink-0">default</span>
              <select
                data-testid="ap-autonomy"
                value={ap.autonomy}
                onChange={(e) => { setAp({ ...ap, autonomy: e.target.value }); touched(); }}
                className="rounded border border-hairline bg-surface2 px-2 py-1 text-sm text-ink-secondary"
              >
                {["manual", "guarded", "auto", "yolo"].map((a) => (
                  <option key={a} value={a}>{a}</option>
                ))}
              </select>
            </div>
            {projectId && projAutonomy !== null && (
              <>
                <h4 className="mt-3 flex items-center gap-2 text-xs text-ink-muted">
                  autonomy{" "}
                  <ScopeChip scope={projAutonomy === "" ? "engine picked" : "this project"} />
                </h4>
                <div className="mt-1 flex items-center gap-2 text-sm text-ink-secondary">
                  <span className="w-24 shrink-0">{projectId}</span>
                  <select
                    data-testid="project-autonomy"
                    value={projAutonomy}
                    onChange={(e) => {
                      const v = e.target.value;
                      void client
                        .projectAutonomySet(projectId, v)
                        .then(() => setProjAutonomy(v))
                        .catch(() => {});
                    }}
                    className="rounded border border-hairline bg-surface2 px-2 py-1 text-sm text-ink-secondary"
                  >
                    <option value="">use the default</option>
                    {["manual", "guarded", "auto", "yolo"].map((a) => (
                      <option key={a} value={a}>{a}</option>
                    ))}
                  </select>
                </div>
              </>
            )}
            </>
          ) : (
            <p className="text-sm text-ink-muted">This engine does not expose autopilot defaults yet.</p>
          )}
          <p className="mt-2 text-xs text-ink-muted">
            The autopilot stops itself at the task cap and after consecutive
            failures — money caps, UNVERIFIED and reviewer dissent always stop
            it regardless. Default autonomy applies to runs that do not pick
            one: guarded waits for you at every gate; auto and yolo accept
            green gates themselves.
          </p>
        </SettingsCard>
      </div>

      <div className={section === "budgets" ? "" : "hidden"}>
      <SettingsCard
        title="budgets & limits"
        desc="how much a run may spend before it pauses for you"
      >
      <h3 className="text-xs text-ink-muted">run budget</h3>
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

      <h3 className="mt-4 text-xs text-ink-muted">rounds per mode</h3>
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

      <h3 className="mt-4 text-xs text-ink-muted">model calls per turn</h3>
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

      <p className="mt-2 text-xs text-ink-muted">
        A round is one pass over every participant, so pair spends two turns on
        each. "Model calls per turn" is the separate limit on one participant
        chaining tool calls — a model working in circles is stopped by that, not
        by the round count. Empty uses the built-in value shown in the box.
      </p>
      </SettingsCard>
      </div>

      {/* One button, at the end, after everything it carries. The page used to
          have two, and the second sat in the middle of its own fields. */}
      <div className={`mt-3 flex items-center gap-3 ${section === "team" || section === "budgets" || section === "autopilot" ? "" : "hidden"}`}>
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
    </div>
  );
}


/** Which facts ride the seat chips — instant-save, like the theme: a display
 * choice, not an engine setting. */
function ChipFactsPicker() {
  const [facts, setFacts] = useState<ChipFact[]>(() => loadChipFacts());
  const toggle = (f: ChipFact) => {
    const next = facts.includes(f) ? facts.filter((x) => x !== f) : [...facts, f];
    setFacts(next);
    saveChipFacts(next);
  };
  return (
    <div className="mt-3" data-testid="chip-facts">
      <div className="text-xs text-ink-muted">seat chips show</div>
      <div className="mt-1 flex flex-wrap gap-3">
        {CHIP_FACTS.map((c) => (
          <label key={c.id} className="flex items-center gap-1 text-sm text-ink-secondary" title={c.hint}>
            <input
              type="checkbox"
              data-testid={`chip-fact-${c.id}`}
              checked={facts.includes(c.id)}
              onChange={() => toggle(c.id)}
            />
            {c.label}
          </label>
        ))}
      </div>
    </div>
  );
}
