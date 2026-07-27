import { ducklingColor } from "../lib/colors";

/** The duck glyph, tinted with the duckling's stable slot colour.
 *
 * It bobs while its turn is in flight (08 §4.4). A thinking turn otherwise
 * looked exactly like a finished one that said nothing — the difference
 * between "wait" and "something is wrong". */
export function DuckAvatar({
  id,
  roster,
  bobbing = false,
}: {
  id: string;
  roster: readonly string[];
  bobbing?: boolean;
}) {
  return (
    <span
      data-testid="duck-avatar"
      data-bobbing={bobbing ? "true" : "false"}
      title={id}
      className={bobbing ? "inline-block duck-bob" : "inline-block"}
      style={{ color: ducklingColor(id, roster) }}
      aria-hidden="true"
    >
      🦆
    </span>
  );
}
