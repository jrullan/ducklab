import { useEffect, useRef, useState } from "react";
import { EngineClient, type Project } from "../api/client";
import { EventSubscriber, type DucklabEvent } from "../api/events";
import { DeltaBatcher, mergeDeltas } from "../api/batcher";
import { useRuns, pendingForHuman } from "../store/runs";
import { interruptions, deliver, setBadge } from "../lib/attention";
import type { Run } from "../api/client";
import { StatusChip } from "../components/StatusChip";
import { AppControl } from "../components/AppControl";
import { Now } from "../views/Now";
import { Bench } from "../views/Bench";
import { Runs } from "../views/Runs";
import { RunView } from "../views/RunView";
import { Board } from "../views/Board";
import { GuideRail } from "../components/GuidePanel";
import { Cycle } from "../views/Cycle";
import { Release } from "../views/Release";
import { Reports } from "../views/Reports";
import { Projects } from "../views/Projects";
import { Review } from "../views/Review";
import { Ducklings } from "../views/Ducklings";
import { Settings } from "../views/Settings";
import { parseRoute, routeHref, type Route } from "./routes";
import { loadTheme, type Theme } from "./theme";

/** Must match internal/build.Version: the engine rejects a client whose
 * major version differs. */
const VERSION = "0.4.0";

declare global {
  interface Window {
    ducklab?: {
      baseUrl: string;
      token: string;
      /** Wails binding name for the native folder chooser. Absent outside the
       * desktop. */
      chooseDirectory?: string;
      /** Wails binding names for the attention surface: OS notifications and
       * the window-title badge. Absent outside the desktop. */
      notify?: string;
      setBadge?: string;
      /** Wails binding name for engine supervision: stop the old engine,
       * start the installed one, hand back the new connection. */
      restartEngine?: string;
      reconnectEngine?: string;
      /** Wails binding name for opening a URL in the system browser — the
       * webview swallows target=_blank anchors. Absent outside the desktop. */
      openURL?: string;
    };
  }
}

// Three destinations instead of ten (docs/ux-evaluation.md §5.1). The old nav
// had one tab per engine resource — which is how a client grows when every new
// endpoint gets its own page — while the person has one workflow smeared
// across them. Now is the inbox; Work is the project's substance; Records is
// history and analysis, where nothing ever needs a decision. Configuration
// lives behind the gear: not a destination a solo dev visits daily.
type Zone = { label: string; testid: string; home: Route; members: Route["name"][] };
const ZONES: Zone[] = [
  { label: "Now", testid: "nav-now", home: { name: "now" }, members: ["now"] },
  {
    label: "Work", testid: "nav-work", home: { name: "board" },
    members: ["board", "cycle"],
  },
  {
    label: "Records", testid: "nav-records", home: { name: "runs" },
    members: ["runs", "run", "reports", "review", "release", "bench"],
  },
];
const CONFIG_MEMBERS: Route["name"][] = ["settings", "ducklings", "projects"];

// Within a zone, its rooms. Documents is the old Cycle: for a solo dev the
// lifecycle documents are work items, not a separate ceremony.
const SUBNAV: Record<string, { label: string; route: Route }[]> = {
  Work: [
    { label: "Documents", route: { name: "cycle" } },
    { label: "Tasks", route: { name: "board" } },
    { label: "Bugs", route: { name: "board", tab: "bugs" } },
  ],
  Records: [
    { label: "Runs", route: { name: "runs" } },
    { label: "Reports", route: { name: "reports" } },
    { label: "Reviews", route: { name: "review" } },
    { label: "Releases", route: { name: "release" } },
    { label: "Bench", route: { name: "bench" } },
  ],
  Config: [
    { label: "Settings", route: { name: "settings" } },
    { label: "Projects", route: { name: "projects" } },
  ],
};


/** Every view that needs a project says the same thing and points at the one
 * place that fixes it. Before this it said "No project registered yet." and
 * stopped — true, and a dead end. */
function NoProject() {
  return (
    <p className="m-4 text-ink-muted" data-testid="cycle-no-project">
      No project yet.{" "}
      <a href={routeHref({ name: "projects" })} className="text-ink underline">
        Create one
      </a>
      .
    </p>
  );
}

export function App() {
  const [route, setRoute] = useState<Route>(() => parseRoute(location.hash));
  const [theme, setTheme] = useState<Theme>(() => loadTheme());
  const [projects, setProjects] = useState<Project[]>([]);
  // The chosen project survives a reload: a view that silently reset to the
  // first project every refresh would show someone else's cycle.
  const [projectId, setProjectId] = useState<string>(
    () => localStorage.getItem("ducklab.project") ?? "",
  );
  const [engineVersion, setEngineVersion] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [client, setClient] = useState<EngineClient | null>(null);
  // The engine this page talks to. State, not a constant: a restart hands
  // back fresh connection details and everything below rebuilds against them.
  const [conn, setConn] = useState(() =>
    window.ducklab ? { baseUrl: window.ducklab.baseUrl, token: window.ducklab.token } : null,
  );
  // A response revealed the engine predates this app. The one action that
  // fixes it gets a button, not a sentence telling someone to open a terminal.
  const [stale, setStale] = useState<false | "older" | "restarted">(false);
  const [restarting, setRestarting] = useState(false);
  const [restartError, setRestartError] = useState<string | null>(null);

  // The stream and its handlers outlive any one project, so they read the
  // choice through a ref rather than capturing whichever was current when the
  // connection opened.
  const projectRef = useRef(projectId);
  useEffect(() => {
    projectRef.current = projectId;
  }, [projectId]);

  const connection = useRuns((s) => s.connection);
  const runs = useRuns((s) => s.runs);

  useEffect(() => {
    const onHash = () => setRoute(parseRoute(location.hash));
    addEventListener("hashchange", onHash);
    return () => removeEventListener("hashchange", onHash);
  }, []);

  // The shell injects window.ducklab via a script the webview runs around
  // page load — sometimes after this bundle has already started. Reading it
  // once turned that race into a permanent "no engine connection details"
  // that a relaunch usually won, which is exactly how it presented: red on
  // some starts, fine on the next. The injection is not an event anyone can
  // subscribe to, so the app waits for it briefly; only genuine absence — a
  // plain browser, a truly broken shell — earns the error.
  useEffect(() => {
    if (conn) return;
    let tries = 0;
    const iv = setInterval(() => {
      if (window.ducklab) {
        clearInterval(iv);
        setError(null);
        setConn({ baseUrl: window.ducklab.baseUrl, token: window.ducklab.token });
      } else if (++tries >= 100) {
        clearInterval(iv);
        setError("no engine connection details were provided by the host");
      }
    }, 100);
    return () => clearInterval(iv);
  }, [conn]);

  useEffect(() => {
    const cfg = conn;
    if (!cfg) {
      return;
    }
    const c = new EngineClient({
      baseUrl: cfg.baseUrl,
      token: cfg.token,
      version: VERSION,
      onStale: (kind) => setStale((cur) => cur || kind),
      onRecovered: () => setStale(false),
      reconnect: async () => {
        const fqn = window.ducklab?.reconnectEngine;
        const call = window.wails?.Call?.ByName;
        if (!fqn || !call) throw new Error("engine binding is stale; reconnect is only available in the desktop app");
        const fresh = (await call(fqn)) as { baseUrl: string; token: string };
        if (window.ducklab) {
          window.ducklab.baseUrl = fresh.baseUrl;
          window.ducklab.token = fresh.token;
        }
        setStale(false);
        setConn({ baseUrl: fresh.baseUrl, token: fresh.token });
        return fresh;
      },
    });
    setClient(c);

    const refresh = () => {
      // Scoped to the chosen project.
      //
      // Overview and Runs read this store, and it used to hold every run the
      // engine knew, from every project, fetched once at startup — so those
      // two views ignored the dropdown that every other view obeys, and went
      // stale the moment anything ran. The same defect existed in the CLI's
      // `run list`.
      c.runs(projectRef.current).then(useRuns.getState().setRuns).catch((e) => setError(String(e)));
      c.projects()
        .then((ps) => {
          setProjects(ps);
          // Prefer a project that is actually on disk. Falling back to a
          // missing one shows empty views that read as "nothing to do here".
          const live = ps.filter((p) => !p.missing);
          setProjectId((cur) =>
            ps.some((p) => p.id === cur && !p.missing) ? cur : (live[0]?.id ?? ps[0]?.id ?? ""),
          );
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
        // The stream carries every project's events. Without this filter a run
        // started elsewhere appeared in Overview and Runs — and since a
        // run_start now creates a record, it would arrive as a new row rather
        // than merely updating one.
        //
        // Events with no project_id are kept: heartbeats and token_delta carry
        // none, and dropping them would silence the connection indicator and
        // every streamed turn.
        if (e.project_id && projectRef.current && e.project_id !== projectRef.current) return;
        if (batcher.push(e)) return;
        useRuns.getState().applyEvent(e);
      },
      onState: (s) => useRuns.getState().setConnection(s),
      // A reconnect can miss a run_start that happened on the dead stream;
      // replace the run list from HTTP after the new stream is open.
      onReconnect: () => refresh(),
      staleAfterMs: 30_000,
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
    // Rebuilt whenever the connection changes — which is exactly once per
    // engine restart, when the shell hands back a fresh port and token.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conn]);

  // Two remedies, one shape: "restart" stops the old engine and starts the
  // installed binary; "reconnect" adopts an engine already running whose
  // token this window missed (it was restarted outside the app). Both hand
  // back fresh connection details and the client rebuilds in place.
  async function superviseEngine(action: "restart" | "reconnect") {
    const fqn =
      action === "restart" ? window.ducklab?.restartEngine : window.ducklab?.reconnectEngine;
    const call = window.wails?.Call?.ByName;
    if (!fqn || !call) {
      setRestartError("engine supervision is only available in the desktop app");
      return;
    }
    setRestarting(true);
    setRestartError(null);
    try {
      const fresh = (await call(fqn)) as { baseUrl: string; token: string };
      if (window.ducklab) {
        window.ducklab.baseUrl = fresh.baseUrl;
        window.ducklab.token = fresh.token;
      }
      setStale(false);
      setConn({ baseUrl: fresh.baseUrl, token: fresh.token });
    } catch (e) {
      setRestartError(e instanceof Error ? e.message : String(e));
    } finally {
      setRestarting(false);
    }
  }

  // Changing project reloads what the project-blind store holds. Without
  // this the switch changed four views and left two showing the old one's
  // work.
  useEffect(() => {
    if (!client || !projectId) return;
    let cancelled = false;
    client
      .runs(projectId)
      .then((rs) => !cancelled && useRuns.getState().setRuns(rs))
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [client, projectId]);

  // A dropped connection dims the last known state; it never blanks it (AC-30).
  const degraded = connection === "reconnecting" || connection === "closed";
  const streamBadge = connection === "open"
    ? "engine ✓"
    : `engine ✓ · stream ${connection}`;
  const waitingCount = pendingForHuman(runs).length;

  // The attention surface. The store sees every state change; this is the one
  // place that turns transitions into interruptions — an OS notification when
  // a run pauses for a human or fails, and the waiting count in the window
  // title so it survives the app sitting in another workspace. Deliberately
  // NOT per-view: attention is app state, and a person on any screen (or none)
  // is owed the same call.
  const prevRuns = useRef<Record<string, Run> | null>(null);
  useEffect(() => {
    for (const i of interruptions(prevRuns.current, runs)) {
      deliver(i);
    }
    prevRuns.current = runs;
  }, [runs]);
  useEffect(() => {
    setBadge(waitingCount);
  }, [waitingCount]);

  // A run that pauses while we watch arrives by stream, and the stream
  // carries only the transition — not the engine's next list, the verdict,
  // or the spend. The decision card renders its buttons from `next` alone,
  // so the person got a card with nothing to click until some other view
  // happened to fetch the full run. Hydrate it the moment it pauses.
  const hydrated = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!client) return;
    for (const r of pendingForHuman(runs)) {
      if (r.next || hydrated.current.has(r.id)) continue;
      hydrated.current.add(r.id);
      client
        .run(r.id)
        .then((d) => useRuns.getState().setRun(d.run))
        .catch(() => hydrated.current.delete(r.id));
    }
  }, [client, runs]);

  // h-screen with the scroll moved inside: the whole page used to scroll, so a
  // long run pushed the nav off the top of the window, and getting back to it
  // meant scrolling past everything the run had said.
  return (
    <div className="flex h-screen flex-col overflow-hidden bg-page text-ink">
      <header className="flex items-center gap-4 border-b border-hairline px-4 py-2">
        <span className="text-md">🦆 ducklab</span>
        <nav className="flex gap-3">
          {ZONES.map((z) => (
            <a
              key={z.label}
              href={routeHref(z.home)}
              data-testid={z.testid}
              className={z.members.includes(route.name) ? "text-ink" : "text-ink-muted"}
            >
              {z.label}
              {/* The waiting count on the inbox, which is where the waiting is. */}
              {z.label === "Now" && waitingCount > 0 && (
                <span className="ml-1 text-serious" data-testid="nav-badge">
                  ● {waitingCount}
                </span>
              )}
            </a>
          ))}
          <a
            href={routeHref({ name: "settings" })}
            data-testid="nav-config"
            title="Settings, ducklings and projects"
            className={CONFIG_MEMBERS.includes(route.name) ? "text-ink" : "text-ink-muted"}
          >
            ⚙
          </a>
        </nav>
        <span className="ml-auto" />
        {client && projectId && <AppControl client={client} projectId={projectId} />}
        {projects.length > 0 && (
          <select
            data-testid="project-select"
            className="bg-page text-ink border border-hairline rounded px-2 py-1 text-sm"
            value={projectId}
            onChange={(e) => {
              setProjectId(e.target.value);
              localStorage.setItem("ducklab.project", e.target.value);
            }}
          >
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {(p.name || p.id) + (p.missing ? " (missing)" : "")}
              </option>
            ))}
          </select>
        )}
      </header>

      {/* The zone's rooms, when it has more than one. A person inside Work or
          Records should see where they are and step sideways without a map. */}
      {(() => {
        const zone =
          ZONES.find((z) => z.members.includes(route.name) && z.members.length > 1)?.label ??
          (CONFIG_MEMBERS.includes(route.name) ? "Config" : "");
        const rooms = SUBNAV[zone];
        if (!rooms) return null;
        const activeRoom = (r: Route) =>
          r.name === route.name &&
          (r.name !== "board" ||
            ("tab" in r ? r.tab : undefined) === ("tab" in route ? route.tab : undefined));
        return (
          <div
            className="flex gap-3 border-b border-hairline px-4 py-1 text-sm"
            data-testid="subnav"
          >
            {rooms.map((r) => (
              <a
                key={r.label}
                href={routeHref(r.route)}
                data-testid={`subnav-${r.label.toLowerCase()}`}
                className={activeRoom(r.route) ? "text-ink" : "text-ink-muted"}
              >
                {r.label}
              </a>
            ))}
          </div>
        );
      })()}

      {/* The plumbing banner. A stale engine used to be a sentence buried in
          whichever view hit it first, telling the person to open a terminal;
          now the one action that fixes it is the button next to the words. */}
      {stale && (
        <div
          className="flex items-center gap-3 border-b border-hairline bg-surface2 px-4 py-2 text-sm"
          data-testid="stale-banner"
        >
          <span className="text-serious" role="status">
            {stale === "restarted"
              ? "The engine was restarted outside the app — this window's session died with it."
              : "The engine is older than this app — some features will fail until it restarts."}
          </span>
          {stale === "restarted" ? (
            <button
              type="button"
              onClick={() => void superviseEngine("reconnect")}
              disabled={restarting}
              data-testid="reconnect-engine"
              className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-50"
            >
              {restarting ? "Reconnecting…" : "Reconnect"}
            </button>
          ) : (
            <button
              type="button"
              onClick={() => void superviseEngine("restart")}
              disabled={restarting}
              data-testid="restart-engine"
              className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-50"
            >
              {restarting ? "Restarting…" : "Restart engine"}
            </button>
          )}
          {restartError && (
            <span className="text-xs text-critical" data-testid="restart-error">
              {restartError}
            </span>
          )}
        </div>
      )}

      <div className="flex min-h-0 flex-1">
        {/* The guide rail lives beside every view, not inside one: following
            a step changes the view, and a thread you can only see from one
            room is not a thread. */}
        {client && projectId && <GuideRail client={client} projectId={projectId} />}
        <main
          aria-readonly={stale ? "true" : undefined}
          data-testid={stale ? "stale-read-only" : undefined}
          className={
            "min-h-0 flex-1 overflow-y-auto" +
            (degraded ? " opacity-60 transition-opacity" : "") +
            (stale ? " pointer-events-none opacity-60" : "")
          }
          data-degraded={String(degraded)}
        >
        {error && <p className="m-4 text-critical" data-testid="app-error">{error}</p>}

        {route.name === "now" && client && projectId && (
          <Now client={client} projectId={projectId} />
        )}
        {route.name === "runs" && (
          <div className="p-4">
            <Runs runs={Object.values(runs)} />
          </div>
        )}
        {route.name === "run" && client && <RunView runId={route.id} client={client} />}
        {route.name === "cycle" &&
          (client && projectId ? (
            <div className="p-4">
              <Cycle client={client} projectId={projectId} stage={route.stage} />
            </div>
          ) : (
            <NoProject />
          ))}
        {route.name === "board" &&
          (client && projectId ? (
            <div className="p-4">
              <Board client={client} projectId={projectId} tab={route.tab} />
            </div>
          ) : (
            <NoProject />
          ))}
        {route.name === "review" &&
          (client && projectId ? (
            <div className="p-4">
              <Review client={client} projectId={projectId} />
            </div>
          ) : (
            <NoProject />
          ))}
        {route.name === "release" &&
          (client && projectId ? (
            <div className="p-4">
              <Release client={client} projectId={projectId} />
            </div>
          ) : (
            <NoProject />
          ))}
        {route.name === "bench" &&
          (client && projectId ? (
            <div className="p-4">
              <Bench client={client} />
            </div>
          ) : (
            <NoProject />
          ))}
        {route.name === "reports" &&
          (client && projectId ? (
            <div className="p-4">
              <Reports client={client} projectId={projectId} />
            </div>
          ) : (
            <NoProject />
          ))}
        {route.name === "projects" && client && (
          <div className="p-4">
            <Projects
              client={client}
              selected={projectId}
              onSelect={(id) => {
                setProjectId(id);
                localStorage.setItem("ducklab.project", id);
              }}
              onChanged={() => {
                client.projects().then(setProjects).catch(() => {});
              }}
            />
          </div>
        )}
        {route.name === "ducklings" && client && <Ducklings client={client} projectId={projectId} />}
        {route.name === "settings" && (
          <Settings
            projectId={projectId}
            theme={theme} onTheme={setTheme} engineVersion={engineVersion} connection={connection}
            client={client ?? undefined}
            onEngine={(a) => void superviseEngine(a)} engineBusy={restarting} engineError={restartError}
          />
        )}
      </main>
      </div>

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
          label={streamBadge}
        />
        {waitingCount > 0 && <StatusChip role="serious" label={`${waitingCount} waiting for you`} />}
      </footer>
    </div>
  );
}
