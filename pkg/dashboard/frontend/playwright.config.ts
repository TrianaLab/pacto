import { defineConfig, devices } from '@playwright/test';
import { APP_PORT, DOC_PORT } from './e2e/ports';

// Browser E2E for the dashboard, run against the BUILT WASM demo (the real Svelte
// bundle + the real Huma dashboard compiled to wasm, serving from embedded data).
// This is the closest thing to the operator-managed dashboard that runs with no
// cluster: the same frontend, the same API surface, deterministic data.
//
// A second server carries the offline `pacto doc --format html` export, which is a
// NON-Fleet host and the only place the retained legacy views are ever served.
//
// Both must be built first: `make -C examples/demo build doc-export`
// (the `make e2e-dashboard-wasm` target does this, then runs these tests).
const DIST = '../../../examples/demo/dist';
const DOC_DIST = '../../../examples/demo/.doc-export';

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: true,
  // Half the cores, stated rather than left to the default so the reasoning is on the
  // page: each worker is a whole Chromium whose main thread also runs the Go engine, so
  // a worker needs more than one core to itself. That is 6 on a 12-core laptop and 2 on
  // GitHub's 4-vCPU ubuntu-latest, which is what keeps the CI job fast without starving it.
  workers: '50%',
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['list']] : [['list']],
  use: {
    baseURL: `http://127.0.0.1:${APP_PORT}/`,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'] }, testIgnore: /mobile\.spec\.ts/ },
    { name: 'mobile', use: { ...devices['Pixel 5'] }, testMatch: /mobile\.spec\.ts/ },
  ],
  webServer: [
    {
      command: `python3 e2e/serve.py ${APP_PORT} ${DIST}`,
      url: `http://127.0.0.1:${APP_PORT}/index.html`,
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
    },
    {
      command: `python3 e2e/serve.py ${DOC_PORT} ${DOC_DIST}`,
      url: `http://127.0.0.1:${DOC_PORT}/index.html`,
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
    },
  ],
});
