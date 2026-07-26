import { useEffect, useState } from "react";
import { EngineClient } from "../api/client";
import { EventSubscriber } from "../api/events";
import { useRuns, pendingForHuman } from "../store/runs";
import { StatusChip } from "../components/StatusChip";
import { EmptyState } from "../components/EmptyState";
import { runStatusRole, verdictLabel, verdictStatus, type Verdict } from "../lib/colors";
import { waitingFor } from "../lib/format";

/** Engine connection details, injected by the Wails host at runtime. */
declare global {
  interface Window {
    ducklab?: { baseUrl: string; token: string };
  }
}

export function App() {
  const runs = useRuns((s) => s.runs);
  const connection = useRuns((s) => s.connection);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const cfg = window.ducklab;
    if (!cfg) {
      setError("no engine connection details were provided by the host");
      return;
    }
    const client = new EngineClient({ baseUrl: cfg.baseUrl, token: cfg.token, version: "0.3.0" });
    const store = useRuns.getState();

    const refresh = () => client.runs().then(store.setRuns).catch((e) => setError(String(e)));
    refresh();

    const sub = new EventSubscriber({
      baseUrl: cfg.baseUrl,
      token: cfg.token,
      onEvent: (e) => useRuns.getState().applyEvent(e),
      onState: (s) => useRuns.getState().setConnection(s),
      // An overflow means we fell behind: refetch rather than guess.
      onOverflow: () => {
        useRuns.getState().markOverflow();
        refresh();
        useRuns.getState().clearResync();
      },
    });
    sub.start();
    return () => sub.stop();
  }, []);

  const list = Object.values(runs);
  const waiting = pendingForHuman(runs);
  // A dropped connection dims the last known state; it never blanks it.
  const dimmed = connection === "reconnecting" || connection === "closed";

  return (
    <div className="min-h-full bg-page text-ink">
      <header className="flex items-center justify-between border-b border-hairline px-4 py-3">
        <span className="text-md">🦆 ducklab</span>
        <StatusChip
          role={connection === "open" ? "good" : connection === "reconnecting" ? "warning" : "critical"}
          label={connection === "open" ? "engine" : connection}
        />
      </header>

      <main className={dimmed ? "opacity-60 transition-opacity" : ""}>
        {error && <div className="m-4 rounded border border-hairline p-3 text-critical">{error}</div>}

        {waiting.length > 0 && (
          <section className="border-b border-hairline p-4" data-testid="human-gate-inbox">
            <h2 className="text-sm text-ink-muted">waiting for you</h2>
            <ul>
              {waiting.map((r) => (
                <li key={r.id} className="flex gap-3 py-1">
                  <StatusChip role="serious" label={r.pending_kind ?? "waiting"} />
                  <span className="text-ink-secondary">{r.task_id}</span>
                  <span className="text-ink-muted">{waitingFor(r.pending_since ?? "")}</span>
                </li>
              ))}
            </ul>
          </section>
        )}

        {list.length === 0 ? (
          <EmptyState message="No runs yet. Pick a task and press Run." />
        ) : (
          <ul className="p-4">
            {list.map((r) => (
              <li key={r.id} className="flex items-center gap-3 py-1" data-testid="run-row">
                <StatusChip role={runStatusRole(r.status)} label={r.status} />
                <span className="text-ink-secondary">{r.mode}</span>
                <span className="text-ink-secondary">{r.task_id}</span>
                <StatusChip role={verdictStatus(r.verdict as Verdict)} label={verdictLabel(r.verdict as Verdict)} />
              </li>
            ))}
          </ul>
        )}
      </main>
    </div>
  );
}
