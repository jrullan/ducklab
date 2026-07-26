import type { StatusRole } from "../lib/colors";
import { statusIcon, statusVar } from "../lib/colors";

/**
 * Status is never colour alone (08 §2.1). Two of the four status steps sit
 * below 3:1 on the light surface by design; the icon and the label are the
 * mitigation, so this component always renders all three.
 */
export function StatusChip({ role, label }: { role: StatusRole; label: string }) {
  return (
    <span
      data-testid="status-chip"
      data-role={role}
      style={{ color: statusVar(role) }}
      className="inline-flex items-center gap-1 text-sm"
    >
      <span aria-hidden="true">{statusIcon(role)}</span>
      <span>{label}</span>
    </span>
  );
}
