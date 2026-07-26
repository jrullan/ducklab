/** Theme handling. Dark is a selected token set, not an inversion; the
 * in-app toggle stamps data-theme on the root so it wins over the OS setting
 * in both directions. */

export type Theme = "light" | "dark" | "system";

const KEY = "ducklab.theme";

export function applyTheme(theme: Theme, root: HTMLElement = document.documentElement): void {
  if (theme === "system") {
    root.removeAttribute("data-theme");
  } else {
    root.setAttribute("data-theme", theme);
  }
}

export function loadTheme(storage: Pick<Storage, "getItem"> = localStorage): Theme {
  const v = storage.getItem(KEY);
  return v === "light" || v === "dark" ? v : "system";
}

export function saveTheme(theme: Theme, storage: Pick<Storage, "setItem"> = localStorage): void {
  storage.setItem(KEY, theme);
}
