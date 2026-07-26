import type { ReactNode } from "react";

/** Every empty view says what would fill it and offers one action. */
export function EmptyState({ message, action }: { message: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-3 p-12 text-center" data-testid="empty-state">
      <div className="text-lg" aria-hidden="true">🦆</div>
      <p className="text-ink-secondary">{message}</p>
      {action}
    </div>
  );
}
