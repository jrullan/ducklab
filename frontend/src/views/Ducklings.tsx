/**
 * Ducklings — the fleet, and the endpoints it is reached through (08 §4.8).
 *
 * The premise of the project is comparing models, so being unable to add one
 * without hand-editing a TOML file and restarting the engine was the gap that
 * mattered most on this page.
 *
 * There is no field for an API key and there will not be one. A provider
 * records the *name* of an environment variable; the engine reads the value at
 * call time (I10). This page can therefore say "this needs OPENROUTER_API_KEY
 * and it is not set" without a key ever passing through it.
 */

import { useCallback, useEffect, useState } from "react";
import type { Duckling, EngineClient, ProviderView, RosterEntry } from "../api/client";
import { StatusChip } from "../components/StatusChip";
import { DuckAvatar } from "../components/DuckAvatar";
import { money } from "../lib/format";
import { assignDucklingColors } from "../lib/colors";

const ROLES = ["architect", "implementer", "reviewer", "judge", "triager", "scribe"] as const;

/** What each role is for, in the words of the prompt each one actually gets.
 *
 * Taken from the system prompts in internal/agent, not invented here: a
 * description that drifts from what the model is told is worse than none,
 * because it is believed. */
const ROLE_HELP: Record<string, string> = {
  architect:
    "Turns intent into a written artifact another model, with no memory of the conversation, can act on. Read-only: requirements, spec and plan.",
  implementer: "Changes the code until the task is done and the gate passes. The only role that writes.",
  reviewer:
    "Reads a change it did not write, and is told not to be agreeable. The tests have already run; their result is given to it and is not its to dispute.",
  judge:
    "Picks between candidates labelled A, B, … in a tournament. It is not told who wrote them and must not ask.",
  triager: "Classifies a bug report: severity, suspected files, whether it duplicates another.",
  scribe: "Writes release notes and changelog entries from the list of accepted work.",
};

export function Ducklings({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [ducklings, setDucklings] = useState<Duckling[]>([]);
  const [providers, setProviders] = useState<ProviderView[]>([]);
  const [failure, setFailure] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  // One colour per duckling, decided from the fleet rather than from whatever
  // list a view had to hand. Computed once per render, not once per card.
  const colors = assignDucklingColors(ducklings);

  const load = useCallback(() => {
    Promise.all([client.ducklings(), client.providers()])
      .then(([d, p]) => {
        setDucklings(d);
        setProviders(p);
      })
      .catch((err) => setFailure(err instanceof Error ? err.message : String(err)));
  }, [client]);

  useEffect(load, [load]);

  // One place for "it worked" and "it did not", so no caller has to remember
  // to both clear the error and reload.
  const done = (err?: unknown) => {
    setFailure(err ? (err instanceof Error ? err.message : String(err)) : null);
    if (!err) {
      setEditing(null);
      load();
    }
  };

  return (
    <div data-testid="ducklings-view" className="space-y-4 p-4">
      {failure && (
        <p className="text-critical" data-testid="fleet-error">
          {failure}
        </p>
      )}

      <ProviderSection client={client} providers={providers} onDone={done} />

      <section className="rounded-card border border-hairline p-3">
        <div className="mb-2 flex items-center gap-2">
          <h3 className="text-ink">Ducklings</h3>
          <button
            type="button"
            onClick={() => setEditing(editing === "" ? null : "")}
            data-testid="duckling-add"
            disabled={providers.length === 0}
            className="ml-auto rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-40"
          >
            Add duckling
          </button>
        </div>
        {providers.length === 0 && (
          // Said, rather than left as a disabled button with no explanation.
          <p className="mb-2 text-sm text-ink-muted">
            Add a provider first — a duckling is a model reached through one.
          </p>
        )}

        {editing !== null && (
          <DucklingForm
            key={editing}
            client={client}
            providers={providers}
            existing={ducklings.find((d) => d.id === editing)}
            onDone={done}
            onCancel={() => setEditing(null)}
          />
        )}

        {ducklings.length === 0 ? (
          <p className="text-sm text-ink-muted">None configured.</p>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" data-testid="ducklings">
            {ducklings.map((d) => (
              <DucklingCard
                key={d.id}
                duckling={d}
                roster={ducklings.map((x) => x.id)}
                color={colors[d.id]}
                provider={providers.find((p) => p.id === d.provider)}
                onEdit={() => setEditing(d.id)}
                onRemove={() => void client.ducklingRemove(d.id).then(() => done()).catch(done)}
                onSaved={done}
                client={client}
              />
            ))}
          </div>
        )}
      </section>

      {projectId && <RosterSection client={client} projectId={projectId} ducklings={ducklings} />}
    </div>
  );
}

/** Which duckling plays which role in this project.
 *
 * Shown as it will actually be used, not as the file declares it: an
 * undeclared role still gets a duckling, and hiding that would make the roster
 * look emptier than the runs behave. The source of each assignment is marked,
 * because a person needs to know which ones are theirs. */
function RosterSection({
  client,
  projectId,
  ducklings,
}: {
  client: EngineClient;
  projectId: string;
  ducklings: readonly Duckling[];
}) {
  const [entries, setEntries] = useState<RosterEntry[]>([]);
  const [warning, setWarning] = useState<string | undefined>();
  const [failure, setFailure] = useState<string | null>(null);

  const load = useCallback(() => {
    client
      .roster(projectId)
      .then((r) => {
        setEntries(r.entries);
        setWarning(r.warning);
      })
      .catch(() => {});
  }, [client, projectId]);

  useEffect(load, [load]);

  if (entries.length === 0) return null;

  return (
    <section className="rounded-card border border-hairline p-3" data-testid="roster-section">
      <h3 className="mb-2 text-ink">Roster for this project</h3>
      {warning && (
        // Running both sides on one duckling measures self-consistency, not
        // review (05 §3.2). Recorded, not blocked.
        <p className="mb-2 text-sm text-serious" data-testid="roster-warning">
          {warning}
        </p>
      )}
      {failure && <p className="mb-2 text-sm text-critical">{failure}</p>}
      <ul className="space-y-3">
        {entries.map((e) => (
          <li key={e.role} className="text-sm" data-testid={`roster-${e.role}`}>
            <div className="flex items-center gap-2">
              <span className="w-28 text-ink-secondary">{e.role}</span>
            <select
              aria-label={`duckling for ${e.role}`}
              data-testid={`roster-select-${e.role}`}
              value={e.duckling}
              onChange={(ev) =>
                void client
                  .rosterSet(projectId, e.role, ev.target.value)
                  .then(load)
                  .catch((err) => setFailure(err instanceof Error ? err.message : String(err)))
              }
              className="rounded border border-hairline bg-surface2 px-2 py-1 text-xs"
            >
              {ducklings.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.id}
                </option>
              ))}
            </select>
              <span className="text-xs text-ink-muted">
                {e.source === "project" ? "yours" : "chosen by the engine"}
              </span>
            </div>
            {/* What the role is for, said next to the choice rather than in
                documentation elsewhere. Deciding which model should review is
                a different question from deciding which should implement, and
                the names alone do not carry that. */}
            {ROLE_HELP[e.role] && (
              <p className="ml-28 pl-2 text-xs text-ink-muted" data-testid={`roster-help-${e.role}`}>
                {ROLE_HELP[e.role]}
              </p>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function DucklingCard({
  duckling: d,
  roster,
  color,
  provider,
  onEdit,
  onRemove,
  onSaved,
  client,
}: {
  duckling: Duckling;
  roster: string[];
  color?: string;
  provider?: ProviderView;
  onEdit: () => void;
  onRemove: () => void;
  onSaved: () => void;
  client: EngineClient;
}) {
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<string | null>(null);
  const [notesOpen, setNotesOpen] = useState(false);
  const [notesDraft, setNotesDraft] = useState("");
  const [notesBusy, setNotesBusy] = useState(false);

  // The only way to find out a duckling answers at all before committing a run
  // to it. A hosted model with an unset key looks identical to a working one
  // until something actually calls it.
  const onTest = () => {
    setTesting(true);
    setResult(null);
    client
      .ducklingTest(d.id, "Reply with exactly: OK")
      .then((r) =>
        setResult(
          `${r.text.trim().slice(0, 80)}  (${r.prompt_tokens ?? 0} in, ${r.completion_tokens ?? 0} out, $${(r.cost_usd ?? 0).toFixed(4)})`,
        ),
      )
      .catch((err) => setResult(err instanceof Error ? err.message : String(err)))
      .finally(() => setTesting(false));
  };

  return (
    <div className="rounded-card border border-hairline p-3" data-testid={`duckling-card-${d.id}`}>
      <header className="flex items-center gap-2">
        <DuckAvatar id={d.id} roster={roster} color={color} />
        <span className="text-md">{d.id}</span>
        <span className="ml-auto flex gap-1">
          <button
            type="button"
            onClick={onEdit}
            data-testid={`duckling-edit-${d.id}`}
            className="rounded border border-hairline px-2 py-0.5 text-xs"
          >
            Edit
          </button>
          <button
            type="button"
            onClick={onTest}
            disabled={testing}
            data-testid={`duckling-test-${d.id}`}
            title="Send one short prompt and report what came back"
            className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-40"
          >
            {testing ? "…" : "Test"}
          </button>
          <button
            type="button"
            onClick={onRemove}
            data-testid={`duckling-remove-${d.id}`}
            className="rounded border border-hairline px-2 py-0.5 text-xs"
          >
            Remove
          </button>
        </span>
      </header>
      <dl className="mt-2 text-sm text-ink-secondary">
        <div className="flex justify-between"><dt>provider</dt><dd>{d.provider}</dd></div>
        <div className="flex justify-between"><dt>model</dt><dd className="font-mono">{d.model}</dd></div>
        <div className="flex justify-between">
          <dt>tools</dt>
          <dd>{d.caps?.native_tools ? "native" : "text protocol"}</dd>
        </div>
        <div className="flex justify-between">
          <dt>context</dt>
          <dd className="tabular-nums">{(d.caps?.context_tokens ?? 0).toLocaleString()}</dd>
        </div>
        <div className="flex justify-between">
          <dt>cost / Mtok out</dt>
          <dd className="tabular-nums">{money(d.cost?.output_per_mtok ?? 0)}</dd>
        </div>
        {/* Shown on the card, not only in the edit form: whether a duckling
            spends its reply budget on thinking is the first thing to check when
            a run burns tokens and writes nothing, and 8192 is a cap someone set
            by not setting it. */}
        <div className="flex justify-between">
          <dt>max tokens / reply</dt>
          <dd className="tabular-nums">
            {d.params?.max_tokens
              ? d.params.max_tokens.toLocaleString()
              : "8,192 (default)"}
          </dd>
        </div>
        <div className="flex justify-between">
          <dt>thinking</dt>
          <dd>{d.params?.disable_thinking ? "suppressed" : "as the model sends it"}</dd>
        </div>
      </dl>
      {/* The person's own knowledge about this model — "fabricates gate
          state as reviewer", "great at pixel-level UI" — kept where the
          seat-picking happens. PUT replaces the whole duckling, so the save
          rebuilds the full body from the card: a notes edit must never wipe
          a cap or a cost. */}
      {notesOpen ? (
        <div className="mt-2 space-y-1" data-testid={`duckling-notes-editor-${d.id}`}>
          <textarea
            value={notesDraft}
            onChange={(e) => setNotesDraft(e.target.value)}
            rows={3}
            placeholder="notes about this duckling — what you learned running it"
            className="w-full rounded border border-hairline bg-surface2 px-2 py-1 text-xs"
          />
          <div className="flex gap-2">
            <button
              type="button"
              data-testid={`duckling-notes-save-${d.id}`}
              disabled={notesBusy}
              onClick={() => {
                setNotesBusy(true);
                void client
                  .ducklingSet(d.id, {
                    provider: d.provider,
                    model: d.model,
                    roles: d.roles ?? [],
                    notes: notesDraft.trim(),
                    params: d.params ?? {},
                    color: d.color ?? 0,
                    caps: {
                      native_tools: d.caps?.native_tools ?? false,
                      context_tokens: d.caps?.context_tokens ?? 0,
                      vision: d.caps?.vision,
                    },
                    cost: {
                      input_per_mtok: d.cost?.input_per_mtok ?? 0,
                      output_per_mtok: d.cost?.output_per_mtok ?? 0,
                    },
                  })
                  .then(() => {
                    setNotesOpen(false);
                    onSaved();
                  })
                  .catch(() => {})
                  .finally(() => setNotesBusy(false));
              }}
              className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-40"
            >
              {notesBusy ? "Saving…" : "Save notes"}
            </button>
            <button type="button" onClick={() => setNotesOpen(false)} className="text-xs text-ink-muted underline">
              cancel
            </button>
          </div>
        </div>
      ) : (
        <div className="mt-2" data-testid={`duckling-notes-${d.id}`}>
          {d.notes && <p className="whitespace-pre-wrap text-xs text-ink-secondary">{d.notes}</p>}
          <button
            type="button"
            data-testid={`duckling-notes-edit-${d.id}`}
            onClick={() => {
              setNotesDraft(d.notes ?? "");
              setNotesOpen(true);
            }}
            className="text-xs text-ink-muted underline"
          >
            {d.notes ? "edit notes" : "add notes"}
          </button>
        </div>
      )}
      {/* A duckling whose provider has no key will fail on its first call.
          Said here, where someone is choosing which one to run. */}
      {provider && provider.api_key_env && !provider.key_present && (
        <div className="mt-2">
          <StatusChip role="critical" label={`${provider.api_key_env} not set`} />
        </div>
      )}
      {(d.cost?.output_per_mtok ?? 0) === 0 && (
        <div className="mt-2"><StatusChip role="good" label="local — no USD cost" /></div>
      )}
      {result && (
        <p className="mt-2 break-words text-xs text-ink-secondary" data-testid={`duckling-result-${d.id}`}>
          {result}
        </p>
      )}
    </div>
  );
}

function ProviderSection({
  client,
  providers,
  onDone,
}: {
  client: EngineClient;
  providers: readonly ProviderView[];
  onDone: (err?: unknown) => void;
}) {
  const [open, setOpen] = useState(false);
  const [id, setId] = useState("");
  const [url, setUrl] = useState("");
  const [keyEnv, setKeyEnv] = useState("");

  const save = () => {
    void client
      .providerSet(id.trim(), { base_url: url.trim(), api_key_env: keyEnv.trim(), kind: "openai" })
      .then(() => {
        setOpen(false);
        setId("");
        setUrl("");
        setKeyEnv("");
        onDone();
      })
      .catch(onDone);
  };

  return (
    <section className="rounded-card border border-hairline p-3">
      <div className="mb-2 flex items-center gap-2">
        <h3 className="text-ink">Providers</h3>
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          data-testid="provider-add"
          className="ml-auto rounded border border-hairline px-2 py-0.5 text-xs"
        >
          Add provider
        </button>
      </div>

      {open && (
        <div className="mb-3 flex flex-wrap items-center gap-2" data-testid="provider-form">
          <input
            aria-label="provider id"
            data-testid="provider-id"
            placeholder="openrouter"
            value={id}
            onChange={(e) => setId(e.target.value)}
            className="w-36 rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
          />
          <input
            aria-label="base url"
            data-testid="provider-url"
            placeholder="https://openrouter.ai/api/v1"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            className="min-w-64 flex-1 rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
          />
          <input
            aria-label="api key environment variable"
            data-testid="provider-key-env"
            placeholder="OPENROUTER_API_KEY"
            value={keyEnv}
            onChange={(e) => setKeyEnv(e.target.value)}
            className="w-56 rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
          />
          <button
            type="button"
            onClick={save}
            disabled={!id.trim() || !url.trim()}
            data-testid="provider-save"
            className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-40"
          >
            Save
          </button>
          <p className="w-full text-xs text-ink-muted">
            That last field is the <em>name</em> of an environment variable, not a key. Ducklab
            never stores or transmits the key itself — the engine reads that variable when it makes
            a call. Leave it empty for a local server that needs none.
          </p>
        </div>
      )}

      {providers.length === 0 ? (
        <p className="text-sm text-ink-muted">None configured.</p>
      ) : (
        <ul className="space-y-1">
          {providers.map((p) => (
            <li
              key={p.id}
              data-testid={`provider-row-${p.id}`}
              className="flex flex-wrap items-center gap-2 p-1"
            >
              <span className="text-ink">{p.id}</span>
              <span className="font-mono text-xs text-ink-muted">{p.base_url}</span>
              {p.api_key_env ? (
                <StatusChip
                  role={p.key_present ? "good" : "critical"}
                  label={p.key_present ? `${p.api_key_env} set` : `${p.api_key_env} not set`}
                />
              ) : (
                <span className="text-xs text-ink-muted">no key needed</span>
              )}
              {p.in_use && p.in_use.length > 0 && (
                <span className="text-xs text-ink-muted">used by {p.in_use.join(", ")}</span>
              )}
              <button
                type="button"
                data-testid={`provider-remove-${p.id}`}
                onClick={() => void client.providerRemove(p.id).then(() => onDone()).catch(onDone)}
                className="ml-auto rounded border border-hairline px-2 py-0.5 text-xs"
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function DucklingForm({
  client,
  providers,
  existing,
  onDone,
  onCancel,
}: {
  client: EngineClient;
  providers: readonly ProviderView[];
  existing?: Duckling;
  onDone: (err?: unknown) => void;
  onCancel: () => void;
}) {
  const [id, setId] = useState(existing?.id ?? "");
  const [provider, setProvider] = useState(existing?.provider ?? providers[0]?.id ?? "");
  const [model, setModel] = useState(existing?.model ?? "");
  const [roles, setRoles] = useState<string[]>(existing?.roles ?? []);
  const [contextTokens, setContextTokens] = useState(String(existing?.caps?.context_tokens ?? ""));
  const [nativeTools, setNativeTools] = useState(existing?.caps?.native_tools !== false);
  const [costIn, setCostIn] = useState(String(existing?.cost?.input_per_mtok ?? 0));
  const [costOut, setCostOut] = useState(String(existing?.cost?.output_per_mtok ?? 0));
  // How the model is asked to generate. The engine has accepted these all
  // along; the form sent no `params` at all, so they were reachable only by
  // hand-editing config.toml — and posting an empty params wiped whatever a
  // hand-edit had put there.
  const [maxTokens, setMaxTokens] = useState(
    existing?.params?.max_tokens != null ? String(existing.params.max_tokens) : "",
  );
  const [temperature, setTemperature] = useState(
    existing?.params?.temperature != null ? String(existing.params.temperature) : "",
  );
  const [disableThinking, setDisableThinking] = useState(
    existing?.params?.disable_thinking ?? false,
  );
  // 0 means "decide from the fleet". A duckling that picks a slot keeps it
  // wherever it appears, which is the whole point: a colour that changes
  // between runs cannot be learned.
  const [color, setColor] = useState(existing?.color ?? 0);

  const save = () => {
    void client
      .ducklingSet(id.trim(), {
        provider,
        model: model.trim(),
        roles,
        // Empty means "do not send it": a temperature of 0 is a real choice and
        // an unset temperature is the endpoint's default, and collapsing the
        // two would silently change how every existing duckling generates.
        params: {
          max_tokens: maxTokens.trim() === "" ? null : Number(maxTokens) || null,
          temperature: temperature.trim() === "" ? null : Number(temperature),
          top_p: existing?.params?.top_p ?? null,
          disable_thinking: disableThinking,
          stop: existing?.params?.stop ?? null,
        },
        color,
        // Preserved, not re-collected: PUT replaces the whole duckling, and
        // the form used to silently wipe the fields it did not show.
        notes: existing?.notes ?? "",
        caps: {
          native_tools: nativeTools,
          context_tokens: Number(contextTokens) || 0,
          vision: existing?.caps?.vision,
        },
        cost: { input_per_mtok: Number(costIn) || 0, output_per_mtok: Number(costOut) || 0 },
      })
      .then(() => onDone())
      .catch(onDone);
  };

  return (
    <div className="mb-3 space-y-2 rounded border border-hairline p-2" data-testid="duckling-form">
      <div className="flex flex-wrap items-center gap-2">
        <input
          aria-label="duckling id"
          data-testid="duckling-id"
          placeholder="pato-sonnet"
          value={id}
          // The id is the name runs and reports are recorded under, so
          // changing it would orphan every measurement already taken.
          disabled={Boolean(existing)}
          onChange={(e) => setId(e.target.value)}
          className="w-40 rounded border border-hairline bg-surface2 px-2 py-1 text-sm disabled:opacity-60"
        />
        <select
          aria-label="provider"
          data-testid="duckling-provider"
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
          className="rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
        >
          {providers.map((p) => (
            <option key={p.id} value={p.id}>
              {p.id}
            </option>
          ))}
        </select>
        <input
          aria-label="model"
          data-testid="duckling-model"
          placeholder="anthropic/claude-sonnet-4.5"
          value={model}
          onChange={(e) => setModel(e.target.value)}
          className="min-w-56 flex-1 rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
        />
      </div>

      <div className="flex flex-wrap items-center gap-3 text-sm text-ink-secondary">
        <label className="flex items-center gap-1">
          context
          <input
            aria-label="context tokens"
            data-testid="duckling-context"
            value={contextTokens}
            onChange={(e) => setContextTokens(e.target.value)}
            className="w-24 rounded border border-hairline bg-surface2 px-2 py-1"
          />
        </label>
        <label className="flex items-center gap-1">
          <input
            type="checkbox"
            checked={nativeTools}
            data-testid="duckling-native-tools"
            onChange={(e) => setNativeTools(e.target.checked)}
          />
          native tool calling
        </label>
        <label className="flex items-center gap-1">
          $/Mtok in
          <input
            aria-label="cost in"
            data-testid="duckling-cost-in"
            value={costIn}
            onChange={(e) => setCostIn(e.target.value)}
            className="w-20 rounded border border-hairline bg-surface2 px-2 py-1"
          />
        </label>
        <label className="flex items-center gap-1">
          out
          <input
            aria-label="cost out"
            data-testid="duckling-cost-out"
            value={costOut}
            onChange={(e) => setCostOut(e.target.value)}
            className="w-20 rounded border border-hairline bg-surface2 px-2 py-1"
          />
        </label>
      </div>

      <div className="flex flex-wrap items-center gap-2 text-sm text-ink-secondary">
        <span>roles:</span>
        {ROLES.map((r) => (
          <label key={r} className="flex items-center gap-1">
            <input
              type="checkbox"
              checked={roles.includes(r)}
              data-testid={`duckling-role-${r}`}
              onChange={(e) =>
                setRoles((cur) => (e.target.checked ? [...cur, r] : cur.filter((x) => x !== r)))
              }
            />
            {r}
          </label>
        ))}
        <span className="text-xs text-ink-muted">empty means any role — the roster decides</span>
      </div>

      {/* How the model generates. max_tokens is the cap on one reply, and a
          reasoning model without one spends its whole output budget thinking
          and returns nothing — which reads as a transport fault rather than a
          setting. */}
      <div className="flex flex-wrap items-center gap-3 text-sm text-ink-secondary">
        <label className="flex items-center gap-1">
          max tokens
          <input
            aria-label="max tokens"
            data-testid="duckling-max-tokens"
            placeholder="endpoint default"
            value={maxTokens}
            onChange={(e) => setMaxTokens(e.target.value)}
            className="w-32 rounded border border-hairline bg-surface2 px-2 py-1"
          />
        </label>
        <label className="flex items-center gap-1">
          temperature
          <input
            aria-label="temperature"
            data-testid="duckling-temperature"
            placeholder="endpoint default"
            value={temperature}
            onChange={(e) => setTemperature(e.target.value)}
            className="w-32 rounded border border-hairline bg-surface2 px-2 py-1"
          />
        </label>
        <label
          className="flex items-center gap-1"
          title="Ask the endpoint not to spend the output budget on hidden reasoning. Only ever a request: what makes it safe is that an inline think block is stripped afterwards."
        >
          <input
            type="checkbox"
            checked={disableThinking}
            data-testid="duckling-disable-thinking"
            onChange={(e) => setDisableThinking(e.target.checked)}
          />
          suppress thinking
        </label>
      </div>

      {/* Eight slots is what the palette has. Past that it stops clearing the
          colour-vision floor, so a ninth would only look distinct. */}
      <div className="flex flex-wrap items-center gap-2 text-sm text-ink-secondary">
        colour
        <button
          type="button"
          aria-label="colour from the fleet"
          data-testid="duckling-color-0"
          aria-pressed={color === 0}
          onClick={() => setColor(0)}
          className={
            "rounded border px-2 py-1 text-xs " +
            (color === 0 ? "border-serious" : "border-hairline")
          }
        >
          auto
        </button>
        {[1, 2, 3, 4, 5, 6, 7, 8].map((slot) => (
          <button
            key={slot}
            type="button"
            aria-label={`colour ${slot}`}
            data-testid={`duckling-color-${slot}`}
            aria-pressed={color === slot}
            onClick={() => setColor(slot)}
            className={
              "h-6 w-6 rounded-full border-2 " +
              (color === slot ? "border-ink" : "border-transparent")
            }
            style={{ backgroundColor: `var(--series-${slot})` }}
          />
        ))}
      </div>

      <div className="flex gap-2">
        <button
          type="button"
          onClick={save}
          disabled={!id.trim() || !model.trim() || !provider}
          data-testid="duckling-save"
          className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-40"
        >
          Save
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="rounded border border-hairline px-2 py-1 text-sm"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}
