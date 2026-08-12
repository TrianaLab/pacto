import { defineConfig, devices } from '@playwright/test';

// Browser E2E against the LIVE, operator-managed dashboard running in a kind
// cluster (port-forwarded), seeded by tests/e2e/kind/operational-graph.sh. Unlike
// playwright.config.ts (which serves the static WASM demo), this exercises the real
// HTTP API surface served by the dashboard container over the wire — so there is no
// webServer here; the caller provides PW_BASE_URL for the forwarded Service.
const BASE = process.env.PW_BASE_URL || 'http://127.0.0.1:8080/';

export default defineConfig({
  testDir: './e2e-live',
  timeout: 60_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  // No retries, here or in CI: these journeys assert facts the live fixture has
  // already been proven to hold (tests/e2e/kind/productready gates on them), so a
  // second attempt could only ever paper over a race in the product or in the test.
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: BASE,
    // Artifacts land in the gitignored test-results/ directory, never in the tree.
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'live', use: { ...devices['Desktop Chrome'] } }],
});
