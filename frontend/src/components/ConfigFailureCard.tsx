import type { ConfigFinding, Duckling, EngineClient } from "../api/client";
import { ChatAbout } from "./ChatAbout";

/** A failed run can hand its configuration evidence directly to the consultant. */
export function ConfigFailureCard({
  client, projectId, finding, ducklings,
}: {
  client: EngineClient;
  projectId: string;
  finding: ConfigFinding;
  ducklings: readonly Duckling[];
}) {
  const initialMessage = `A run failed with this configuration finding. Please explain the priority and safe amendment: ${finding.key} → ${finding.proposed}. Reason: ${finding.reason}`;
  return <section className="m-2 rounded-card border border-warning p-3" data-testid="config-failure-card">
    <h2 className="text-sm font-medium text-ink">configuration may have caused this failure</h2>
    <p className="mt-1 text-sm text-ink-secondary"><code>{finding.key}</code> needs attention: {finding.reason}</p>
    <div className="mt-2"><ChatAbout client={client} projectId={projectId} aboutKind="ducklab" aboutId="configuration" ducklings={ducklings} label="Ask the configuration consultant" initialMessage={initialMessage} /></div>
  </section>;
}
