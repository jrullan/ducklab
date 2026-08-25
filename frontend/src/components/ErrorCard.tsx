import type { ApiError } from "../api/client";

/** Presents a request failure as a readable sentence, with diagnostics opt-in. */
export function ErrorCard({ error, testId = "error-card" }: { error: unknown; testId?: string }) {
  const api = error as Partial<ApiError>;
  const raw = error instanceof Error ? error.message : String(error);
  const sentence = raw.replace(/^ApiError:\s*/, "");
  const hasDetails = api.name === "ApiError" && (api.method || api.path || api.status !== undefined);

  return (
    <section className="rounded-card border border-warning bg-surface2 p-3" data-testid={testId} role="alert">
      <p className="text-sm text-ink">{sentence}</p>
      {hasDetails && (
        <details className="mt-2 text-xs text-ink-muted">
          <summary className="cursor-pointer">details</summary>
          <dl className="mt-1 space-y-0.5 font-mono">
            {api.method && api.path && <div><dt className="inline">request: </dt><dd className="inline">{api.method} {api.path}</dd></div>}
            {api.status !== undefined && <div><dt className="inline">status: </dt><dd className="inline">{api.status || "connection"}</dd></div>}
          </dl>
        </details>
      )}
    </section>
  );
}
