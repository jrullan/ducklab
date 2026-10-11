import { isImageRef, suggestScope } from "../lib/refroles";

/**
 * One toggle per attached document reference (B-520): "background" (the
 * default — context, never scope) or "scope" (it describes what to build;
 * requirements are derived from it and exclusions it contradicts can be
 * lifted). When the brief reads as if it targets a reference and none is
 * marked, a hint asks — it never flips the toggle itself.
 */
export function ReferenceRoles({
  paths,
  scope,
  onToggle,
  brief,
  testPrefix,
}: {
  paths: string[];
  scope: ReadonlySet<string>;
  onToggle: (path: string, scope: boolean) => void;
  brief: string;
  testPrefix: string;
}) {
  const docs = paths.filter((p) => !isImageRef(p));
  if (docs.length === 0) return null;
  const hint = suggestScope(brief, docs, scope);
  return (
    <div className="mt-1 space-y-1" data-testid={`${testPrefix}-roles`}>
      <p className="text-xs text-ink-muted">
        Each document is <span className="text-ink-secondary">background</span> (context; never adds requirements)
        unless you mark it <span className="text-ink-secondary">scope</span>: it describes what to build, so
        requirements come from it and exclusions it contradicts can be lifted.
      </p>
      <ul className="space-y-0.5">
        {docs.map((p) => (
          <li key={p} className="flex items-center gap-2 text-xs">
            <label className="flex items-center gap-1 text-ink-secondary">
              <input
                type="checkbox"
                data-testid={`${testPrefix}-scope`}
                data-path={p}
                checked={scope.has(p)}
                onChange={(e) => onToggle(p, e.target.checked)}
              />
              scope
            </label>
            <span className="min-w-0 truncate font-mono text-ink" title={p}>
              {p.split("/").pop() || p}
            </span>
            <span className="text-ink-muted">{scope.has(p) ? "— what to build" : "— background"}</span>
          </li>
        ))}
      </ul>
      {hint && (
        <p className="text-xs text-warning" data-testid={`${testPrefix}-scope-hint`}>
          This brief seems to refer to a guide or document. If it describes what to build, mark it as scope —
          otherwise it is read as background and its features are not added as requirements.
        </p>
      )}
    </div>
  );
}
