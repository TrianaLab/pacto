import { defineConfig, devices } from '@playwright/test';
import { APP_PORT } from './e2e/ports';

// The documentation screenshots (docs/assets/screenshots/dashboard-*.png), captured
// from the BUILT WASM demo so they are reproducible: `make docs-screenshots`. A
// separate config, so the PR suite never rewrites the docs' images.
export default defineConfig({
  testDir: './e2e-screenshots',
  timeout: 90_000,
  expect: { timeout: 20_000 },
  workers: 1,
  reporter: [['list']],
  use: {
    ...devices['Desktop Chrome'],
    baseURL: `http://127.0.0.1:${APP_PORT}/`,
    viewport: { width: 1600, height: 1000 },
    deviceScaleFactor: 1,
    // Partial raster repaints only a tile's dirty rect, and the dither of a gradient or
    // a mask then lands a few grey levels apart depending on which rect a late update
    // dirtied. Whole-tile raster makes two runs over the same build match pixel for pixel.
    launchOptions: { args: ['--disable-partial-raster'] },
  },
  webServer: {
    command: `python3 e2e/serve.py ${APP_PORT} ../../../examples/demo/dist`,
    url: `http://127.0.0.1:${APP_PORT}/index.html`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
});
