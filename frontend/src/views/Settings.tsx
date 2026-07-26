import { applyTheme, saveTheme, type Theme } from "../app/theme";
import { StatusChip } from "../components/StatusChip";

/**
 * Settings. Secrets are never displayed: a key field shows whether it is set
 * and the env var it reads, never the value (07 §4.9).
 */
export function Settings({
  theme, onTheme, engineVersion, connection,
}: {
  theme: Theme;
  onTheme: (t: Theme) => void;
  engineVersion: string;
  connection: string;
}) {
  const change = (t: Theme) => {
    applyTheme(t);
    saveTheme(t);
    onTheme(t);
  };
  return (
    <div className="p-4" data-testid="settings">
      <section>
        <h2 className="text-sm text-ink-muted">theme</h2>
        <div className="mt-1 flex gap-2">
          {(["light", "dark", "system"] as Theme[]).map((t) => (
            <button
              key={t}
              type="button"
              onClick={() => change(t)}
              data-testid={`theme-${t}`}
              aria-pressed={theme === t}
              className={`rounded border border-hairline px-2 py-1 text-sm ${theme === t ? "text-ink" : "text-ink-muted"}`}
            >
              {t}
            </button>
          ))}
        </div>
      </section>

      <section className="mt-4">
        <h2 className="text-sm text-ink-muted">engine</h2>
        <div className="mt-1 flex items-center gap-3">
          <StatusChip
            role={connection === "open" ? "good" : connection === "reconnecting" ? "warning" : "critical"}
            label={connection}
          />
          <span className="text-ink-secondary">version {engineVersion || "unknown"}</span>
        </div>
        <p className="mt-2 text-sm text-ink-muted">
          API keys are read from environment variables and are never stored or displayed here.
        </p>
      </section>
    </div>
  );
}
