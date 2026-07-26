import { useEffect, useState } from "react";
import { EngineClient, type Duckling } from "../api/client";
import { EventSubscriber } from "../api/events";
import { useRuns, pendingForHuman } from "../store/runs";
import { StatusChip } from "../components/StatusChip";
import { Overview } from "../views/Overview";
import { RunView } from "../views/RunView";
import { Ducklings } from "../views/Ducklings";
import { Settings } from "../views/Settings";
import { parseRoute, routeHref, type Route } from "./routes";
import { loadTheme, type Theme } from "./theme";

declare global {
  interface Window {
    ducklab?: { baseUrl: string; token: string };
  }
}

const NAV: { route: Route; label: string }[] = [
  { route: { name: "overview" }, label: "Overview" },
  { route: { name: "runs" }, label: "Runs" },
  { route: { name: "ducklings" }, label: "Ducklings" },
  { route: { name: "settings" }, label: "Settings" },
];

export function App() {
  const [route, setRoute] = useState<Route>(() => parseRoute(location.hash));
  const [theme, setTheme] = useState<Theme>(() => loadTheme());
  const [ducklings, setDucklings] = useState<Duckling[]>([]);
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
    const c = new EngineClient({ baseUrl: cfg.baseUrl, token: cfg.token, version: "0.3.0" });
    setClient(c);

    const refresh = () => {
      c.runs().then(useRuns.getState().setRuns).catch((e) => setError(String(e)));
      c.ducklings().then(setDucklings).catch(() => {});
      c.health().then((h) => setEngineVersion(h.version)).catch(() => {});
    };
    refresh();

    const sub = new EventSubscriber({
      baseUrl: cfg.baseUrl,
      token: cfg.token,
      onEvent: (e) => useRuns.getState().applyEvent(e),
      onState: (s) => useRuns.getState().setConnection(s),
      // Overflow means we fell behind: refetch the snapshot rather than guess.
      onOverflow: () => {
        useRuns.getState().markOverflow();
        refresh();
        useRuns.getState().clearResync();
      },
    });
    sub.start();
    return () => sub.stop();
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
      </header>

      <main className={degraded ? "flex-1 opacity-60 transition-opacity" : "flex-1"} data-degraded={String(degraded)}>
        {error && <p className="m-4 text-critical" data-testid="app-error">{error}</p>}

        {route.name === "overview" && <Overview spentToday={0} budget={2} />}
        {route.name === "runs" && <Overview spentToday={0} budget={2} />}
        {route.name === "run" && client && <RunView runId={route.id} client={client} />}
        {route.name === "ducklings" && <Ducklings ducklings={ducklings} />}
        {route.name === "settings" && (
          <Settings theme={theme} onTheme={setTheme} engineVersion={engineVersion} connection={connection} />
        )}
      </main>

      <footer className="flex items-center gap-3 border-t border-hairline px-4 py-1 text-sm">
        <StatusChip
          role={connection === "open" ? "good" : connection === "reconnecting" ? "warning" : "critical"}
          label={connection === "open" ? "engine" : connection}
        />
        {waitingCount > 0 && <StatusChip role="serious" label={`${waitingCount} waiting for you`} />}
      </footer>
    </div>
  );
}
