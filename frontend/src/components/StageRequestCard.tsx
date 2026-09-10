function visible(value: unknown): string | null {
  if (typeof value === "string") return value.trim() || null;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return value.length ? value.map(String).join(", ") : null;
  if (value && typeof value === "object") return JSON.stringify(value);
  return null;
}

export function StageRequestCard({ request }: { request?: Record<string, unknown> }) {
  if (!request) return null;
  const entries = Object.entries(request)
    .map(([key, value]) => [key, visible(value)] as const)
    .filter((entry): entry is readonly [string, string] => entry[1] !== null);
  if (entries.length === 0) return null;
  return (
    <section data-testid="stage-request-card" className="mt-3 rounded-card border border-hairline p-3">
      <div className="text-sm font-medium text-ink">Request</div>
      <dl className="mt-1 max-h-40 overflow-auto text-xs">
        {entries.map(([key, value]) => <div key={key} className="grid grid-cols-[7rem_1fr] gap-2 py-0.5">
          <dt className="text-ink-muted">{key.replaceAll("_", " ")}</dt>
          <dd className="whitespace-pre-wrap break-words text-ink">{value}</dd>
        </div>)}
      </dl>
    </section>
  );
}
