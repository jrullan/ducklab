/** Selection is an interaction state, never a severity. */
export function selectableSurface(selected: boolean): string {
  return selected
    ? "border-accent bg-surface2 shadow-[inset_3px_0_0_var(--accent)]"
    : "border-hairline hover:border-axis hover:bg-surface2";
}
