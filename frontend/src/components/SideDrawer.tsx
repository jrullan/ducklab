import { useEffect, type ReactNode } from "react";

/** Stable editing surface for forms that used to expand inside cards and
 * move the collection underneath the pointer. */
export function SideDrawer({ title, subtitle, onClose, children, testId }: {
  title: string;
  subtitle?: string;
  onClose: () => void;
  children: ReactNode;
  testId?: string;
}) {
  useEffect(() => {
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex justify-end" data-testid={testId}>
      <button type="button" aria-label="Close drawer" className="absolute inset-0 bg-ink/20" onClick={onClose} />
      <aside role="dialog" aria-modal="true" aria-label={title} className="relative flex h-full w-full max-w-2xl flex-col border-l border-hairline bg-page shadow-2xl">
        <header className="flex items-start gap-4 border-b border-hairline p-4">
          <div className="min-w-0 flex-1">
            <h2 className="text-lg font-semibold text-ink">{title}</h2>
            {subtitle && <p className="mt-1 text-sm text-ink-muted">{subtitle}</p>}
          </div>
          <button type="button" onClick={onClose} className="rounded border border-hairline px-2 py-1 text-sm text-ink-muted hover:text-ink" aria-label={`Close ${title}`}>×</button>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto p-4">{children}</div>
      </aside>
    </div>
  );
}
