import { describe, it, expect } from "vitest";
import { applyTheme, loadTheme, saveTheme } from "./theme";

function fakeRoot() {
  return document.createElement("html");
}

describe("theme", () => {
  // The toggle must beat the OS setting in BOTH directions: a light stamp has
  // to win under OS-dark, which is why "system" removes the attribute rather
  // than setting a third value.
  it("stamps data-theme for an explicit choice and clears it for system", () => {
    const root = fakeRoot();
    applyTheme("dark", root);
    expect(root.getAttribute("data-theme")).toBe("dark");
    applyTheme("light", root);
    expect(root.getAttribute("data-theme")).toBe("light");
    applyTheme("system", root);
    expect(root.hasAttribute("data-theme")).toBe(false);
  });

  it("round-trips through storage and defaults to system", () => {
    const store = new Map<string, string>();
    const storage = {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
    };
    expect(loadTheme(storage)).toBe("system");
    saveTheme("dark", storage);
    expect(loadTheme(storage)).toBe("dark");
  });

  it("falls back to system on a corrupt stored value", () => {
    const storage = { getItem: () => "purple", setItem: () => {} };
    expect(loadTheme(storage)).toBe("system");
  });
});
