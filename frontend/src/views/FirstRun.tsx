/**
 * The first screen of an installation with no project (B-455).
 *
 * Now used to render nothing until a project existed, and the only way
 * forward was Settings → Projects → Project management: a person could launch
 * Ducklab successfully and see no door at all. This page has one dominant
 * action and says, before anything is attempted, whether a model is ready to
 * draft with — the prerequisite every later step depends on.
 *
 * Readiness is read from what the engine already reports (providers with
 * their key presence, ducklings with their provider). Nothing is probed here:
 * a probe spends money, and a first screen must not.
 */

import { useCallback, useEffect, useState } from "react";
import type { Duckling, EngineClient, ProjectStartResult, ProviderView } from "../api/client";
import { StartProject } from "../components/StartProject";
import { routeHref } from "../app/routes";
import { StatusChip } from "../components/StatusChip";

export type Readiness = {
  /** Ducklings whose provider exists and has the key it names (or needs none). */
  usable: string[];
  /** Providers that name a key the engine's environment does not have. */
  missingKeys: { provider: string; env: string }[];
  /** True when no duckling is configured at all. */
  noDucklings: boolean;
  /** True when no provider is configured at all. */
  noProviders: boolean;
  /** A configured OpenRouter provider whose key is already available. */
  openRouter?: string;
  /** Every configured id, including ducklings whose provider is unavailable. */
  ducklingIDs: string[];
};

export function readiness(providers: readonly ProviderView[], ducklings: readonly Duckling[]): Readiness {
  const byId = new Map(providers.map((p) => [p.id, p]));
  const usable = ducklings
    .filter((d) => {
      const p = byId.get(d.provider);
      return !!p && (!p.api_key_env || p.key_present);
    })
    .map((d) => d.id);
  const missingKeys = providers
    .filter((p) => p.api_key_env && !p.key_present)
    .map((p) => ({ provider: p.id, env: p.api_key_env as string }));
  const openRouter = providers.find((p) =>
    p.key_present && (p.id === "openrouter" || p.base_url.includes("openrouter.ai")),
  )?.id;
  return { usable, missingKeys, noDucklings: ducklings.length === 0, noProviders: providers.length === 0, openRouter, ducklingIDs: ducklings.map((d) => d.id) };
}

export const OPENROUTER_STARTERS = [
  { id: "pato-gemini", model: "google/gemini-3.7-flash", label: "Gemini 3.7 Flash", vision: true, note: "recommended · lower cost" },
  { id: "pato-qwen", model: "qwen/qwen3.6-flash", label: "Qwen 3.6 Flash", vision: true, note: "alternative" },
] as const;

export function availableDucklingID(base: string, existing: readonly string[]): string {
  const used = new Set(existing);
  if (!used.has(base)) return base;
  let suffix = 2;
  while (used.has(`${base}-${suffix}`)) suffix++;
  return `${base}-${suffix}`;
}

export function friendlyProbeError(id: string, message: string): string {
  if (id === "pato-local" && /404|not found|localhost|127\.0\.0\.1/i.test(message)) {
    return "Nothing at the local model address is answering chats. Start your local model server, or add a hosted model below.";
  }
  return message;
}

export function FirstRun({
  client,
  connected,
  onStarted,
}: {
  client: EngineClient;
  connected: boolean;
  /** Called once the project exists (and its intake run started, when it could). */
  onStarted: (result: ProjectStartResult) => void;
}) {
  const [state, setState] = useState<Readiness | null>(null);
  // A project that exists while its intake could not start (no usable model,
  // say) is shown here before moving on, so the reason is not lost.
  const [stalled, setStalled] = useState<ProjectStartResult | null>(null);
  const [failed, setFailed] = useState(false);
  const [adding, setAdding] = useState("");
  const [addFailure, setAddFailure] = useState("");
  const [providerModels, setProviderModels] = useState<string[] | null>(null);

  const load = useCallback(() => {
    let live = true;
    Promise.all([client.providers(), client.ducklings()])
      .then(async ([providers, ducklings]) => {
        const next = readiness(providers, ducklings);
        if (live) setState(next);
        if (!next.openRouter) {
          if (live) setProviderModels(null);
          return;
        }
        if (live) setProviderModels(null);
        try {
          const models = await client.providerModels(next.openRouter);
          if (live) setProviderModels(models);
        } catch {
          if (live) setProviderModels([]);
        }
      })
      .catch(() => {
        if (live) setFailed(true);
      });
    return () => {
      live = false;
    };
  }, [client]);

  useEffect(() => load(), [load]);

  const addStarter = (starter: (typeof OPENROUTER_STARTERS)[number]) => {
    if (!state?.openRouter) return;
    const id = availableDucklingID(starter.id, state.ducklingIDs);
    setAdding(id);
    setAddFailure("");
    void client.ducklingSet(id, {
      provider: state.openRouter,
      model: starter.model,
      caps: { native_tools: true, vision: starter.vision },
      actor: "human",
      create_only: true,
    })
      .then(() => client.ducklingProbe(id))
      .then(() => {
        setTests((current) => ({ ...current, [id]: "ok" }));
        load();
      })
      .catch((error) => setAddFailure(error instanceof Error ? error.message : String(error)))
      .finally(() => setAdding(""));
  };

  const modelReady = !!state && state.usable.length > 0;
  // A configured duckling is a claim, not a working model: the starter config
  // ships one pointed at localhost:8080 that a fresh machine rarely serves.
  // Testing is the person's click, because a probe of a hosted model spends
  // (a few) tokens.
  const [tests, setTests] = useState<Record<string, "testing" | "ok" | string>>({});
  const testModels = () => {
    for (const id of (state?.usable ?? []).slice(0, 3)) {
      setTests((t) => ({ ...t, [id]: "testing" }));
      client
        .ducklingProbe(id)
        .then(() => setTests((t) => ({ ...t, [id]: "ok" })))
        .catch((e) => setTests((t) => ({ ...t, [id]: e instanceof Error ? e.message : String(e) })));
    }
  };
  const tested = Object.values(tests);
  const anyOk = tested.includes("ok");

  return (
    <section className="mx-auto max-w-2xl space-y-5 p-6" data-testid="first-run">
      <div className="space-y-2">
        <h2 className="text-lg font-medium text-ink">Start your first project</h2>
        <p className="text-sm text-ink-secondary">
          Describe what you want to build. Ducklab turns the description into requirements, a
          specification and a plan that you approve one at a time; then models write the code and
          your project&apos;s tests check it.
        </p>
      </div>


      <div className="space-y-2 rounded-card border border-hairline p-3" data-testid="first-run-readiness">
        <h3 className="text-xs font-medium text-ink-muted">Before the first draft</h3>
        <ul className="space-y-2 text-sm">
          <li data-testid="first-run-engine">
            {/* This page renders only after the engine answered the project
                list, so the engine is reachable; what can still be pending is
                the live event stream. */}
            <StatusChip role={connected ? "good" : "warning"} label={connected ? "engine connected" : "engine reachable · live updates connecting"} />
          </li>
          <li data-testid="first-run-model">
            {failed ? (
              <StatusChip role="warning" label="could not read the model setup" />
            ) : !state ? (
              <span className="text-ink-muted">Checking the model setup…</span>
            ) : modelReady ? (
              <div className="space-y-1">
                <div className="flex flex-wrap items-center gap-2">
                  <StatusChip
                    role={anyOk ? "good" : "warning"}
                    label={`${state.usable.length} model${state.usable.length === 1 ? "" : "s"} configured (${state.usable.slice(0, 3).join(", ")}${state.usable.length > 3 ? ", …" : ""})${anyOk ? " · answering" : tested.length === 0 ? " · not tested yet" : ""}`}
                  />
                  <button
                    type="button"
                    onClick={testModels}
                    disabled={tested.includes("testing")}
                    data-testid="first-run-test-models"
                    className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-40"
                  >
                    {tested.includes("testing") ? "Testing…" : "Test connection"}
                  </button>
                </div>
                {Object.entries(tests).filter(([, r]) => r !== "ok" && r !== "testing").map(([id, r]) => (
                  <p key={id} className="text-xs text-ink-secondary" data-testid="first-run-test-failure">
                    {friendlyProbeError(id, r)}{" "}
                    <a href={routeHref({ name: "settings", section: "ducklings" })} className="text-ink underline">
                      Fix the model setup
                    </a>
                  </p>
                ))}
              </div>
            ) : (
              <div className="space-y-1">
                <StatusChip role="critical" label="no model ready to draft with" />
                <p className="text-xs text-ink-secondary" data-testid="first-run-model-fix">
                  {state.noProviders
                    ? "Ducklab reaches models through a provider (OpenRouter, a local server). Add one, then add a duckling: a model on that provider."
                    : state.noDucklings
                      ? "A provider exists, but no duckling (a model on that provider) does. Add one."
                      : "The ducklings configured cannot reach their provider; see the missing keys below."}{" "}
                  <a href={routeHref({ name: "settings", section: "ducklings" })} className="text-ink underline">
                    Set up a model
                  </a>
                </p>
              </div>
            )}
          </li>
          {state?.openRouter && !state.usable.some((id) => id !== "pato-local") && (
            <li className="rounded border border-good p-2" data-testid="first-run-openrouter">
              <p className="text-sm font-medium text-ink">OpenRouter key found — pick a model</p>
              <p className="mt-1 text-xs text-ink-secondary">Ducklab will create and test it here; no settings detour required.</p>
              <div className="mt-2 flex flex-wrap gap-2">
                {OPENROUTER_STARTERS.filter((starter) => providerModels?.includes(starter.model)).map((starter) => (
                  <button
                    key={starter.id}
                    type="button"
                    disabled={adding !== ""}
                    onClick={() => addStarter(starter)}
                    data-testid={`first-run-add-${starter.id}`}
                    className="rounded border border-hairline px-2 py-1 text-left text-xs disabled:opacity-40"
                  >
                    <span className="font-medium">{starter.label}</span>
                    {starter.vision ? <span title="accepts image references"> · vision</span> : null}
                    <span> · {starter.note}</span>
                    <span className="block font-mono text-ink-muted">{starter.model}</span>
                    <span className="block text-ink-muted">save as {availableDucklingID(starter.id, state.ducklingIDs)}</span>
                  </button>
                ))}
              </div>
              {providerModels !== null && !OPENROUTER_STARTERS.some((starter) => providerModels.includes(starter.model)) && (
                <p className="mt-2 text-xs text-ink-secondary">No verified starter is in the provider catalog right now. Add a current model in Settings.</p>
              )}
              {addFailure && <p className="mt-2 text-xs text-critical" data-testid="first-run-add-failure">{addFailure}</p>}
            </li>
          )}
          {state && state.missingKeys.length > 0 && (
            <li className="text-xs text-ink-secondary" data-testid="first-run-missing-keys">
              {state.missingKeys.map((k) => (
                <p key={k.provider}>
                  Provider <code>{k.provider}</code> needs <code>{k.env}</code> in the engine&apos;s
                  environment. Ducklab never stores keys: set the variable where the engine starts,
                  then restart the engine from Settings → engine.
                </p>
              ))}
            </li>
          )}
        </ul>
      </div>
      {stalled ? (
        <div className="space-y-2 rounded-card border border-warning p-3" data-testid="first-run-stalled">
          <p className="text-sm text-ink">
            Project <strong>{stalled.project.name}</strong> was created at <code>{stalled.project.path}</code>, but drafting
            could not start: {stalled.intake_error}
          </p>
          <button
            type="button"
            onClick={() => onStarted(stalled)}
            className="rounded border border-hairline px-3 py-1 text-xs"
            data-testid="first-run-continue"
          >
            Continue to the project
          </button>
        </div>
      ) : (
        <section className="rounded-card border border-hairline p-4" data-testid="first-run-create">
          <h3 className="mb-3 text-sm font-medium text-ink">Create your first project</h3>
          <StartProject
            client={client}
            onStarted={(result) => (result.run_id ? onStarted(result) : setStalled(result))}
            modelWarning={anyOk ? undefined : "No model has answered a test yet. You can create the project now, but drafting needs a model that answers: use Test connection above first."}
          />
        </section>
      )}
    </section>
  );
}
