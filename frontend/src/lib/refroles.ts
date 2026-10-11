// Reference roles (B-520). A reference is background (context; the default)
// or scope (it describes the product the brief asks to build toward). The
// person sets the role per reference — the engine never infers it, because a
// guess either way rewrites what the person asked for.

import type { StageRef } from "../api/client";

export type RefRole = "background" | "scope";

const IMAGE = /\.(png|jpe?g|webp|gif)$/i;

/** Images travel as images whatever their role, so they get no toggle. */
export function isImageRef(path: string): boolean {
  return IMAGE.test(path);
}

/**
 * The request's reference list: background paths as plain strings (the
 * shape every engine has always accepted), scope paths as {path, role}.
 */
export function stageRefs(paths: string[], scope: ReadonlySet<string>): StageRef[] {
  return paths.map((p) => (scope.has(p) && !isImageRef(p) ? { path: p, role: "scope" } : p));
}

// Phrases that point the brief AT a reference: "use this guide", "match the
// manual", "according to the spec", "fill the gap … intended". Deterministic
// on purpose — this only suggests a toggle, it never sets one.
const TARGETS_REFERENCE = [
  /\b(this|the|that|attached|these)\s+(guide|guidebook|manual|handbook|spec|specification|document|doc|reference|datasheet|standard|rfc)s?\b/i,
  /\b(according to|as described in|described in|as specified in|per)\s+(the|this|that)\b/i,
  /\bintended (functionality|behaviou?r|features?)\b/i,
];

/**
 * True when the brief reads as if a reference is the target, there are
 * document references, and none is marked scope yet — the moment to ask
 * "mark it as scope?" instead of letting the default silently apply.
 */
export function suggestScope(brief: string, docPaths: string[], scope: ReadonlySet<string>): boolean {
  if (docPaths.length === 0) return false;
  if (docPaths.some((p) => scope.has(p))) return false;
  return TARGETS_REFERENCE.some((re) => re.test(brief));
}
