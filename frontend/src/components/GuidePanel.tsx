import { useEffect, useState } from "react";
import type { AutopilotState, EngineClient } from "../api/client";

/** Autopilot is a shell control; the retired GuideRail has no mount point. */
export function AutopilotControl({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [ap, setAp] = useState<AutopilotState | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!projectId) return;
    client.autopilot(projectId).then(setAp).catch(() => setAp(null));
  }, [client, projectId]);
  useEffect(() => {
    if (!projectId || !ap?.on) return;
    const t = setInterval(() => client.autopilot(projectId).then(setAp).catch(() => {}), 4000);
    return () => clearInterval(t);
  }, [client, projectId, ap?.on]);
  const toggle = () => {
    setBusy(true);
    client.autopilotSet(projectId, !(ap?.on ?? false)).then(setAp).catch(() => {}).finally(() => setBusy(false));
  };
  return (
    <section data-testid="sidebar-autopilot" className="border-t border-hairline pt-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs text-ink-muted">autopilot</span>
        <button type="button" data-testid="autopilot-toggle" disabled={busy} onClick={toggle} className={`rounded border px-2 py-0.5 text-xs ${ap?.on ? "border-good text-good" : "border-hairline text-ink-muted"}`}>
          {ap?.on ? "on — stop" : "start"}
        </button>
      </div>
      {ap?.on && <p className="mt-1 text-xs text-ink-muted" data-testid="autopilot-status">{ap.started}/{ap.max_tasks} tasks started · {ap.last_action || "…"}</p>}
      {!ap?.on && ap?.stopped_reason && <p className="mt-1 text-xs text-serious" data-testid="autopilot-stopped">{ap.stopped_reason}</p>}
    </section>
  );
}
