import { useState } from "react";
import type { ConfigFinding, EngineClient } from "../api/client";

/** Turn the doctor's machine-shaped values into words a person can act on. */
export function describeConfigValue(key: string, value: unknown, side: "old" | "new"): string {
  const rendered = typeof value === "string" ? value : value == null ? "" : String(value);
  const lower = `${key} ${rendered}`.toLowerCase();
  if (/(github|\bgh\b)/.test(lower) && /false/.test(lower)) {
    return side === "old" ? "github integration is configured but nothing uses it" : "github integration is turned off";
  }
  if (!rendered) return side === "old" ? "not set" : "no change";
  if (/^(false|off|disabled|none|null)$/i.test(rendered.trim())) return "disabled";
  if (/^(true|on|enabled)$/i.test(rendered.trim())) return "enabled";
  return rendered;
}

/** A standing finding is quiet unless the conversation makes it relevant or it is new. */
export function shouldShowConfigAmendment(options: { touchesConfiguration: boolean; isNew: boolean; dismissed: boolean }): boolean {
  return !options.dismissed && (options.touchesConfiguration || options.isNew);
}

/** A consultant's amendment remains inert data until this explicit human action. */
export function ConfigAmendmentCard({
  client, projectId, finding, old = "", why, onDismiss,
}: {
  client: EngineClient;
  projectId: string;
  finding: ConfigFinding;
  old?: string;
  /** Scribe-authored explanation, never executable configuration. */
  why?: string;
  onDismiss?: () => void;
}) {
  const [state, setState] = useState<"idle" | "applying" | "applied" | "error">("idle");
  const apply = () => {
    setState("applying");
    void client.projectUpdate(projectId, { [finding.key]: finding.proposed }, "consultant_proposal")
      .then(() => setState("applied"))
      .catch(() => setState("error"));
  };
  return <section className="m-2 rounded-card border border-warning p-3" data-testid="config-amendment-card">
    <div className="flex items-start justify-between gap-2">
      <h2 className="text-sm font-medium text-ink">configuration amendment</h2>
      {onDismiss && <button type="button" className="text-xs text-ink-muted underline" data-testid="config-amendment-dismiss" onClick={onDismiss}>dismiss for this session</button>}
    </div>
    <p className="mt-2 text-sm text-ink-secondary">{/(github|\bgh\b)/i.test(finding.key) && /false/i.test(String(old)) && /false/i.test(String(finding.proposed))
      ? "github integration is configured but nothing uses it — the amendment turns it off"
      : `${describeConfigValue(finding.key, old, "old")} — the amendment ${describeConfigValue(finding.key, finding.proposed, "new").toLowerCase()}`}</p>
    <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-sm">
      <dt className="text-ink-muted">key</dt><dd data-testid="config-amendment-key">{finding.key}</dd>
      <dt className="text-ink-muted">old</dt><dd data-testid="config-amendment-old">{describeConfigValue(finding.key, old, "old")}</dd>
      <dt className="text-ink-muted">new</dt><dd data-testid="config-amendment-new">{describeConfigValue(finding.key, finding.proposed, "new")}</dd>
      <dt className="text-ink-muted">why</dt><dd data-testid="config-amendment-why">{why || finding.reason}</dd>
    </dl>
    {(old || finding.proposed) && <details className="mt-2 text-xs text-ink-muted" data-testid="config-amendment-raw">
      <summary className="cursor-pointer">technical values</summary>
      <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-2"><dt>old</dt><dd>{String(old || "not set")}</dd><dt>new</dt><dd>{String(finding.proposed)}</dd></dl>
    </details>}
    <button type="button" data-testid="config-amendment-apply" disabled={state === "applying" || state === "applied"} onClick={apply} className="mt-3 rounded border border-hairline px-2 py-1 text-sm">
      {state === "applying" ? "Applying…" : state === "applied" ? "Applied" : "Apply amendment"}
    </button>
    {state === "error" && <p role="alert" className="mt-1 text-xs text-critical">Could not apply amendment.</p>}
  </section>;
}
