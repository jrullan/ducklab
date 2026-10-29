import { useCallback, useEffect, useState } from "react";
import type { AgentGrant, AgentToken, EngineClient, Project } from "../api/client";
import { StatusChip } from "../components/StatusChip";
import { ErrorCard } from "../components/ErrorCard";

type Level = AgentGrant["level"] | "none";

const LEVELS: { value: Level; label: string }[] = [
  { value: "none", label: "no access" },
  { value: "read-only", label: "read-only" },
  { value: "full", label: "full" },
];

function levelOf(grants: AgentGrant[] | null | undefined, project: string): Level {
  return grants?.find((g) => g.project === project)?.level ?? "none";
}

function withLevel(grants: AgentGrant[] | null | undefined, project: string, level: Level): AgentGrant[] {
  const rest = (grants ?? []).filter((g) => g.project !== project);
  return level === "none" ? rest : [...rest, { project, level }];
}

const day = (iso?: string) => (iso ? iso.slice(0, 10) : "");

/**
 * Agent access (B-550): the person issues each agent its own token and
 * grants it projects. The engine records the agent under the token's name and
 * refuses whatever the grants do not cover. Every control here is the
 * person's — the engine refuses them to an agent's credential.
 */
export function AgentAccess({ client }: { client: EngineClient }) {
  const [tokens, setTokens] = useState<AgentToken[]>([]);
  const [legacy, setLegacy] = useState<boolean | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [draft, setDraft] = useState<AgentGrant[]>([]);
  const [secret, setSecret] = useState<{ name: string; value: string } | null>(null);
  const [confirmRevoke, setConfirmRevoke] = useState<string | null>(null);

  const fail = (e: unknown) => setError(e instanceof Error ? e.message : String(e));

  const load = useCallback(() => {
    void client
      .agentTokens()
      .then((list) => {
        setTokens(list.items ?? []);
        setLegacy(list.legacy_enabled);
      })
      .catch(fail);
    void client.projects().then(setProjects).catch(fail);
  }, [client]);

  useEffect(load, [load]);

  const create = () => {
    setError(null);
    void client
      .agentTokenCreate(name.trim(), draft)
      .then((out) => {
        setSecret({ name: out.token.name, value: out.secret });
        setName("");
        setDraft([]);
        load();
      })
      .catch(fail);
  };

  const setGrant = (token: AgentToken, project: string, level: Level) => {
    setError(null);
    void client.agentTokenSetGrants(token.id, withLevel(token.grants, project, level)).then(load).catch(fail);
  };

  const revoke = (id: string) => {
    setError(null);
    setConfirmRevoke(null);
    void client.agentTokenRevoke(id).then(load).catch(fail);
  };

  const toggleLegacy = () => {
    if (legacy === null) return;
    setError(null);
    void client
      .agentTokenLegacySet(!legacy)
      .then((out) => setLegacy(out.legacy_enabled))
      .catch(fail);
  };

  const live = tokens.filter((t) => !t.revoked_at);
  const revoked = tokens.filter((t) => t.revoked_at);

  return (
    <section className="mb-4 rounded-card border border-hairline p-4" data-testid="agent-access">
      <h2 className="text-sm font-medium text-ink">agent access</h2>
      <p className="mt-0.5 text-xs text-ink-muted">
        each agent gets its own token and reaches only the projects you grant it, recorded under the token&apos;s name
      </p>

      <div className="mt-3 flex flex-wrap items-center gap-2" data-testid="agent-access-legacy">
        <span className="text-sm text-ink">shared agent token</span>
        {legacy !== null && <StatusChip role={legacy ? "warning" : "good"} label={legacy ? "on" : "off"} />}
        <button
          type="button"
          data-testid="agent-access-legacy-toggle"
          disabled={legacy === null}
          onClick={toggleLegacy}
          className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-40"
        >
          {legacy ? "Turn off" : "Turn on"}
        </button>
        <p className="w-full text-xs text-ink-muted">
          {legacy
            ? "any agent that reads engine.json has full access to every project. Turn it off once each agent has its own token."
            : "only agents holding their own token (DUCKLAB_TOKEN) reach the engine."}
        </p>
      </div>

      {secret && (
        <div className="mt-3 rounded border border-warning p-3" data-testid="agent-access-secret">
          <p className="text-xs text-ink">
            Token for <strong>{secret.name}</strong> — shown this once. Put it in the agent&apos;s MCP server environment:
          </p>
          <code className="mt-1 block select-all break-all font-mono text-xs text-ink">DUCKLAB_TOKEN={secret.value}</code>
          <div className="mt-2 flex gap-2">
            <button
              type="button"
              onClick={() => void navigator.clipboard?.writeText(secret.value)}
              className="rounded border border-hairline px-2 py-0.5 text-xs"
            >
              Copy
            </button>
            <button
              type="button"
              data-testid="agent-access-secret-done"
              onClick={() => setSecret(null)}
              className="rounded border border-hairline px-2 py-0.5 text-xs"
            >
              Done
            </button>
          </div>
        </div>
      )}

      {error && <ErrorCard error={error} testId="agent-access-error" />}

      <ul className="mt-3 space-y-3">
        {live.map((t) => (
          <li key={t.id} className="rounded border border-hairline p-2" data-testid={`agent-token-${t.id}`}>
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="text-ink">{t.name}</span>
              <span className="font-mono text-xs text-ink-muted">{t.actor}</span>
              <span className="font-mono text-xs text-ink-muted">{t.hint}…</span>
              <span className="text-xs text-ink-muted">
                {t.last_used_at ? `last used ${day(t.last_used_at)}` : "never used"}
              </span>
              {confirmRevoke === t.id ? (
                <button
                  type="button"
                  data-testid={`agent-token-revoke-confirm-${t.id}`}
                  onClick={() => revoke(t.id)}
                  className="ml-auto rounded border border-critical px-2 py-0.5 text-xs text-critical"
                >
                  Revoke for good
                </button>
              ) : (
                <button
                  type="button"
                  data-testid={`agent-token-revoke-${t.id}`}
                  onClick={() => setConfirmRevoke(t.id)}
                  className="ml-auto rounded border border-hairline px-2 py-0.5 text-xs"
                >
                  Revoke
                </button>
              )}
            </div>
            <GrantTable
              projects={projects}
              grants={t.grants}
              onChange={(project, level) => setGrant(t, project, level)}
              testid={`agent-token-grants-${t.id}`}
            />
          </li>
        ))}
      </ul>
      {live.length === 0 && <p className="mt-3 text-xs text-ink-muted">no agent tokens yet</p>}

      <div className="mt-4 border-t border-hairline pt-3" data-testid="agent-access-new">
        <label className="flex items-center gap-2 text-sm">
          <span className="text-ink">new token for</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="OpenCode ti36"
            data-testid="agent-access-name"
            className="rounded border border-hairline bg-transparent px-2 py-0.5 text-sm"
          />
        </label>
        <GrantTable
          projects={projects}
          grants={draft}
          onChange={(project, level) => setDraft(withLevel(draft, project, level))}
          testid="agent-access-new-grants"
        />
        <button
          type="button"
          data-testid="agent-access-create"
          disabled={!name.trim()}
          onClick={create}
          className="mt-2 rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-40"
        >
          Issue token
        </button>
      </div>

      {revoked.length > 0 && (
        <details className="mt-3 text-xs text-ink-muted">
          <summary>{revoked.length} revoked</summary>
          <ul className="mt-1 space-y-0.5">
            {revoked.map((t) => (
              <li key={t.id}>
                {t.name} <span className="font-mono">{t.hint}…</span> — revoked {day(t.revoked_at)}
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  );
}

function GrantTable({
  projects,
  grants,
  onChange,
  testid,
}: {
  projects: Project[];
  grants: AgentGrant[] | null | undefined;
  onChange: (project: string, level: Level) => void;
  testid: string;
}) {
  if (projects.length === 0) return null;
  return (
    <table className="mt-2 text-xs" data-testid={testid}>
      <tbody>
        {projects.map((p) => (
          <tr key={p.id}>
            <td className="pr-3 text-ink-secondary">{p.id}</td>
            <td>
              <select
                aria-label={`access to ${p.id}`}
                value={levelOf(grants, p.id)}
                onChange={(e) => onChange(p.id, e.target.value as Level)}
                className="rounded border border-hairline bg-transparent px-1 py-0.5"
              >
                {LEVELS.map((l) => (
                  <option key={l.value} value={l.value}>
                    {l.label}
                  </option>
                ))}
              </select>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
