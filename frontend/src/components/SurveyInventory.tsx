import type { Run } from "../api/client";

export type SurveyInventoryItem = {
  name?: string;
  kind?: string;
  "evidence-path"?: string;
  evidence_path?: string;
};

function unaccounted(run?: Run | null): SurveyInventoryItem[] {
  if (run?.stage !== "intake") return [];
  const items = run.pending_data?.unaccounted;
  return Array.isArray(items) ? items as SurveyInventoryItem[] : [];
}

/** Recorded survey coverage only; this never derives coverage from the tree. */
export function SurveyCoverageLine({ run, testId }: { run?: Run | null; testId?: string }) {
  const items = unaccounted(run);
  if (items.length === 0) return null;
  return (
    <p data-testid={testId} className="mb-2 text-xs text-warn">
      ⚠ {items.length} surface areas unaccounted: {items.map((item) => item.name).filter(Boolean).join(", ")}
    </p>
  );
}

export function SurveyInventory({ items }: { items: SurveyInventoryItem[] }) {
  if (items.length === 0) return null;
  return (
    <details data-testid="survey-inventory" className="m-2 rounded-card border border-hairline p-3">
      <summary className="cursor-pointer text-sm text-ink-secondary">Recorded surface inventory · {items.length} items</summary>
      <ul className="mt-2 space-y-1 text-sm">
        {items.map((item, index) => (
          <li key={`${item.name ?? "surface"}-${index}`}>
            <span className="text-ink">{item.name}</span>
            {item.kind && <span className="text-ink-muted"> · {item.kind}</span>}
            {(item["evidence-path"] ?? item.evidence_path) && (
              <span className="font-mono text-xs text-ink-muted"> · {item["evidence-path"] ?? item.evidence_path}</span>
            )}
          </li>
        ))}
      </ul>
    </details>
  );
}
