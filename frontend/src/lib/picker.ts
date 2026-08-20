/**
 * The folder chooser, which is the one thing the engine cannot do for us: it
 * has no screen.
 *
 * Everything else the desktop does goes over HTTP. This is a native binding,
 * so it exists only in the desktop — in a browser or a test the caller gets
 * null and must fall back to asking for a path.
 */

declare global {
  interface Window {
    wails?: { Call?: { ByName?: (name: string, ...args: unknown[]) => Promise<unknown> } };
  }
}

/** True when a native chooser is available. */
export function canChooseDirectory(): boolean {
  return Boolean(window.ducklab?.chooseDirectory && window.wails?.Call?.ByName);
}

/**
 * Opens the system folder chooser.
 *
 * Returns null when the person cancelled, and null when there is no chooser at
 * all. The caller cannot tell those apart on purpose: both mean "no path", and
 * a UI that had to branch on which would be a UI with two empty states.
 */
export async function chooseDirectory(title = "Choose a project folder"): Promise<string | null> {
  const name = window.ducklab?.chooseDirectory;
  const call = window.wails?.Call?.ByName;
  if (!name || !call) return null;
  try {
    const path = await call(name, title);
    return typeof path === "string" && path !== "" ? path : null;
  } catch {
    // A dialog that fails is not worth crashing a page over; the path field
    // beside the button still works.
    return null;
  }
}

/** True when a native reference-file chooser is available. */
export function canChooseFile(): boolean {
  return Boolean(window.ducklab?.chooseFile && window.wails?.Call?.ByName);
}

/** Opens the system chooser for a reference document. */
export async function chooseFile(title = "Choose a reference document"): Promise<string | null> {
  const name = window.ducklab?.chooseFile;
  const call = window.wails?.Call?.ByName;
  if (!name || !call) return null;
  try {
    const path = await call(name, title);
    return typeof path === "string" && path !== "" ? path : null;
  } catch {
    return null;
  }
}
