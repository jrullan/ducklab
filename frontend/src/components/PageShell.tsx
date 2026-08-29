import type { ReactNode } from "react";

/** Shared visual grammar for top-level rooms. The room still owns its
 * workflow; this only keeps identity, actions and vertical rhythm stable. */
export function PageHeader({
  eyebrow,
  title,
  subtitle,
  actions,
}: {
  eyebrow?: string;
  title: string;
  subtitle: string;
  actions?: ReactNode;
}) {
  return (
    <header className="flex flex-wrap items-start gap-4 border-b border-hairline pb-4">
      <div className="min-w-0 flex-1">
        {eyebrow && <p className="text-xs uppercase tracking-[0.14em] text-ink-muted">{eyebrow}</p>}
        <h1 className={`${eyebrow ? "mt-1 " : ""}text-xl font-semibold text-ink`}>{title}</h1>
        <p className="mt-1 max-w-3xl text-sm text-ink-muted">{subtitle}</p>
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </header>
  );
}

export function ContextStrip({ children, tone = "neutral" }: {
  children: ReactNode;
  tone?: "neutral" | "attention";
}) {
  return (
    <section
      className={`rounded-card border px-3 py-2 text-sm ${
        tone === "attention" ? "border-warning bg-surface1" : "border-hairline bg-surface1"
      }`}
    >
      {children}
    </section>
  );
}

export function CollectionToolbar({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-card border border-hairline bg-surface1 p-2">
      {children}
    </div>
  );
}

export function InspectorPane({ title, children, empty }: {
  title: string;
  children?: ReactNode;
  empty?: string;
}) {
  return (
    <aside className="min-w-0 border-t border-hairline bg-surface1 p-4 lg:border-l lg:border-t-0">
      <h2 className="text-sm font-semibold text-ink">{title}</h2>
      {children ?? <p className="mt-4 rounded-card border border-dashed border-hairline p-4 text-sm text-ink-muted">{empty}</p>}
    </aside>
  );
}
