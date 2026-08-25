import { useState } from "react";
import type { ConfigFinding, EngineClient } from "../api/client";

/** A consultant's amendment remains inert data until this explicit human action. */
export function ConfigAmendmentCard({
  client, projectId, finding, old = "", why,
}: {
  client: EngineClient;
  projectId: string;
  finding: ConfigFinding;
  old?: string;
  /** Scribe-authored explanation, never executable configuration. */
  why?: string;
}) {
  const [state, setState] = useState<"idle" | "applying" | "applied" | "error">("idle");
  const apply = () => {
    setState("applying");
    void client.projectUpdate(projectId, { [finding.key]: finding.proposed }, "consultant_proposal")
      .then(() => setState("applied"))
      .catch(() => setState("error"));
  };
  return <section className="m-2 rounded-card border border-warning p-3" data-testid="config-amendment-card">
    <h2 className="text-sm font-medium text-ink">configuration amendment</h2>
    <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-sm">
      <dt className="text-ink-muted">key</dt><dd data-testid="config-amendment-key">{finding.key}</dd>
      <dt className="text-ink-muted">old</dt><dd data-testid="config-amendment-old">{old || "not set"}</dd>
      <dt className="text-ink-muted">new</dt><dd data-testid="config-amendment-new">{finding.proposed}</dd>
      <dt className="text-ink-muted">why</dt><dd data-testid="config-amendment-why">{why || finding.reason}</dd>
    </dl>
    <button type="button" data-testid="config-amendment-apply" disabled={state === "applying" || state === "applied"} onClick={apply} className="mt-3 rounded border border-hairline px-2 py-1 text-sm">
      {state === "applying" ? "Applying…" : state === "applied" ? "Applied" : "Apply amendment"}
    </button>
    {state === "error" && <p role="alert" className="mt-1 text-xs text-critical">Could not apply amendment.</p>}
  </section>;
}
