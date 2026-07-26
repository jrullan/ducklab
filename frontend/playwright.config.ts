import { defineConfig } from "@playwright/test";

/**
 * Drives the real frontend against cmd/fake-engine (06 Appendix B).
 *
 * No model, no GPU, no repo: the double speaks the same HTTP contract as
 * ducklab-engine and reuses internal/bus, so these tests exercise the actual
 * streaming and reconnection paths rather than a mock of them.
 */
export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: "http://127.0.0.1:5178",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "npm run preview -- --port 5178 --strictPort",
    url: "http://127.0.0.1:5178",
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
