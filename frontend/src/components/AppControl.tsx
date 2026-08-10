import { useEffect, useState } from "react";
import type { AppStatus, EngineClient } from "../api/client";
import { StatusChip } from "./StatusChip";
import { openExternal } from "../lib/attention";

/** The app's Launch/Stop, in the shell header beside the project picker.
 *
 * It lived at the bottom of Now, behind a scroll — for the one control whose
 * whole point is "try the thing", on the one surface that is always on
 * screen. Compact up here; the details (environment checklist, crash exit
 * and log) unfold below on demand. Polls while mounted so the chip converges
 * to what the app actually says. */
export function AppControl({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [app, setApp] = useState<AppStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!projectId) return;
    let cancelled = false;
    const fetchStatus = () =>
      client.appStatus(projectId).then((a) => !cancelled && setApp(a)).catch(() => !cancelled && setApp(null));
    void fetchStatus();
    const t = setInterval(fetchStatus, 7000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, [client, projectId]);

  if (!app?.configured) return null;

  const act = () => {
    setBusy(true);
    setError(null);
    const action = app.running ? client.appStop(projectId) : client.appStart(projectId).then(() => {});
    void action
      .then(() => client.appStatus(projectId))
      .then(setApp)
      .catch((e) => {
        setError(e instanceof Error ? e.message : String(e));
        setOpen(true);
      })
      .finally(() => setBusy(false));
  };

  const hasDetail = !!(app.requires || app.exit_error || error);
  return (
    <div className="relative flex items-center gap-2" data-testid="app-control">
      <button
        type="button"
        onClick={() => hasDetail && setOpen((v) => !v)}
        title={hasDetail ? "details" : undefined}
        className={hasDetail ? "cursor-pointer" : "cursor-default"}
      >
        {app.running ? (
          <StatusChip
            role={app.health === "unhealthy" ? "warning" : "good"}
            label={app.health ? `app · ${app.health}` : "app · running"}
          />
        ) : app.exit_error ? (
          <StatusChip role="critical" label="app · crashed" />
        ) : (
          <StatusChip role="muted" label="app · stopped" />
        )}
      </button>
      {app.running && app.url && (
        <button
          type="button"
          data-testid="app-open"
          onClick={() => openExternal(app.url!)}
          className="text-sm text-ink underline"
        >
          open
        </button>
      )}
      <button
        type="button"
        data-testid={app.running ? "app-stop" : "app-launch"}
        onClick={act}
        disabled={busy}
        className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
      >
        {busy ? "…" : app.running ? "Stop" : "Launch"}
      </button>
      {open && hasDetail && (
        <div className="absolute right-0 top-full z-10 mt-1 w-96 rounded-card border border-hairline bg-page p-3 shadow" data-testid="app-detail">
          {!app.running && app.requires && (
            <div data-testid="app-requires">
              <p className="text-xs text-ink-muted">the environment must provide:</p>
              <ul className="ml-4 list-disc text-xs text-ink-secondary">
                {app.requires.split(";").map((r) => r.trim()).filter(Boolean).map((r, i) => (
                  <li key={i}>{r}</li>
                ))}
              </ul>
            </div>
          )}
          {app.exit_error && (
            <div className="mt-1">
              <p className="text-xs text-critical" data-testid="app-exit-error">exited: {app.exit_error}</p>
              {app.log_tail && (
                <pre className="mt-1 max-h-32 overflow-auto whitespace-pre-wrap font-mono text-xs text-ink-muted">{app.log_tail}</pre>
              )}
            </div>
          )}
          {error && <p className="mt-1 text-xs text-critical" data-testid="app-error">{error}</p>}
        </div>
      )}
    </div>
  );
}
