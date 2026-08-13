/** A shell command, colored for reading.
 *
 * The Projects cards show commands a hundred characters long — env prefixes,
 * && chains, flags, paths — and a monochrome wall makes the person parse
 * with their eyes what a four-rule tokenizer parses for free: executables
 * bright, env assignments and flags in their own hues, the operators that
 * split one command from the next loudest of all. Read-only decoration; the
 * string itself is untouched (editors edit the plain text).
 */

type Tok = { text: string; kind: "exe" | "env" | "flag" | "op" | "str" | "plain" };

export function tokenizeShell(cmd: string): Tok[] {
  const out: Tok[] = [];
  // Split keeping separators: whitespace runs, chain operators, quoted strings.
  const parts = cmd.split(/(\s+|&&|\|\||[;|]|"[^"]*"|'[^']*')/g).filter((p) => p !== "" && p !== undefined);
  let expectExe = true;
  for (const p of parts) {
    if (/^\s+$/.test(p)) {
      out.push({ text: p, kind: "plain" });
      continue;
    }
    if (p === "&&" || p === "||" || p === ";" || p === "|") {
      out.push({ text: p, kind: "op" });
      expectExe = true;
      continue;
    }
    if (/^["'].*["']$/.test(p)) {
      out.push({ text: p, kind: "str" });
      continue;
    }
    if (expectExe && /^[A-Za-z_][A-Za-z0-9_]*=/.test(p)) {
      out.push({ text: p, kind: "env" }); // prefix assignment; the exe is still to come
      continue;
    }
    if (/^-/.test(p)) {
      out.push({ text: p, kind: "flag" });
      continue;
    }
    if (expectExe) {
      out.push({ text: p, kind: "exe" });
      expectExe = false;
      continue;
    }
    out.push({ text: p, kind: "plain" });
  }
  return out;
}

const COLOR: Record<Tok["kind"], string | undefined> = {
  exe: "var(--text-primary)",
  env: "var(--series-1)",
  flag: "var(--series-4)",
  op: "var(--series-2)",
  str: "var(--series-3)",
  plain: undefined, // inherits the row's muted tone
};

export function ShellCmd({ cmd, className, title }: { cmd: string; className?: string; title?: string }) {
  return (
    <span className={className} title={title ?? cmd} data-testid="shell-cmd">
      {tokenizeShell(cmd).map((t, i) => (
        <span
          key={i}
          data-kind={t.kind !== "plain" ? t.kind : undefined}
          style={COLOR[t.kind] ? { color: COLOR[t.kind], fontWeight: t.kind === "op" ? 600 : undefined } : undefined}
        >
          {t.text}
        </span>
      ))}
    </span>
  );
}
