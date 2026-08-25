/**
 * Projects — creating one, renaming it, and letting it go.
 *
 * A full-cycle harness that cannot start a project is not full cycle. Every
 * mutation here already existed on the engine; the desktop had only ever read
 * the list.
 *
 * Nothing here deletes files. Forgetting a project unregisters it and leaves
 * the directory exactly where it was, because a tool that can erase a person's
 * work from a list view is a tool nobody should point at a real repository.
 */

import { useCallback, useEffect, useState } from "react";
import type { AppStatus, ConfigFinding, Duckling, EngineClient, GateStatus, Project } from "../api/client";
import { ChatAbout } from "../components/ChatAbout";

type RemoteStatus = { ahead?: number; behind?: number };
import { canChooseDirectory, chooseDirectory } from "../lib/picker";
import { StatusChip } from "../components/StatusChip";
import { ShellCmd } from "../components/ShellCmd";

export function Projects({
  client,
  selected,
  onSelect,
  onChanged,
}: {
  client: EngineClient;
  selected: string;
  onSelect: (id: string) => void;
  onChanged: () => void;
}) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [path, setPath] = useState("");
  const [name, setName] = useState("");
  const [gitInit, setGitInit] = useState(true);
  const [gates, setGates] = useState<Record<string, GateStatus>>({});
  const [apps, setApps] = useState<Record<string, AppStatus>>({});
  const [remotes, setRemotes] = useState<Record<string, RemoteStatus>>({});
  const [remoteNotice, setRemoteNotice] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [renameTo, setRenameTo] = useState("");
  // Opening a project with a known configuration concern offers explanation;
  // it never changes the recorded configuration.
  const [configFindings, setConfigFindings] = useState<Record<string, ConfigFinding[]>>({});
  const [consultants, setConsultants] = useState<Duckling[]>([]);
  const [openedProject, setOpenedProject] = useState<string | null>(null);

  const load = useCallback(() => {
    client
      .projects()
      .then((ps) => {
        setProjects(ps);
        // A project that cannot produce PASSED is the thing people discover
        // three runs in. Asked for here, where they are already looking.
        for (const p of ps) {
          if (p.missing) continue;
          client
            .projectGate(p.id)
            .then((g) => setGates((cur) => ({ ...cur, [p.id]: g })))
            .catch(() => {});
          client
            .appStatus(p.id)
            .then((a) => setApps((cur) => ({ ...cur, [p.id]: a })))
            .catch(() => {});
          client
            .projectStatus(p.id)
            .then((status) => setRemotes((cur) => ({ ...cur, [p.id]: status })))
            .catch(() => {});
          if (typeof client.configDoctor === "function") {
            client.configDoctor(p.id).then((findings) => setConfigFindings((cur) => ({ ...cur, [p.id]: findings }))).catch(() => {});
          }
        }
      })
      .catch((err) => setFailure(err instanceof Error ? err.message : String(err)));
  }, [client]);

  useEffect(load, [load]);
  useEffect(() => {
    if (typeof client.ducklings !== "function") return;
    void client.ducklings().then(setConsultants).catch(() => {});
  }, [client]);

  // A selection is a project-open moment. Query its read-only diagnosis here,
  // not only while the full project list happens to refresh.
  useEffect(() => {
    if (!selected || typeof client.configDoctor !== "function") return;
    setOpenedProject(selected);
    void client.configDoctor(selected)
      .then((findings) => setConfigFindings((current) => ({ ...current, [selected]: findings })))
      .catch(() => {});
  }, [client, selected]);

  // Said before it is sent. `~` is a shell feature the engine cannot expand
  // for you, and a relative path means nothing to a daemon — one real session
  // typed "~/dev/calculator" and got a project in a folder named "~".
  const pathProblem = (() => {
    const p = path.trim();
    if (!p) return null;
    if (p.startsWith("~")) return "~ is a shell shortcut. Write the full path, or use Choose….";
    if (!p.startsWith("/")) return "Give a full path starting with /, or use Choose….";
    return null;
  })();

  const create = async () => {
    if (!path.trim() || pathProblem) return;
    setBusy(true);
    setFailure(null);
    try {
      const p = await client.projectInit(path.trim(), name.trim(), gitInit);
      setPath("");
      setName("");
      load();
      onChanged();
      onSelect(p.id);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const browse = async () => {
    const chosen = await chooseDirectory();
    if (!chosen) return;
    setPath(chosen);
    // The folder's own name is nearly always the project's name, and typing it
    // again is the sort of small friction that adds up.
    if (!name.trim()) setName(chosen.split("/").filter(Boolean).pop() ?? "");
  };

  const rename = async (id: string) => {
    if (!renameTo.trim()) return;
    try {
      await client.projectUpdate(id, { name: renameTo.trim() });
      setRenaming(null);
      load();
      onChanged();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    }
  };

  const forget = async (p: Project) => {
    // Asked once, in words that say what actually happens. "Delete" would be a
    // lie, and a person who reads it as one will not click it when they should.
    if (!confirm(`Forget "${p.name || p.id}"?\n\nDucklab stops tracking it. The files at\n${p.path}\nare not touched.`)) {
      return;
    }
    try {
      await client.projectForget(p.id);
      load();
      onChanged();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <div data-testid="projects-view" className="space-y-4">
      <section className="rounded-card border border-hairline p-3">
        <h3 className="mb-2 text-ink">New project</h3>
        <div className="flex flex-wrap items-center gap-2">
          <input
            aria-label="project folder"
            data-testid="project-path"
            placeholder="/path/to/repo"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            className="min-w-64 flex-1 rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
          />
          {canChooseDirectory() && (
            <button
              type="button"
              onClick={() => void browse()}
              data-testid="project-browse"
              className="rounded border border-hairline px-2 py-1 text-sm"
            >
              Choose…
            </button>
          )}
          <input
            aria-label="project name"
            data-testid="project-name"
            placeholder="name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-40 rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
          />
          <label className="flex items-center gap-1 text-sm text-ink-secondary">
            <input
              type="checkbox"
              checked={gitInit}
              data-testid="project-git-init"
              onChange={(e) => setGitInit(e.target.checked)}
            />
            git init if needed
          </label>
          <button
            type="button"
            onClick={() => void create()}
            disabled={busy || !path.trim() || Boolean(pathProblem)}
            data-testid="project-create"
            className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-40"
          >
            {busy ? "Creating…" : "Create"}
          </button>
        </div>
        {pathProblem && (
          <p className="mt-2 text-sm text-serious" data-testid="path-problem">
            {pathProblem}
          </p>
        )}
        <p className="mt-2 text-xs text-ink-muted">
          A folder that is already a ducklab project is adopted rather than refused. Runs need a
          git repository — leave the box ticked unless the folder already has one.
        </p>
      </section>

      {failure && (
        <p className="text-critical" data-testid="projects-error">
          {failure}
        </p>
      )}
      {remoteNotice && (
        <p className="rounded-card border border-hairline p-3 text-sm text-ink" data-testid="remote-action-notice">
          {remoteNotice}
        </p>
      )}

      <section className="rounded-card border border-hairline p-3">
        <h3 className="mb-2 text-ink">Projects</h3>
        {projects.length === 0 ? (
          <p className="text-sm text-ink-muted">None yet.</p>
        ) : (
          <ul className="space-y-2">
            {projects.map((p) => (
              <li
                key={p.id}
                data-testid={`project-row-${p.id}`}
                className={
                  "rounded-card border border-hairline p-3 " +
                  (p.id === selected ? "bg-surface2" : "")
                }
              >
                {/* Header: identity left, actions right — never sharing a
                    line with a shell command. */}
                <div className="flex flex-wrap items-center gap-2">
                  {renaming === p.id ? (
                    <>
                      <input
                        aria-label="new name"
                        data-testid="rename-input"
                        value={renameTo}
                        onChange={(e) => setRenameTo(e.target.value)}
                        className="rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
                      />
                      <button
                        type="button"
                        onClick={() => void rename(p.id)}
                        data-testid="rename-save"
                        className="rounded border border-hairline px-2 py-1 text-xs"
                      >
                        Save
                      </button>
                      <button
                        type="button"
                        onClick={() => setRenaming(null)}
                        className="rounded border border-hairline px-2 py-1 text-xs"
                      >
                        Cancel
                      </button>
                    </>
                  ) : (
                    <>
                      <button
                        type="button"
                        onClick={() => onSelect(p.id)}
                        data-testid={`project-select-${p.id}`}
                        className="text-left text-sm font-medium text-ink"
                      >
                        {p.name || p.id}
                      </button>
                      {p.missing && <StatusChip role="critical" label="folder is gone" />}
                      {remotes[p.id] && <span className="text-xs text-ink-muted" data-testid={`remote-status-${p.id}`}>↑{remotes[p.id]?.ahead ?? 0} ↓{remotes[p.id]?.behind ?? 0}</span>}
                      <span className="flex gap-1">
                        <button type="button" data-testid={`project-pull-${p.id}`} onClick={() => void client.projectPull(p.id).then((result) => { setRemoteNotice(result.prompt ?? "Pull complete."); load(); }).catch((e) => setFailure(String(e)))} className="rounded border border-hairline px-2 py-0.5 text-xs">Pull</button>
                        <button type="button" data-testid={`project-push-${p.id}`} onClick={() => void client.projectPush(p.id).then((result) => { setRemoteNotice(`Pushed ${result.branch}.`); load(); }).catch((e) => setFailure(String(e)))} className="rounded border border-hairline px-2 py-0.5 text-xs">Push</button>
                        <button type="button" data-testid={`project-pr-${p.id}`} onClick={() => void client.projectPR(p.id).then((result) => { setRemoteNotice(result.pr_url ? `Pull request created: ${result.pr_url}` : result.compare_url ? `Your branch is ready. Open this compare page to create the pull request: ${result.compare_url}` : "Your branch is ready for a pull request."); load(); }).catch((e) => setFailure(String(e)))} className="rounded border border-hairline px-2 py-0.5 text-xs">Create PR</button>
                      </span>
                      <span className="ml-auto flex gap-1">
                        <button
                          type="button"
                          onClick={() => {
                            setRenaming(p.id);
                            setRenameTo(p.name || p.id);
                          }}
                          data-testid={`project-rename-${p.id}`}
                          className="rounded border border-hairline px-2 py-0.5 text-xs"
                        >
                          Rename
                        </button>
                        <button
                          type="button"
                          onClick={() => void forget(p)}
                          data-testid={`project-forget-${p.id}`}
                          className="rounded border border-hairline px-2 py-0.5 text-xs"
                        >
                          Forget
                        </button>
                      </span>
                    </>
                  )}
                </div>
                <div className="mt-0.5 truncate font-mono text-xs text-ink-muted" title={p.path}>
                  {p.path}
                </div>
                {(() => {
                  const finding = configFindings[p.id]?.[0];
                  if (p.id !== openedProject || !finding || consultants.length === 0) return null;
                  const initialMessage = `Please prioritize this configuration finding and explain the safe amendment: ${finding.key} → ${finding.proposed}. Reason: ${finding.reason}`;
                  return <div className="mt-2 rounded border border-warning p-2 text-xs" data-testid={`project-config-offer-${p.id}`}>
                    <p><code>{finding.key}</code> needs attention: {finding.reason}</p>
                    <ChatAbout client={client} projectId={p.id} aboutKind="ducklab" aboutId="configuration" ducklings={consultants} label="Ask the configuration consultant" initialMessage={initialMessage} />
                  </div>;
                })()}
                {/* Config rows: label · value · edit, one per line. Commands
                    truncate with the full text on hover; editors open below
                    at full width. */}
                <div className="mt-2 space-y-1 border-t border-hairline pt-2">
                  <AppChip
                    status={apps[p.id]}
                    onSet={async (command, url, health, preflight, requires) => {
                      try {
                        await client.projectUpdate(p.id, {
                          "run.command": command, "run.url": url, "run.health": health,
                          "run.preflight": preflight, "run.requires": requires,
                        });
                        const a = await client.appStatus(p.id);
                        setApps((cur) => ({ ...cur, [p.id]: a }));
                      } catch (err) {
                        setFailure(err instanceof Error ? err.message : String(err));
                      }
                    }}
                  />
                  <GateChip
                    status={gates[p.id]}
                    onAdopt={() =>
                      void client
                        .projectGateAdopt(p.id)
                        .then((g) => setGates((cur) => ({ ...cur, [p.id]: g })))
                        .catch((err) => setFailure(err instanceof Error ? err.message : String(err)))
                    }
                    onSet={async (mode, command) => {
                      try {
                        await client.projectUpdate(p.id, {
                          "verify.mode": mode,
                          ["verify." + (mode === "custom" ? "custom" : mode)]: command,
                        });
                        const g = await client.projectGate(p.id);
                        setGates((cur) => ({ ...cur, [p.id]: g }));
                      } catch (err) {
                        setFailure(err instanceof Error ? err.message : String(err));
                      }
                    }}
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

/** How the app starts — the stage the gate cannot see. A project reached
 * all-tasks-accepted with no way to run; this chip is where that fact stops
 * being invisible. */
function AppChip({
  status,
  onSet,
}: {
  status?: AppStatus;
  onSet: (command: string, url: string, health: string, preflight: string, requires: string) => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [command, setCommand] = useState("");
  const [url, setUrl] = useState("");
  const [health, setHealth] = useState("");
  const [preflight, setPreflight] = useState("");
  const [requires, setRequires] = useState("");
  if (!status) return null;
  const field = (
    label: string,
    value: string,
    set: (v: string) => void,
    placeholder: string,
    testid: string,
    mono = true,
  ) => (
    <label className="flex items-center gap-2 text-xs text-ink-muted">
      <span className="w-16 shrink-0">{label}</span>
      <input
        value={value}
        onChange={(e) => set(e.target.value)}
        placeholder={placeholder}
        data-testid={testid}
        className={`w-full rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs ${mono ? "font-mono" : ""}`}
      />
    </label>
  );
  const editor = editing && (
    <div className="mt-1 w-full max-w-xl space-y-1" data-testid="app-editor">
      {field("command", command, setCommand, "python app.py — starts the app", "app-command")}
      {field("url", url, setUrl, "http://localhost:8000", "app-url")}
      {field("health", health, setHealth, "http://localhost:8000/health (optional)", "app-health")}
      {field("preflight", preflight, setPreflight, "pg_isready -p 5432 — checked before every launch (optional)", "app-preflight")}
      {field("requires", requires, setRequires, "PostgreSQL on :5432; db created — human checklist, ; separated (optional)", "app-requires-input", false)}
      <div className="flex items-center gap-2">
        <button
          type="button"
          disabled={command.trim() === ""}
          onClick={() => void onSet(command.trim(), url.trim(), health.trim(), preflight.trim(), requires.trim()).then(() => setEditing(false))}
          data-testid="app-save"
          className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-50"
        >
          Set
        </button>
        <button type="button" onClick={() => setEditing(false)} className="text-xs text-ink-muted underline">
          cancel
        </button>
      </div>
    </div>
  );
  return (
    <div data-testid="app-chip">
      <div className="flex items-center gap-2 text-xs">
        <span className="w-10 shrink-0 text-ink-muted">app</span>
        {status.configured ? (
          <ShellCmd cmd={status.command ?? ""} className="min-w-0 flex-1 truncate font-mono text-ink-secondary" />
        ) : (
          <span className="min-w-0 flex-1 truncate" style={{ color: "var(--status-warning)" }}>
            not set — the app cannot start
          </span>
        )}
        {!editing && (
          <button
            type="button"
            onClick={() => {
              setCommand(status.command ?? "");
              setUrl(status.url ?? "");
              setHealth("");
              setPreflight(status.preflight ?? "");
              setRequires(status.requires ?? "");
              setEditing(true);
            }}
            data-testid="app-edit"
            className="shrink-0 text-ink-muted underline"
          >
            {status.configured ? "edit" : "set it"}
          </button>
        )}
      </div>
      {editor}
    </div>
  );
}

/** What a project's gate is, and the one thing worth doing about it.
 *
 * A gate with nothing runnable behind it means every run ends UNVERIFIED — the
 * failure mode is that nobody notices, so this says the consequence rather
 * than the setting. */
function GateChip({
  status,
  onAdopt,
  onSet,
}: {
  status?: GateStatus;
  onAdopt: () => void;
  onSet: (mode: string, command: string) => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [mode, setMode] = useState("tests");
  const [command, setCommand] = useState("");
  if (!status) return null;

  // The manual door. Detection finds go.mod and package.json; it cannot find
  // "this project verifies with a script I have not written yet" — and until
  // this, that case dead-ended at a warning telling nobody what to do next.
  // The gate stays human-set by design (a gate decides what a verdict means),
  // which is exactly why the human needs a place to set it.
  // Full-width, below the row: the command this edits can be a hundred
  // characters of env vars and && chains — a 10rem inline input made the
  // most important field on the page the least usable one.
  const editor = editing && (
    <div className="mt-1 flex w-full items-center gap-1" data-testid="gate-editor">
      <select
        value={mode}
        onChange={(e) => setMode(e.target.value)}
        data-testid="gate-mode"
        className="rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs"
      >
        <option value="tests">tests</option>
        <option value="build">build</option>
        <option value="custom">custom</option>
      </select>
      <input
        value={command}
        onChange={(e) => setCommand(e.target.value)}
        placeholder="pytest -q  (chain more with && — e.g. pytest -q && cd frontend && npm run build)"
        data-testid="gate-command"
        className="min-w-0 flex-1 rounded border border-hairline bg-surface2 px-1 py-0.5 font-mono text-xs"
      />
      <button
        type="button"
        disabled={command.trim() === ""}
        onClick={() => {
          void onSet(mode, command.trim()).then(() => setEditing(false));
        }}
        data-testid="gate-save"
        className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-50"
      >
        Set
      </button>
      <button type="button" onClick={() => setEditing(false)} className="text-xs text-ink-muted underline">
        cancel
      </button>
    </div>
  );

  if (status.mode !== "none") {
    return (
      <div data-testid="gate-ok">
      <div className="flex items-center gap-2 text-xs">
        <span className="w-10 shrink-0 text-ink-muted">gate</span>
        <span className="shrink-0 text-ink-muted">{status.mode}</span>
        {status.command && (
          <ShellCmd cmd={status.command} className="min-w-0 flex-1 truncate font-mono text-ink-secondary" />
        )}
        {!editing && (
          <button
            type="button"
            onClick={() => {
              setMode(status.mode === "custom" ? "custom" : status.mode);
              setCommand(status.command ?? "");
              setEditing(true);
            }}
            data-testid="gate-edit"
            className="shrink-0 text-ink-muted underline"
          >
            edit
          </button>
        )}
      </div>
      {(status.setup || status.link_deps?.length) && (
        <div className="ml-12 mt-1 space-y-1 text-xs text-ink-muted" data-testid="gate-preparation">
          {status.setup && <ShellCmd cmd={status.setup} className="block font-mono" />}
          {status.link_deps?.length ? <span>link dependencies: {status.link_deps.join(", ")}</span> : null}
        </div>
      )}
      {editor}
      </div>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs" data-testid="gate-none">
      <span className="w-10 shrink-0 text-ink-muted">gate</span>
      <StatusChip role="serious" label="none — runs end UNVERIFIED" />
      {status.adoptable && !editing && (
        <button
          type="button"
          onClick={onAdopt}
          data-testid="gate-adopt"
          title={status.detected_command}
          className="rounded border border-hairline px-2 py-0.5 text-xs"
        >
          use {status.detected_command}
        </button>
      )}
      {!editing && (
        <button
          type="button"
          onClick={() => setEditing(true)}
          data-testid="gate-set-manual"
          className="rounded border border-hairline px-2 py-0.5 text-xs"
        >
          set by hand
        </button>
      )}
      {editor}
    </div>
  );
}
