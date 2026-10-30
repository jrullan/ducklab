import { useCallback, useEffect, useState } from "react";
import type { AppStatus, EngineClient } from "../api/client";
import { openExternal } from "../lib/attention";
import { useAutoOpen } from "./useAutoOpen";

/** The live environment a paused run's manual checks ask for (B-552).
 *
 * "Verify in the live environment" used to offer checkboxes and nothing to
 * verify with, and the project's own Launch serves the registered tree —
 * main, without the change awaiting decision. This launches the run's
 * worktree instead, and says plainly which tree is running. */
const POLL_MS = 2000;

export function RunLiveApp({ client, runId, projectId, worktreePath }: {
  client: EngineClient;
  runId: string;
  projectId: string;
  worktreePath?: string;
}) {
  const [status, setStatus] = useState<AppStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [statusError, setStatusError] = useState("");

  const refresh = useCallback(() => {
    if (!projectId || typeof client.appStatus !== "function") return;
    client
      .appStatus(projectId)
      .then((next) => {
        setStatus(next);
        setStatusError("");
      })
      .catch((e) => setStatusError(e instanceof Error ? e.message : String(e)));
  }, [client, projectId]);
  // The app lives on its own: it can crash after launch, print its first
  // output a moment later, or be stopped from Projects or the CLI. A status
  // read once would keep saying "running" over all of that.
  useEffect(() => {
    refresh();
    const timer = setInterval(refresh, POLL_MS);
    return () => clearInterval(timer);
  }, [refresh]);

  const isOurs = useCallback((s: AppStatus) => s.run_id === runId, [runId]);
  const armAutoOpen = useAutoOpen(status, refresh, isOurs);

  const act = (call: () => Promise<unknown>) => {
    setBusy(true);
    setError("");
    call()
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => {
        setBusy(false);
        refresh();
      });
  };

  if (!status) {
    return statusError ? (
      <p className="mt-2 text-xs text-serious" data-testid="live-app-status-error">
        Cannot read the app's state: {statusError}
      </p>
    ) : null;
  }
  const ours = status.run_id === runId;
  const where = worktreePath ? <code className="break-all font-mono text-xs">{worktreePath}</code> : "the run's worktree";

  if (!worktreePath) {
    return (
      <p className="mt-2 text-xs text-ink-muted" data-testid="live-app-in-place">
        This run works in the project tree: launch the app from Projects to see its change.
      </p>
    );
  }
  if (!status.configured) {
    return (
      <p className="mt-2 text-xs text-ink-muted" data-testid="live-app-unconfigured">
        Ducklab cannot launch this project: it has no run command (set one in Projects → app). The change is in {where}.
      </p>
    );
  }
  return (
    <div className="mt-2 space-y-1 text-xs" data-testid="live-app">
      {status.running && ours ? (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-ink">This run's app is running{status.pid ? ` (pid ${status.pid})` : ""}.</span>
          {status.url ? (
            // A button through the shell: the desktop webview swallows
            // target=_blank, so a plain link opened nothing (B-553).
            <button type="button" onClick={() => openExternal(status.url!)} className="underline" data-testid="live-app-url" title={status.url_source === "output" ? "the address the app printed" : "run.url"}>
              {status.url}
            </button>
          ) : (
            <span className="text-ink-muted" data-testid="live-app-no-url">
              no address yet — if the app never prints one, set run.url in Projects
            </span>
          )}
          <button type="button" disabled={busy} onClick={() => act(() => client.appStop(projectId))} className="rounded border border-hairline px-2 py-0.5 disabled:opacity-50" data-testid="live-app-stop">
            Stop
          </button>
        </div>
      ) : status.running ? (
        <div className="flex flex-wrap items-center gap-2" data-testid="live-app-occupied">
          <span className="text-ink-secondary">
            {status.run_id ? `Run ${status.run_id}'s app` : "The project's app (main)"} is running — not this change. Stop it to launch this run's.
          </span>
          <button type="button" disabled={busy} onClick={() => act(() => client.appStop(projectId))} className="rounded border border-hairline px-2 py-0.5 disabled:opacity-50" data-testid="live-app-stop-other">
            Stop it
          </button>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <button type="button" disabled={busy} onClick={() => act(() => client.runAppStart(runId).then(armAutoOpen))} className="rounded border border-hairline px-2 py-0.5 disabled:opacity-50" data-testid="live-app-launch">
            {busy ? "Launching…" : "Launch this run's app"}
          </button>
          <code className="min-w-0 truncate font-mono text-ink-muted">{status.command}</code>
        </div>
      )}
      <p className="text-ink-muted">
        Runs {where} — the change awaiting your decision, not main. Accept or reject stops it; files the app writes there are not part of the change, and Accept names them until they are gone.
      </p>
      {ours && !status.running && status.exit_error && (
        <p className="text-serious" data-testid="live-app-exit">The app exited: {status.exit_error}</p>
      )}
      {ours && status.log_tail && (
        <details>
          <summary className="cursor-pointer text-ink-muted">app output</summary>
          <pre className="max-h-40 overflow-auto whitespace-pre-wrap font-mono text-ink-secondary">{status.log_tail}</pre>
        </details>
      )}
      {error && <p className="text-serious" data-testid="live-app-error">{error}</p>}
      {statusError && (
        <p className="text-serious" data-testid="live-app-status-error">
          Cannot read the app's state (showing the last one known): {statusError}
        </p>
      )}
    </div>
  );
}
