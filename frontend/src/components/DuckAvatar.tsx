import { ducklingColor } from "../lib/colors";

/** The duck glyph, tinted with the duckling's stable slot colour. */
export function DuckAvatar({ id, roster }: { id: string; roster: readonly string[] }) {
  return (
    <span
      data-testid="duck-avatar"
      title={id}
      style={{ color: ducklingColor(id, roster) }}
      aria-hidden="true"
    >
      🦆
    </span>
  );
}
