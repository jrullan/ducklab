/**
 * Start a project from an idea (B-456).
 *
 * The greenfield path used to begin with an absolute folder path and git, in
 * Settings, and the brief lived three screens away. This form asks in the
 * order a person thinks: what to build, what to call it, what to look at;
 * where it lives is optional (default ~/Ducklab/<name>). One click creates the
 * project and starts the run that drafts the requirements, and the person is
 * taken to that run. A machine with no git identity is asked once, here, and
 * the answer is set for this project only (B-463).
 */

import { useEffect, useState } from "react";
import { ApiError, type EngineClient, type ProjectPreset, type ProjectStartResult } from "../api/client";
import { canChooseDirectory, canChooseFile, chooseDirectory, chooseFile } from "../lib/picker";

const isImageRef = (path: string) => /\.(png|jpe?g|webp|gif)$/i.test(path);

/** Absolute on any platform the desktop runs on: POSIX /…, Windows C:\… or
 * C:/…, and UNC \\server\share (review of #121: requiring "/" refused every
 * Windows folder, including the one Browse returned). The engine still has
 * the final word with filepath.IsAbs. */
export function isAbsolutePath(p: string): boolean {
  return /^(\/|[A-Za-z]:[\\/]|\\\\)/.test(p);
}

export function slugPreview(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
}

export function StartProject({
  client,
  onStarted,
  modelWarning,
}: {
  client: EngineClient;
  onStarted: (result: ProjectStartResult) => void;
  /** Said beside the button when no model has answered yet. */
  modelWarning?: string;
}) {
  const [brief, setBrief] = useState("");
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const [refsText, setRefsText] = useState("");
  const [needIdentity, setNeedIdentity] = useState(false);
  const [gitName, setGitName] = useState("");
  const [gitEmail, setGitEmail] = useState("");
  const [presets, setPresets] = useState<ProjectPreset[]>([]);
  const [preset, setPreset] = useState("");
  useEffect(() => {
    if (typeof client.projectPresets !== "function") return;
    let live = true;
    client.projectPresets().then((items) => { if (live) setPresets(items); }).catch(() => {});
    return () => { live = false; };
  }, [client]);
  // The folder preference (Settings → Projects); "~/Ducklab" until it loads
  // or when the engine predates the preference.
  const [projectsDir, setProjectsDir] = useState("~/Ducklab");
  useEffect(() => {
    if (typeof client.projectDefaults !== "function") return;
    let live = true;
    client.projectDefaults().then((v) => { if (live && v.effective) setProjectsDir(v.effective); }).catch(() => {});
    return () => { live = false; };
  }, [client]);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const refs = refsText.split("\n").map((l) => l.trim()).filter(Boolean);
  const images = refs.filter(isImageRef).length;
  const slug = slugPreview(name);
  const pathProblem = path.trim() && !isAbsolutePath(path.trim())
    ? "Use a full folder path, like /home/you/app or C:\\Users\\you\\app (Browse fills one in), or leave it empty for the default."
    : null;
  const identityMissing = needIdentity && (!gitName.trim() || !gitEmail.trim());
  const canStart = !!name.trim() && !pathProblem && !identityMissing && !busy;

  const start = async () => {
    setBusy(true);
    setFailure(null);
    try {
      const result = await client.projectStart({
        name: name.trim(),
        brief: brief.trim(),
        ...(path.trim() ? { path: path.trim() } : {}),
        ...(refs.length ? { refs } : {}),
        ...(preset ? { preset } : {}),
        ...(needIdentity ? { git_name: gitName.trim(), git_email: gitEmail.trim() } : {}),
      });
      onStarted(result);
    } catch (e) {
      if (e instanceof ApiError && e.code === "git_identity_required") {
        setNeedIdentity(true);
        setFailure(null);
      } else {
        setFailure(e instanceof Error ? e.message : String(e));
      }
    } finally {
      setBusy(false);
    }
  };

  const field = "w-full rounded border border-hairline bg-surface2 px-2 py-1.5 text-sm text-ink";
  return (
    <div className="space-y-4" data-testid="start-project">
      <label className="block space-y-1">
        <span className="text-sm font-medium text-ink">What do you want to build?</span>
        <textarea
          data-testid="start-brief"
          rows={4}
          value={brief}
          onChange={(e) => setBrief(e.target.value)}
          placeholder="Describe it in your own words. Leave it empty and Ducklab will interview you instead."
          className={field}
        />
      </label>

      <label className="block space-y-1">
        <span className="text-sm font-medium text-ink">Name</span>
        <input data-testid="start-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="TI-36X calculator" className={field} />
      </label>

      {presets.length > 0 && (
        <fieldset className="space-y-1" data-testid="start-presets">
          <legend className="text-sm font-medium text-ink">What kind of project?</legend>
          {[{ id: "", label: "Something else", summary: "Ducklab sets nothing up; you configure how it runs later." }, ...presets].map((p) => (
            <label key={p.id || "none"} className="flex items-start gap-2 text-sm">
              <input
                type="radio"
                name="start-preset"
                value={p.id}
                checked={preset === p.id}
                onChange={() => setPreset(p.id)}
                data-testid={`start-preset-${p.id || "none"}`}
                className="mt-1"
              />
              <span>
                <span className="text-ink">{p.label}</span>
                <span className="block text-xs text-ink-secondary">{p.summary}</span>
              </span>
            </label>
          ))}
        </fieldset>
      )}

      <div className="space-y-1">
        <span className="text-sm font-medium text-ink">References <span className="font-normal text-ink-muted">(optional)</span></span>
        <p className="text-xs text-ink-secondary">
          Documents (.md, .txt) or images (.png, .jpg, .webp, .gif) of what it should look like, one per line.
          {images > 0 && ` ${images} image${images === 1 ? "" : "s"}: shown to a model that can see, and citable in the requirements.`}
        </p>
        <div className="flex items-start gap-2">
          <textarea data-testid="start-refs" rows={2} value={refsText} onChange={(e) => setRefsText(e.target.value)} className={`${field} font-mono text-xs`} />
          {canChooseFile() && (
            <button
              type="button"
              onClick={() => void chooseFile().then((p) => p && setRefsText((cur) => (cur ? `${cur}\n${p}` : p)))}
              className="rounded border border-hairline px-2 py-1 text-xs text-ink-secondary"
            >
              Browse…
            </button>
          )}
        </div>
      </div>

      <div className="space-y-1">
        <span className="text-sm font-medium text-ink">Folder <span className="font-normal text-ink-muted">(optional)</span></span>
        <div className="flex items-center gap-2">
          <input
            data-testid="start-path"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            placeholder={`${projectsDir}/${slug || "<name>"}`}
            className={`${field} font-mono text-xs`}
          />
          {canChooseDirectory() && (
            <button
              type="button"
              onClick={() => void chooseDirectory().then((p) => p && setPath(p))}
              className="rounded border border-hairline px-2 py-1 text-xs text-ink-secondary"
            >
              Browse…
            </button>
          )}
        </div>
        {pathProblem && <p className="text-xs text-critical" data-testid="start-path-problem">{pathProblem}</p>}
      </div>

      {needIdentity && (
        <div className="space-y-2 rounded-card border border-warning p-3" data-testid="start-git-identity">
          <p className="text-sm text-ink">
            Ducklab keeps every change in git, and git needs a name and email to record them. This computer has
            none set. They are saved for this project only.
          </p>
          <div className="flex flex-wrap gap-2">
            <input data-testid="start-git-name" value={gitName} onChange={(e) => setGitName(e.target.value)} placeholder="Your name" className={`${field} max-w-xs`} />
            <input data-testid="start-git-email" value={gitEmail} onChange={(e) => setGitEmail(e.target.value)} placeholder="you@example.com" className={`${field} max-w-xs`} />
          </div>
        </div>
      )}

      <div className="flex items-center gap-3">
        <button
          type="button"
          data-testid="start-submit"
          onClick={() => void start()}
          disabled={!canStart}
          className="rounded border border-good px-4 py-2 text-sm font-medium text-good disabled:border-hairline disabled:text-ink-muted disabled:opacity-50"
        >
          {busy ? "Creating…" : "Create project and draft the requirements"}
        </button>
        <span className="text-xs text-ink-muted">You approve the requirements before anything else happens.</span>
      </div>
      {modelWarning && <p className="text-xs text-warning" data-testid="start-model-warning">{modelWarning}</p>}
      {failure && <p className="text-sm text-critical" data-testid="start-failure">{failure}</p>}
    </div>
  );
}
