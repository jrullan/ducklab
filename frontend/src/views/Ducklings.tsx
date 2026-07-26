import type { Duckling } from "../api/client";
import { StatusChip } from "../components/StatusChip";
import { DuckAvatar } from "../components/DuckAvatar";
import { money } from "../lib/format";
import { EmptyState } from "../components/EmptyState";

/** The roster of models, with the dialect each one actually speaks. */
export function Ducklings({ ducklings }: { ducklings: readonly Duckling[] }) {
  if (ducklings.length === 0) {
    return <EmptyState message="No ducklings configured. Add one in config.toml." />;
  }
  const order = ducklings.map((d) => d.id);
  return (
    <div className="grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-3" data-testid="ducklings">
      {ducklings.map((d) => (
        <div key={d.id} className="rounded-card border border-hairline p-3" data-testid="duckling-card">
          <header className="flex items-center gap-2">
            <DuckAvatar id={d.id} roster={order} />
            <span className="text-md">{d.id}</span>
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
          </dl>
          {(d.cost?.output_per_mtok ?? 0) === 0 && (
            <div className="mt-2"><StatusChip role="good" label="local — no USD cost" /></div>
          )}
        </div>
      ))}
    </div>
  );
}
