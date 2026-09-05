import { defineConfig, devices } from "@playwright/test";

// These tests drive the real editor UI against a running dev-mode server —
// `go run . -dev-mode` with a `../nyc_code_archive` checkout alongside this
// repo (see EDITOR_PLAN.md and intro_nyc.go's `-law-path` flag), reachable at
// the hostname below (mkcert + /etc/hosts, same setup `-dev-mode` already
// expects). They sign in via `GET /_admin/testing/auth?email=`, a route that
// only responds in dev mode (see editor_auth.go), so no real Firebase
// sign-in flow is needed.
const baseURL = process.env.EDITOR_BASE_URL || "https://editor.dev.intro.nyc";

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: [["list"]],
  use: {
    baseURL,
    ignoreHTTPSErrors: true,
    trace: "on-first-retry",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
