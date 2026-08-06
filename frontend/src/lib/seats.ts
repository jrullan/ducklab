/** Seats: the stable dimension of a line-up.
 *
 * One dropdown per seat instead of one checkbox per duckling — a fleet of ten
 * models made every picker a wall of boxes that widened with each model
 * added. Shared by Settings (saved line-ups) and the run launcher (this run's
 * line-up), because two pickers that disagree about what a seat means would
 * be worse than the wall.
 */

/** How many seats a mode has: fixed for solo and pair, 0 for "as many as you
 * fill" (council, tournament, split — they start at two). */
export function fixedSeats(mode: string): number {
  switch (mode) {
    case "solo":
      return 1;
    case "pair":
      return 2;
  }
  return 0;
}

/** What the seat's position means in this mode — the position IS the role. */
export function seatLabel(mode: string, i: number): string {
  switch (mode) {
    case "solo":
      return "implementer";
    case "pair":
      return i === 0 ? "implementer" : "reviewer";
    case "council":
      return i === 0 ? "drafts" : `critic ${i}`;
    case "tournament":
      return `contestant ${i + 1}`;
    case "split":
      return `worker ${i + 1}`;
  }
  return `#${i + 1}`;
}
