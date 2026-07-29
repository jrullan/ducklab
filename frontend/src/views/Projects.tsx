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
import type { EngineClient, GateStatus, Project } from "../api/client";
import { canChooseDirectory, chooseDirectory } from "../lib/picker";
import { StatusChip } from "../components/StatusChip";

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
  const [renaming, setRenaming] = useState<string | null>(null);
  const [renameTo, setRenameTo] = useState("");

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
        }
      })
      .catch((err) => setFailure(err instanceof Error ? err.message : String(err)));
  }, [client]);

  useEffect(load, [load]);

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

      <section className="rounded-card border border-hairline p-3">
        <h3 className="mb-2 text-ink">Projects</h3>
        {projects.length === 0 ? (
          <p className="text-sm text-ink-muted">None yet.</p>
        ) : (
          <ul className="space-y-1">
            {projects.map((p) => (
              <li
                key={p.id}
                data-testid={`project-row-${p.id}`}
                className={
                  "flex flex-wrap items-center gap-2 rounded p-2 " +
                  (p.id === selected ? "bg-surface2" : "")
                }
              >
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
                      className="text-left text-ink"
                    >
                      {p.name || p.id}
                    </button>
                    {/* Not a warning chip on a missing project: it is the whole
                        reason the board looks empty, so it is stated plainly. */}
                    {p.missing && <StatusChip role="critical" label="folder is gone" />}
                    <GateChip
                      status={gates[p.id]}
                      onAdopt={() =>
                        void client
                          .projectGateAdopt(p.id)
                          .then((g) => setGates((cur) => ({ ...cur, [p.id]: g })))
                          .catch((err) => setFailure(err instanceof Error ? err.message : String(err)))
                      }
                    />
                    <span className="font-mono text-xs text-ink-muted">{p.path}</span>
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
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

/** What a project's gate is, and the one thing worth doing about it.
 *
 * A gate with nothing runnable behind it means every run ends UNVERIFIED — the
 * failure mode is that nobody notices, so this says the consequence rather
 * than the setting. */
function GateChip({ status, onAdopt }: { status?: GateStatus; onAdopt: () => void }) {
  if (!status) return null;
  if (status.mode !== "none") {
    return (
      <span className="text-xs text-ink-muted" data-testid="gate-ok">
        gate {status.mode}
      </span>
    );
  }
  return (
    <span className="flex items-center gap-1" data-testid="gate-none">
      <StatusChip role="serious" label="no gate — runs end UNVERIFIED" />
      {status.adoptable && (
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
    </span>
  );
}
