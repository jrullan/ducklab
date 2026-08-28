/** Hash routing. A pop-out window opens a route directly (08 §1.3), so routes
 * must be addressable without a router that owns history. */

export type SettingsSection = "ducklings" | "fleet" | "budgets" | "autopilot" | "remote" | "appearance" | "engine";

export type Route =
  | { name: "now" }
  | { name: "bench" }
  | { name: "runs" }
  | { name: "cycle"; stage?: string }
  | { name: "ledger" }
  | { name: "board"; tab?: string }
  | { name: "review" }
  | { name: "release" }
  | { name: "reports" }
  | { name: "projects" }
  | { name: "run"; id: string }
  | { name: "ducklings" }
  | { name: "settings"; section?: SettingsSection }
  | { name: "flock" }
  | { name: "skills" };

export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#/, "").replace(/^\//, "");
  const [pathWithoutQuery = "", query = ""] = path.split("?");
  const [head, arg] = pathWithoutQuery.split("/");
  switch (head) {
    case "now":
      return { name: "now" };
    // Overview was absorbed by the inbox (docs/ux-evaluation.md phase 3); the
    // old links keep working by landing where its job went.
    case "overview":
      return { name: "now" };
    case "bench":
      return { name: "bench" };
    case "runs":
      return arg ? { name: "run", id: arg } : { name: "runs" };
    case "cycle":
      // #/cycle/plan opens straight on a tab, so a pop-out can carry one.
      return arg === "ledger" ? { name: "ledger" } : arg ? { name: "cycle", stage: arg } : { name: "cycle" };
    case "board":
      // #/board/bugs opens straight on a board, so a pop-out can carry one.
      return arg ? { name: "board", tab: arg } : { name: "board" };
    case "review":
      return { name: "review" };
    case "release":
      return { name: "release" };
    case "reports":
      return { name: "reports" };
    case "projects":
      return { name: "projects" };
    case "ducklings":
      return { name: "ducklings" };
    case "settings": {
      const section = arg || new URLSearchParams(query).get("section") || undefined;
      return isSettingsSection(section) ? { name: "settings", section } : { name: "settings" };
    }
    case "flock":
    case "roster": // Backward-compatible bookmarks; Flock is the public name.
      return { name: "flock" };
    case "skills":
      return { name: "skills" };
    default:
      return { name: "now" };
  }
}

function isSettingsSection(value: string | undefined): value is SettingsSection {
  return value === "ducklings" || value === "fleet" || value === "budgets" || value === "autopilot" || value === "remote" || value === "appearance" || value === "engine";
}

export function routeHref(route: Route): string {
  switch (route.name) {
    case "run":
      return `#/runs/${route.id}`;
    case "runs":
      return "#/runs";
    case "cycle":
      return route.stage ? `#/cycle/${route.stage}` : "#/cycle";
    case "ledger":
      return "#/cycle/ledger";
    case "board":
      return route.tab ? `#/board/${route.tab}` : "#/board";
    case "review":
      return "#/review";
    case "release":
      return "#/release";
    case "reports":
      return "#/reports";
    case "projects":
      return "#/projects";
    case "ducklings":
      return "#/ducklings";
    case "settings":
      return route.section ? `#/settings/${route.section}` : "#/settings";
    case "flock":
      return "#/flock";
    case "skills":
      return "#/skills";
    case "now":
      return "#/now";
    case "bench":
      return "#/bench";
    default:
      return "#/now";
  }
}
