import { useCallback, useEffect, useState } from "react";
import type { EngineClient, SkillSummary, SkillDetail } from "../api/client";

/** The skills loop's desktop surface (spec 08 §4.9, AC-57). The engine, CLI
 * and tool belt have carried skills since v0.5 while the desktop showed
 * nothing — the exact gap the coverage test listed as "no desktop surface at
 * all". A skill is a directory with a SKILL.md: documentation-only skills
 * brief a seat (a survey guide for an adopt), runnable ones execute an entry
 * script through the shell policy. One authored by a duckling during a run
 * shows here as pending and stays grey until its run is accepted. */
export function Skills({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [skills, setSkills] = useState<SkillSummary[] | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [detail, setDetail] = useState<SkillDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [newRunnable, setNewRunnable] = useState(false);
  const [runArgs, setRunArgs] = useState<Record<string, string>>({});
  const [runOut, setRunOut] = useState<{ name: string; output: string; exit: number } | null>(null);
  const [running, setRunning] = useState(false);

  const reload = useCallback(() => {
    client
      .skills(projectId)
      .then((r: { skills?: SkillSummary[] }) => setSkills(r.skills ?? []))
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [client, projectId]);
  useEffect(() => {
    setSkills(null);
    setOpen(null);
    setDetail(null);
    reload();
  }, [reload]);

  const openSkill = (name: string) => {
    if (open === name) {
      setOpen(null);
      setDetail(null);
      return;
    }
    setOpen(name);
    setDetail(null);
    setRunOut(null);
    setRunArgs({});
    client
      .skillGet(projectId, name)
      .then(setDetail)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  };

  const create = async () => {
    setError(null);
    try {
      await client.skillNew(projectId, newName.trim(), newRunnable);
      setCreating(false);
      setNewName("");
      reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const run = async (sk: SkillDetail) => {
    setRunning(true);
    setRunOut(null);
    setError(null);
    try {
      const args: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(runArgs)) if (v.trim() !== "") args[k] = v;
      const r = await client.skillRun(projectId, sk.name ?? "", args);
      setRunOut({ name: sk.name ?? "", output: r.output ?? "", exit: r.exit_code ?? 0 });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setRunning(false);
    }
  };

  return (
    <div data-testid="skills-view" className="mx-auto max-w-3xl">
      <div className="mb-3 flex items-baseline justify-between">
        <div>
          <h1 className="text-lg text-ink">Skills</h1>
          <p className="text-xs text-ink-muted">
            Directories with a SKILL.md — project ones live in .ducklab/skills and shadow global
            ones on a name collision. A documentation skill briefs a seat (the architect reads a
            survey guide before an adopt); a runnable one executes its entry script. Ducklings
            propose skills with ordinary writes; one stays pending until its run is accepted.
          </p>
        </div>
        <button
          type="button"
          data-testid="skill-new-toggle"
          onClick={() => setCreating((v) => !v)}
          className="ml-3 shrink-0 rounded border border-hairline px-3 py-1 text-sm"
        >
          {creating ? "Cancel" : "New skill"}
        </button>
      </div>

      {error && (
        <p className="mb-2 text-xs text-warn" data-testid="skills-error">
          {error}
        </p>
      )}

      {creating && (
        <section className="mb-4 rounded-card border border-hairline bg-surface2 p-3" data-testid="skill-new">
          <p className="text-sm text-ink">Scaffold a skill in this project's .ducklab/skills/.</p>
          <div className="mt-2 flex items-center gap-3">
            <input
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="survey-map"
              data-testid="skill-new-name"
              className="rounded border border-hairline bg-surface px-2 py-1 text-sm"
            />
            <label className="flex items-center gap-1 text-xs text-ink-secondary">
              <input
                type="checkbox"
                checked={newRunnable}
                onChange={(e) => setNewRunnable(e.target.checked)}
                data-testid="skill-new-runnable"
              />
              runnable (scaffolds run.sh)
            </label>
            <button
              type="button"
              onClick={() => void create()}
              disabled={!newName.trim()}
              data-testid="skill-new-create"
              className="rounded border border-hairline px-3 py-1 text-sm disabled:opacity-50"
            >
              Create
            </button>
          </div>
          <p className="mt-1 text-xs text-ink-muted">
            The scaffold is a template: it will not load until you replace its placeholders —
            open SKILL.md and write the description (say WHEN to use it) and the body.
          </p>
        </section>
      )}

      {skills === null ? (
        <p className="text-sm text-ink-muted">Loading…</p>
      ) : skills.length === 0 ? (
        <p className="text-sm text-ink-muted" data-testid="skills-empty">
          No skills yet. A good first one: a documentation skill with the project's survey map —
          the module list, where the routes live, which schema is the truth — so an adopt's
          architect sweeps instead of wanders.
        </p>
      ) : (
        <ul className="space-y-2" data-testid="skills-list">
          {skills.map((sk) => (
            <li
              key={`${sk.scope}:${sk.name}`}
              className={`rounded-card border border-hairline p-3 ${sk.pending ? "opacity-60" : ""}`}
              data-testid="skill-row"
            >
              <div className="flex items-baseline gap-2">
                <button
                  type="button"
                  onClick={() => openSkill(sk.name ?? "")}
                  className="text-sm text-ink underline-offset-2 hover:underline"
                  data-testid={`skill-open-${sk.name}`}
                >
                  {sk.name}
                </button>
                <span className="rounded border border-hairline px-1 text-[10px] text-ink-muted">
                  {sk.scope}
                </span>
                <span className="text-[10px] text-ink-muted">
                  {sk.runnable ? "runnable" : "documentation"}
                </span>
                {sk.pending && (
                  <span className="text-[10px] text-warn" data-testid="skill-pending">
                    pending acceptance — unusable until its run is accepted
                  </span>
                )}
              </div>
              <p className="mt-1 text-xs text-ink-secondary">{sk.description}</p>
              {(sk.problems?.length ?? 0) > 0 && (
                <ul className="mt-1 text-xs text-warn" data-testid="skill-problems">
                  {sk.problems!.map((p) => (
                    <li key={p}>⚠ {p}</li>
                  ))}
                </ul>
              )}
              {open === sk.name && detail && (
                <div className="mt-2 border-t border-hairline pt-2" data-testid="skill-detail">
                  {(detail.args?.length ?? 0) > 0 && (
                    <p className="text-xs text-ink-muted">
                      args:{" "}
                      {detail.args!
                        .map((a) => `${a.name}${a.required ? "" : "?"} (${a.type || "string"})`)
                        .join(", ")}
                    </p>
                  )}
                  <pre className="mt-1 max-h-96 overflow-auto whitespace-pre-wrap text-xs text-ink-secondary">
                    {detail.body}
                  </pre>
                  {detail.entry && !sk.pending && (
                    <div className="mt-2">
                      {(detail.args ?? []).map((a) => (
                        <input
                          key={a.name}
                          value={runArgs[a.name ?? ""] ?? ""}
                          onChange={(e) => setRunArgs((m) => ({ ...m, [a.name ?? ""]: e.target.value }))}
                          placeholder={`${a.name}${a.required ? " (required)" : ""}`}
                          className="mb-1 mr-2 rounded border border-hairline bg-surface px-2 py-1 text-xs"
                        />
                      ))}
                      <button
                        type="button"
                        onClick={() => void run(detail)}
                        disabled={running}
                        data-testid="skill-run"
                        className="rounded border border-hairline px-3 py-1 text-xs disabled:opacity-50"
                      >
                        {running ? "Running…" : "Run"}
                      </button>
                      {runOut && runOut.name === sk.name && (
                        <pre
                          className="mt-2 max-h-60 overflow-auto whitespace-pre-wrap rounded border border-hairline bg-surface2 p-2 text-xs"
                          data-testid="skill-run-output"
                        >
                          {`exit ${runOut.exit}\n${runOut.output}`}
                        </pre>
                      )}
                    </div>
                  )}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
