import { useEffect, useState } from "react";
import { Ducklings } from "./Ducklings";
import { Skills } from "./Skills";
import { Projects } from "./Projects";
import { applyTheme, saveTheme, type Theme } from "../app/theme";
import { CHIP_FACTS, loadChipFacts, saveChipFacts, type ChipFact } from "../lib/chipfacts";
import { quack } from "../lib/attention";
import { money2 } from "../lib/format";
import { StatusChip } from "../components/StatusChip";
import { ErrorCard } from "../components/ErrorCard";
import type { BudgetView, ConfigDiagnostics, EngineClient, EngineDefaultsView, GateStatus, ModeDefaultsView, Run } from "../api/client";
import { routeHref, type SettingsSection } from "../app/routes";
import { PageHeader } from "../components/PageShell";

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

/** One titled card per concern. The page was a flat column of ten unrelated
 * headings — a cognitive disaster to scan (the user's words). Cards group by
 * task, ordered by how often each is touched. */
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
const SETTINGS_SECTIONS: { id: SettingsSection; label: string }[] = [
  { id: "ducklings", label: "ducklings" },
  { id: "fleet", label: "providers" },
  { id: "budgets", label: "budgets & limits" },
  { id: "autopilot", label: "autopilot & autonomy" },
  { id: "remote", label: "repo, remote & git" },
  { id: "appearance", label: "appearance & alerts" },
  { id: "engine", label: "engine" },
];

const SETTINGS_ROOMS = [
  { name: "skills" as const, label: "Skills" },
  { name: "projects" as const, label: "Project management" },
];

/** The three questions are the information architecture; the existing rooms
 * remain separate routes and views. This is deliberately only navigation: it
 * keeps bookmarks and each room's implementation intact. */
const SETTINGS_GROUPS: { label: string; shortLabel: string; sectionIds?: SettingsSection[]; rooms?: typeof SETTINGS_ROOMS[number]["name"][] }[] = [
  {
    label: "Who works for you — and how far they may go",
    shortLabel: "People & autonomy",
    sectionIds: ["ducklings", "fleet", "budgets", "autopilot"],
    rooms: ["skills"],
  },
  {
    label: "Your projects",
    shortLabel: "Projects",
    sectionIds: ["remote"],
    rooms: ["projects"],
  },
  {
    label: "Your preferences",
    shortLabel: "Preferences",
    sectionIds: ["appearance", "engine"],
  },
];

export function Settings({
  theme, onTheme, engineVersion, connection, client, projectId, onEngine, engineBusy, engineError, room, section, onProjectSelect, onProjectsChanged,
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
  room?: "skills" | "projects";
  /** Settings section selected by the hash route. */
  section?: SettingsSection;
  onProjectSelect?: (id: string) => void;
  onProjectsChanged?: () => void;
}) {
  const change = (t: Theme) => {
    applyTheme(t);
    saveTheme(t);
    onTheme(t);
  };
  // The app owns navigation through the hash route. The fallback only keeps
  // standalone component previews (which provide no route) interactive.
  const [previewSection, setPreviewSection] = useState<SettingsSection>("ducklings");
  const activeSection = section ?? previewSection;
  return (
    <div className="flex h-full min-h-0 flex-col gap-4 overflow-hidden p-4" data-testid="settings">
      <PageHeader eyebrow="Configuration" title="Settings" subtitle="Manage who works for you, your projects and Ducklab's local preferences." />
      <div className="flex min-h-0 flex-1 gap-6 overflow-hidden">
      {/* The sub-menu: one concern on screen at a time (the user's own
          mock). Nothing unmounts except the fleet — config state and its
          one Save survive switching via CSS visibility. */}
      <nav className="w-52 shrink-0 space-y-1 overflow-y-auto border-r border-hairline pr-4" data-testid="settings-nav">
        {SETTINGS_GROUPS.map((group) => (
          <section key={group.label} className="mb-3" aria-label={group.label}>
            <h2 className="px-2 pb-1 text-[10px] font-semibold uppercase tracking-[0.14em] text-ink-muted" aria-label={group.label} title={group.label}>{group.shortLabel}</h2>
            <div className="space-y-1">
              {(group.sectionIds ?? []).map((id) => {
                const sec = SETTINGS_SECTIONS.find((item) => item.id === id)!;
                return (
                  <a
                    key={sec.id}
                    href={routeHref({ name: "settings", section: sec.id })}
                    data-testid={`settings-nav-${sec.id}`}
                    aria-current={!room && activeSection === sec.id ? "page" : undefined}
                    onClick={section === undefined ? () => setPreviewSection(sec.id) : undefined}
                    className={`block w-full rounded px-2 py-1 text-left text-sm ${
                      !room && activeSection === sec.id ? "bg-surface2 text-ink" : "text-ink-muted"
                    }`}
                  >
                    {sec.label}
                  </a>
                );
              })}
              {(group.rooms ?? []).map((name) => {
                const roomItem = SETTINGS_ROOMS.find((item) => item.name === name)!;
                return (
                  <a
                    key={roomItem.name}
                    href={routeHref({ name: roomItem.name })}
                    data-testid={`settings-nav-${roomItem.name}`}
                    className={`block whitespace-nowrap rounded px-2 py-1 text-sm ${room === roomItem.name ? "bg-surface2 text-ink" : "text-ink-muted"}`}
                  >
                    {roomItem.label}
                  </a>
                );
              })}
            </div>
          </section>
        ))}
      </nav>

      <div className="flex min-h-0 min-w-0 max-w-3xl flex-1 flex-col overflow-y-auto" data-testid="settings-content">
      {room === "skills" && client && projectId && (
        <div className="h-full overflow-y-auto p-4" data-testid="settings-room-skills">
          <Skills client={client} projectId={projectId} />
        </div>
      )}
      {room === "projects" && client && (
        <div className="h-full overflow-y-auto p-4" data-testid="settings-room-projects">
          <Projects client={client} selected={projectId ?? ""} onSelect={onProjectSelect ?? (() => {})} onChanged={onProjectsChanged ?? (() => {})} />
        </div>
      )}
      {!room && (
        <div data-testid={`settings-section-${activeSection}`}>
          {activeSection === "ducklings" && client && (
            <>
              <Ducklings client={client} projectId={projectId ?? ""} only="ducklings" />
              <a href={routeHref({ name: "flock" })} role="link" className="ml-4 text-sm text-ink underline">Open Flock</a>
            </>
          )}
          {!room && client && <ConfigSection client={client} section={activeSection} projectId={projectId} />}
          {!room && activeSection === "fleet" && client && (
            <Ducklings client={client} projectId={projectId ?? ""} only="providers" />
          )}
          {!room && activeSection === "remote" && client && projectId && <RemoteGitSection client={client} projectId={projectId} />}

          <div className={activeSection === "appearance" ? "" : "hidden"}>
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

          <div className={activeSection === "engine" ? "" : "hidden"}>
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
                  {engineError && <ErrorCard error={engineError} testId="settings-engine-error" />}
                </div>
              )}
            </SettingsCard>
          </div>
        </div>
      )}

      </div>
      </div>
    </div>
  );
}

/** Project-scoped remote controls deliberately save through ProjectUpdate: no
 * field mutates while it is being typed, and every write is an explicit person
 * action. Diagnostics has no controls or setter wiring. */
function RemoteGitSection({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [draft, setDraft] = useState<Record<string, string>>({
    "remote.name": "", "remote.on_accept": "nothing", "remote.fetch_on_open": "false", "remote.allow_mcp_verbs": "",
    "github.pr_base": "", "github.pr_draft": "false", "github.pr_tool": "", "github.pr_body_by_scribe": "false",
    "shell.allow_prefixes": "", "shell.deny": "", "git.protected_paths": "", "verify.link_deps": "",
  });
  const [state, setState] = useState("");
  const [remoteError, setRemoteError] = useState<unknown>(null);
  const [findings, setFindings] = useState<{ key: string; reason: string }[]>([]);
  const [diagnostics, setDiagnostics] = useState<ConfigDiagnostics | null>(null);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    if (typeof client.configDoctor === "function") {
      void client.configDoctor(projectId).then(setFindings).catch(() => {}).finally(() => setLoaded(true));
    } else setLoaded(true);
    if (typeof client.configDiagnostics === "function") {
      void client.configDiagnostics(projectId).then(setDiagnostics).catch(() => {});
    }
    // Settings never re-asks for values the project already records.
    if (typeof client.projectGet !== "function") return;
    void client.projectGet(projectId).then((project) => {
      const c = project.config ?? {};
      const section = (name: string) => (c[name] ?? {}) as Record<string, unknown>;
      const remote = section("remote"), github = section("github"), shell = section("shell"), git = section("git"), verify = section("verify");
      const value = (v: unknown) => Array.isArray(v) ? v.join(",") : v === undefined || v === null ? "" : String(v);
      setDraft({
        "remote.name": value(remote.name), "remote.on_accept": value(remote.on_accept ?? (remote.name ? "push" : "nothing")), "remote.fetch_on_open": value(remote.fetch_on_open), "remote.allow_mcp_verbs": value(remote.allow_mcp_verbs),
        "github.pr_base": value(github.pr_base), "github.pr_draft": value(github.pr_draft), "github.pr_tool": value(github.pr_tool), "github.pr_body_by_scribe": value(github.pr_body_by_scribe),
        "shell.allow_prefixes": value(shell.allow_prefixes), "shell.deny": value(shell.deny), "git.protected_paths": value(git.protected_paths), "verify.link_deps": value(verify.link_deps),
      });
    }).catch(() => {});
  }, [client, projectId]);
  const update = (key: string, value: string) => setDraft((d) => ({ ...d, [key]: value }));
  const list = (key: string, label: string) => <label className="flex flex-col gap-0.5 text-sm text-ink-secondary">{label}<span className="text-xs text-ink-muted">one item per line</span><textarea data-testid={`slice-${key}`} value={(draft[key] ?? "").replaceAll(",", "\n")} onChange={(e) => update(key, e.target.value.split("\n").filter(Boolean).join(","))} className="rounded border border-hairline bg-surface2 px-2 py-1" /></label>; 
  const save = () => {
    setRemoteError(null);
    setState("saving…");
    void client.projectUpdate(projectId, draft, "settings_remote_git").then(() => setState("saved")).catch((e) => setRemoteError(e));
  };
  const text = (key: string, label: string, hint?: string) => (
    <label className="flex flex-col gap-0.5 text-sm text-ink-secondary">
      {label}{hint && <span className="text-xs text-ink-muted">{hint}</span>}
      <input data-testid={`remote-${key}`} value={draft[key]} onChange={(e) => update(key, e.target.value)} className="rounded border border-hairline bg-surface2 px-2 py-1" />
    </label>
  );
  return <>
    <SettingsCard title="remote & pull requests" desc="how this project uses its one named git remote; fetching remains opt-in" testid="remote-git-settings">
      <div className="grid gap-3">{text("remote.name", "remote name")}<label className="flex flex-col gap-0.5 text-sm text-ink-secondary">after accepting a run<span className="text-xs text-ink-muted">commit first, then choose whether to leave it local, push it, or open/update a pull request</span><select data-testid="remote-on_accept" value={draft["remote.on_accept"]} onChange={(e) => update("remote.on_accept", e.target.value)} className="rounded border border-hairline bg-surface2 px-2 py-1"><option value="nothing">nothing (commit locally)</option><option value="push">push to the base branch</option><option value="pr">push and open/update a pull request</option></select></label>{text("remote.fetch_on_open", "fetch on open", "true or false")}
      {text("github.pr_base", "pull request base branch")}{text("github.pr_draft", "create draft pull requests", "true or false")}{text("github.pr_tool", "pull request tool")}{text("github.pr_body_by_scribe", "scribe writes PR body", "true or false")}</div>
    </SettingsCard>
    <SettingsCard title="lists & safety rules" desc="one item per comma; these lists constrain commands, protected files, and acceptance checkouts" testid="slice-key-editors">
      <div className="grid gap-3">{list("shell.allow_prefixes", "allowed shell prefixes")}{list("shell.deny", "denied shell commands")}{list("git.protected_paths", "protected paths")}{list("verify.link_deps", "linked verification dependencies")}{list("remote.allow_mcp_verbs", "remote MCP verbs")}</div>
      <button type="button" data-testid="save-remote-git-settings" onClick={save} className="mt-3 rounded border border-hairline px-2 py-1 text-sm">Save remote & git settings</button>
      {state && !remoteError && <span className="ml-2 text-xs text-ink-muted" data-testid="remote-git-state">{state}</span>}
      {remoteError !== null && <ErrorCard error={remoteError} testId="settings-remote-error" />}
    </SettingsCard>
    <SettingsCard title="connection diagnostics" desc="read-only checks from the engine; no setting is changed here" testid="remote-diagnostics">
      {findings.length > 0 && <p className="mb-2 text-xs text-warning" data-testid="config-findings">Configuration needs attention: {findings.map((f) => f.key).join(", ")}</p>}
      {!loaded && <p className="text-xs text-ink-muted">checking diagnostics…</p>}
      <dl className="space-y-1 text-sm text-ink-secondary"><div><dt className="inline">remote reachable: </dt><dd className="inline" data-testid="diagnostic-remote">{diagnostics?.remote_reachable ?? "not available"}</dd></div><div><dt className="inline">gh authentication: </dt><dd className="inline" data-testid="diagnostic-gh">{diagnostics?.gh_auth ?? "not available"}</dd></div><div><dt className="inline">credential helper: </dt><dd className="inline" data-testid="diagnostic-credential-helper">{diagnostics?.credential_helper ?? "not available"}</dd></div></dl>
    </SettingsCard>
  </>;
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
  const [recentRuns, setRecentRuns] = useState<Run[] | null>(null);
  const [modes, setModes] = useState<ModeDefaultsView | null>(null);
  const [engine, setEngine] = useState<EngineDefaultsView | null>(null);
  const [engineDraft, setEngineDraft] = useState("");
  // Drafts, so nothing is sent until Save and a half-typed number is never a
  // ceiling of zero.
  const [b, setB] = useState<Record<string, string>>({});
  const [rounds, setRounds] = useState<Record<string, string>>({});
  const [roleTurns, setRoleTurns] = useState<Record<string, string>>({});
  const [agentTurns, setAgentTurns] = useState("");
  const [buildMode, setBuildMode] = useState("");
  const [testMode, setTestMode] = useState("");
  const [state, setState] = useState<{ kind: "idle" | "saving" | "saved" | "error"; message?: unknown }>({
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
  // The project's own autonomy — the level runs and triage consult FIRST.
  // It had no control anywhere; the guidance was "edit the TOML".
  const [projAutonomy, setProjAutonomy] = useState<string | null>(null);
  // Checkout preparation is part of the gate contract, not an engine guess;
  // show it next to the project settings that name the gate.
  const [gate, setGate] = useState<GateStatus | null>(null);
  useEffect(() => {
    // Older engine clients (and focused settings test doubles) may not expose
    // the gate endpoint; the rest of Settings remains usable without it.
    if (!projectId || typeof client.projectGate !== "function") return;
    void client.projectGate(projectId).then(setGate).catch(() => {});
  }, [client, projectId]);
  useEffect(() => {
    if (!projectId) return;
    Promise.resolve()
      .then(() => client.projectAutonomy(projectId))
      .then((r) => setProjAutonomy(r.autonomy))
      .catch(() => {});
  }, [client, projectId]);
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
      wallclock_escalation_multiplier: String(v.wallclock_escalation_multiplier),
    });
  };
  const applyEngine = (v: EngineDefaultsView) => {
    setEngine(v);
    setEngineDraft(String(v.max_concurrent_runs));
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
    setBuildMode(v.build_mode ?? "");
    setTestMode(v.test_mode ?? "");
  };

  const load = () => {
    client.budgetDefaults().then(applyBudget).catch((e) => setState({ kind: "error", message: e }));
    client.modeDefaults().then(applyModes).catch((e) => setState({ kind: "error", message: e }));
    if (typeof client.engineDefaults === "function") client.engineDefaults().then(applyEngine).catch(() => {});
  };

  useEffect(load, [client]);
  useEffect(() => {
    // The run log is the source of truth. Only fetch it while this panel is
    // visible: the other settings sections have no use for this read.
    if (section !== "budgets" || typeof client.runs !== "function") return;
    void client.runs(projectId).then(setRecentRuns).catch(() => setRecentRuns([]));
  }, [client, projectId, section]);

  if (!budget || !modes) {
    return (
      <section className="mt-4" data-testid="config-settings">
        {state.kind === "error" ? <ErrorCard error={state.message} testId="settings-load-error" /> : <p className="text-sm text-ink-muted">reading…</p>}
      </section>
    );
  }

  const recent = (recentRuns ?? []).filter((run) => {
    const started = Date.parse(run.started_at);
    return Number.isFinite(started) && started >= Date.now() - 30 * 24 * 60 * 60 * 1000;
  });
  const hitCounts = {
    tokens: recent.filter((r) => r.budget?.limit && r.budget.tokens >= r.budget.limit.tokens).length,
    usd: recent.filter((r) => r.budget?.limit && r.budget.usd >= r.budget.limit.usd).length,
    turns: recent.filter((r) => r.budget?.limit && r.budget.turns >= r.budget.limit.turns).length,
    wallclock_s: recent.filter((r) => r.budget?.limit && r.budget.wallclock_s >= r.budget.limit.wallclock_s).length,
  };
  const spend = recent.reduce((totals, run) => {
    const amount = run.budget?.usd ?? 0;
    if (run.status === "failed") totals.failed += amount;
    else if (run.status === "done" && (run.accepted === true || /accept|land/i.test(run.verdict))) totals.accepted += amount;
    else if (run.status === "done") totals.rejected += amount;
    return totals;
  }, { accepted: 0, rejected: 0, failed: 0 });
  const ceilingActivity = (label: string, count: number) => count > 0 ? (
    <div key={label}>
      <p>{count} run{count === 1 ? "" : "s"} hit this ceiling in the last 30 days ({label}).</p>
      {count >= 2 && <p className="text-xs text-warning">Suggested adjustment: consider raising the {label} ceiling.</p>}
    </div>
  ) : null;

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
        wallclock_escalation_multiplier: Number(b.wallclock_escalation_multiplier) || 0,
      }),
      engine && typeof client.engineDefaultsSet === "function"
        ? client.engineDefaultsSet({ ...engine, max_concurrent_runs: Number(engineDraft) || 0 })
        : Promise.resolve(null),
      client.modeDefaultsSet({
        rounds: numbersOnly(rounds),
        agent_max_turns: Number(agentTurns) || 0,
        // Empty seats are UI scaffolding, not preferences.
        build_mode: buildMode,
        test_mode: testMode,
        role_turns: numbersOnly(roleTurns),
      }),
    ])
      .then(([savedAp, savedBudget, savedEngine, savedModes]) => {
        if (savedAp) {
          setAp({ max_tasks: String(savedAp.max_tasks), max_fails: String(savedAp.max_fails), autonomy: savedAp.autonomy });
        }
        applyBudget(savedBudget);
        if (savedEngine) applyEngine(savedEngine);
        applyModes(savedModes);
        setState({ kind: "saved" });
      })
      .catch((e) => setState({ kind: "error", message: e }));
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
      <div className={section === "engine" ? "" : "hidden"}>
        {engine && <SettingsCard title="concurrency" desc="Live queue admission limits; changes take effect without restarting the engine." testid="engine-concurrency">
          {num(engineDraft, setEngineDraft, "maximum concurrent runs", "engine-max-concurrent", String(engine.cpu_ceiling))}
          <p className="mt-1 text-xs text-ink-muted">The host CPU ceiling is {engine.cpu_ceiling}; this is context, not a hard limit.</p>
        </SettingsCard>}
      </div>
      <div>
      {gate && (
        <SettingsCard
          title="verification"
          desc="the gate and its clean-checkout preparation"
          testid="gate-preparation"
        >
          <p className="font-mono text-sm text-ink-secondary">gate: {gate.command || "none"}</p>
          {gate.setup && <p className="mt-1 font-mono text-sm text-ink-secondary">setup: {gate.setup}</p>}
          {gate.link_deps?.length ? (
            <p className="mt-1 text-sm text-ink-secondary">link dependencies: {gate.link_deps.join(", ")}</p>
          ) : null}
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
        {num(b.wallclock_escalation_multiplier ?? "", (v) => setB({ ...b, wallclock_escalation_multiplier: v }), "escalation multiplier", "budget-wallclock_escalation_multiplier")}
      </div>
      <p className="mt-1 text-xs text-ink-muted">escalate when a run takes N x its kind's median active time</p>
      <p className="mt-1 text-sm text-ink-muted">
        tokens counts prompt and completion together, and every round re-sends the
        conversation — so a long task spends most of its budget on input.
        Currently {budget.max_tokens.toLocaleString()} per run.
      </p>
      {recentRuns !== null && (
        <div className="mt-3 rounded border border-hairline p-3 text-sm text-ink-secondary" data-testid="budget-activity">
          <p className="font-medium text-ink">Recently</p>
          <div data-testid="budget-hits" className="space-y-1">
            {ceilingActivity("tokens", hitCounts.tokens)}
            {ceilingActivity("USD", hitCounts.usd)}
            {ceilingActivity("turns", hitCounts.turns)}
            {ceilingActivity("time", hitCounts.wallclock_s)}
          </div>
          <p className="mt-3 font-medium text-ink">where the money went</p>
          <p data-testid="budget-money">accepted work {money2(spend.accepted)} / rejected work {money2(spend.rejected)} / failed runs {money2(spend.failed)}</p>
          <p className="mt-1 text-xs text-ink-muted">Figures cover finished runs in the last 30 days.</p>
        </div>
      )}

      <h3 className="mt-4 text-xs text-ink-muted">default phase modes</h3>
      <div className="mt-1 flex flex-wrap items-center gap-3 text-sm text-ink-secondary">
        {[
          ["build runs open in", buildMode, setBuildMode],
          ["test runs open in", testMode, setTestMode],
        ].map(([label, value, setter]) => (
          <label key={label as string} className="flex flex-col gap-0.5 text-xs text-ink-muted">
            {label as string}
            <select
              aria-label={label as string}
              data-testid={`default-${(label as string).split(" ")[0]}-mode`}
              value={value as string}
              onChange={(e) => {
                (setter as (value: string) => void)(e.target.value);
                touched();
              }}
              className="rounded border border-hairline bg-surface2 px-2 py-1 text-sm text-ink-secondary"
            >
              <option value="">project habit, then solo</option>
              {["solo", "pair", "tournament", "split"].map((mode) => (
                <option key={mode} value={mode}>{mode}</option>
              ))}
            </select>
          </label>
        ))}
      </div>
      <p className="mt-2 text-xs text-ink-muted">
        Leave blank to use the project's [modes] habit, then solo. The per-project [modes] table stays config.toml-only for now.
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
      <div className={`mt-3 flex items-center gap-3 ${section === "budgets" || section === "autopilot" || section === "engine" ? "" : "hidden"}`}>
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
          <ErrorCard error={state.message} testId="settings-error" />
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
