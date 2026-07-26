/** Hash routing. A pop-out window opens a route directly (08 §1.3), so routes
 * must be addressable without a router that owns history. */

export type Route =
  | { name: "overview" }
  | { name: "runs" }
  | { name: "run"; id: string }
  | { name: "ducklings" }
  | { name: "settings" };

export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#/, "").replace(/^\//, "");
  const [head, arg] = path.split("/");
  switch (head) {
    case "runs":
      return arg ? { name: "run", id: arg } : { name: "runs" };
    case "ducklings":
      return { name: "ducklings" };
    case "settings":
      return { name: "settings" };
    default:
      return { name: "overview" };
  }
}

export function routeHref(route: Route): string {
  switch (route.name) {
    case "run":
      return `#/runs/${route.id}`;
    case "runs":
      return "#/runs";
    case "ducklings":
      return "#/ducklings";
    case "settings":
      return "#/settings";
    default:
      return "#/overview";
  }
}
