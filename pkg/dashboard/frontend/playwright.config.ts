import { defineConfig, devices } from '@playwright/test';

// Browser E2E for the dashboard, run against the BUILT WASM demo (the real Svelte
// bundle + the real Huma dashboard compiled to wasm, serving from embedded data).
// This is the closest thing to the operator-managed dashboard that runs with no
// cluster: the same frontend, the same API surface, deterministic data.
//
// The demo dist must be built first: `make -C examples/demo build`
// (the `make e2e-dashboard-wasm` target does this, then runs these tests).
const PORT = Number(process.env.PW_PORT || 4321);
const DIST = '../../../examples/demo/dist';

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['list']] : [['list']],
  use: {
    baseURL: `http://127.0.0.1:${PORT}/`,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'] }, testIgnore: /mobile\.spec\.ts/ },
    { name: 'mobile', use: { ...devices['Pixel 5'] }, testMatch: /mobile\.spec\.ts/ },
  ],
  webServer: {
    command: `python3 -m http.server ${PORT} --directory ${DIST}`,
    url: `http://127.0.0.1:${PORT}/index.html`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
});
