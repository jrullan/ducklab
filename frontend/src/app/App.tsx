import { useEffect, useState } from "react";
import { EngineClient, type Duckling, type Project } from "../api/client";
import { EventSubscriber, type DucklabEvent } from "../api/events";
import { DeltaBatcher, mergeDeltas } from "../api/batcher";
import { useRuns, pendingForHuman } from "../store/runs";
import { StatusChip } from "../components/StatusChip";
import { Overview } from "../views/Overview";
import { RunView } from "../views/RunView";
import { Board } from "../views/Board";
import { Cycle } from "../views/Cycle";
import { Ducklings } from "../views/Ducklings";
import { Settings } from "../views/Settings";
import { parseRoute, routeHref, type Route } from "./routes";
import { loadTheme, type Theme } from "./theme";

/** Must match internal/build.Version: the engine rejects a client whose
 * major version differs. */
const VERSION = "0.4.0";

declare global {
  interface Window {
    ducklab?: { baseUrl: string; token: string };
  }
}

const NAV: { route: Route; label: string }[] = [
  { route: { name: "overview" }, label: "Overview" },
  { route: { name: "runs" }, label: "Runs" },
  { route: { name: "cycle" }, label: "Cycle" },
  { route: { name: "board" }, label: "Board" },
  { route: { name: "ducklings" }, label: "Ducklings" },
  { route: { name: "settings" }, label: "Settings" },
];

export function App() {
  const [route, setRoute] = useState<Route>(() => parseRoute(location.hash));
  const [theme, setTheme] = useState<Theme>(() => loadTheme());
  const [ducklings, setDucklings] = useState<Duckling[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  // The chosen project survives a reload: a view that silently reset to the
  // first project every refresh would show someone else's cycle.
  const [projectId, setProjectId] = useState<string>(
    () => localStorage.getItem("ducklab.project") ?? "",
  );
  const [engineVersion, setEngineVersion] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [client, setClient] = useState<EngineClient | null>(null);

  const connection = useRuns((s) => s.connection);
  const runs = useRuns((s) => s.runs);

  useEffect(() => {
    const onHash = () => setRoute(parseRoute(location.hash));
    addEventListener("hashchange", onHash);
    return () => removeEventListener("hashchange", onHash);
  }, []);

  useEffect(() => {
    const cfg = window.ducklab;
    if (!cfg) {
      setError("no engine connection details were provided by the host");
      return;
    }
    const c = new EngineClient({ baseUrl: cfg.baseUrl, token: cfg.token, version: VERSION });
    setClient(c);

    const refresh = () => {
      c.runs().then(useRuns.getState().setRuns).catch((e) => setError(String(e)));
      c.ducklings().then(setDucklings).catch(() => {});
      c.projects()
        .then((ps) => {
          setProjects(ps);
          // Only fall back to the first project when the stored one is gone,
          // not on every load.
          setProjectId((cur) => (ps.some((p) => p.id === cur) ? cur : (ps[0]?.id ?? "")));
        })
        .catch(() => {});
      c.health().then((h) => setEngineVersion(h.version)).catch(() => {});
    };
    refresh();

    // Streamed text is coalesced per animation frame; persisted events go
    // straight through, since delaying them would make the UI lag the
    // engine's own record.
    const batcher = new DeltaBatcher((batch) =>
      useRuns.getState().applyDeltaBatch(mergeDeltas(batch)),
    );

    const sub = new EventSubscriber({
      baseUrl: cfg.baseUrl,
      token: cfg.token,
      onEvent: (e) => {
        if (batcher.push(e)) return;
        useRuns.getState().applyEvent(e);
      },
      onState: (s) => useRuns.getState().setConnection(s),
      // Overflow means we fell behind and the engine dropped us. Refetching
      // the run LIST is not enough: the conversation lives in the run detail,
      // so without this the transcript stays permanently truncated at the
      // point we fell behind.
      onOverflow: () => {
        const store = useRuns.getState();
        store.markOverflow();
        refresh();
        const current = parseRoute(location.hash);
        if (current.name === "run") {
          c.run(current.id)
            .then((d) => store.resyncRun(d.run, d.events as DucklabEvent[]))
            .catch(() => {})
            .finally(() => useRuns.getState().clearResync());
        } else {
          store.clearResync();
        }
      },
    });
    sub.start();
    return () => {
      sub.stop();
      batcher.drain();
    };
  }, []);

  // A dropped connection dims the last known state; it never blanks it (AC-30).
  const degraded = connection === "reconnecting" || connection === "closed";
  const waitingCount = pendingForHuman(runs).length;

  return (
    <div className="flex min-h-full flex-col bg-page text-ink">
      <header className="flex items-center gap-4 border-b border-hairline px-4 py-2">
        <span className="text-md">🦆 ducklab</span>
        <nav className="flex gap-3">
          {NAV.map((n) => (
            <a
              key={n.label}
              href={routeHref(n.route)}
              data-testid={`nav-${n.route.name}`}
              className={route.name === n.route.name ? "text-ink" : "text-ink-muted"}
            >
              {n.label}
              {n.route.name === "runs" && waitingCount > 0 && (
                <span className="ml-1 text-serious" data-testid="nav-badge">{waitingCount}</span>
              )}
            </a>
          ))}
        </nav>
        {projects.length > 0 && (
          <select
            data-testid="project-select"
            className="ml-auto bg-page text-ink border border-hairline rounded px-2 py-1 text-sm"
            value={projectId}
            onChange={(e) => {
              setProjectId(e.target.value);
              localStorage.setItem("ducklab.project", e.target.value);
            }}
          >
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name || p.id}
              </option>
            ))}
          </select>
        )}
      </header>

      <main className={degraded ? "flex-1 opacity-60 transition-opacity" : "flex-1"} data-degraded={String(degraded)}>
        {error && <p className="m-4 text-critical" data-testid="app-error">{error}</p>}

        {route.name === "overview" && <Overview spentToday={0} budget={2} />}
        {route.name === "runs" && <Overview spentToday={0} budget={2} />}
        {route.name === "run" && client && <RunView runId={route.id} client={client} />}
        {route.name === "cycle" &&
          (client && projectId ? (
            <div className="p-4">
              <Cycle client={client} projectId={projectId} />
            </div>
          ) : (
            <p className="m-4 text-ink-muted" data-testid="cycle-no-project">
              No project registered yet.
            </p>
          ))}
        {route.name === "board" &&
          (client && projectId ? (
            <div className="p-4">
              <Board client={client} projectId={projectId} />
            </div>
          ) : (
            <p className="m-4 text-ink-muted">No project registered yet.</p>
          ))}
        {route.name === "ducklings" && <Ducklings ducklings={ducklings} />}
        {route.name === "settings" && (
          <Settings theme={theme} onTheme={setTheme} engineVersion={engineVersion} connection={connection} />
        )}
      </main>

      <footer className="flex items-center gap-3 border-t border-hairline px-4 py-1 text-sm">
        {/* "connecting" is the normal first second of the app's life, not a
            failure. Painting it critical told every user that something was
            broken before anything had a chance to go wrong. Only "closed" —
            we gave up — is critical. */}
        <StatusChip
          role={
            connection === "open"
              ? "good"
              : connection === "closed"
                ? "critical"
                : "warning"
          }
          label={connection === "open" ? "engine" : connection}
        />
        {waitingCount > 0 && <StatusChip role="serious" label={`${waitingCount} waiting for you`} />}
      </footer>
    </div>
  );
}
