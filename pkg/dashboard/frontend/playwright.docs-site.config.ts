import { fileURLToPath } from 'node:url';
import { defineConfig, devices } from '@playwright/test';

// Browser acceptance for the REAL built documentation site — the MkDocs output
// itself, not documentation rendered inside the dashboard (that is e2e/mermaid.spec.ts).
// It lives in this package because this package is the repository's one pinned
// Playwright + Chromium installation; it is a separate suite, config and Make
// target because the dashboard bundle and the docs site are separate products.
//
// PORT is baked into mkdocs.test.yml's site_url as well: Material's instant
// navigation is gated on sitemap.xml, which is written from site_url, so the
// served origin and the configured one must match or instant navigation is
// inert. See mkdocs.test.yml.
const PORT = 4322;

// mkdocs must run from the repository root: pymdownx.snippets resolves
// `base_path: .` against the CWD, so building from anywhere else silently drops
// every snippet (and then fails --strict on the anchors they contribute).
const REPO_ROOT = fileURLToPath(new URL('../../..', import.meta.url));

export default defineConfig({
  testDir: './e2e-docs-site',
  timeout: 60_000,
  // Mermaid parses and lays out each diagram in the browser; the initial render
  // of a large flowchart is comfortably slower than a DOM assertion.
  expect: { timeout: 15_000 },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['list']] : [['list']],
  use: {
    baseURL: `http://127.0.0.1:${PORT}/`,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'docs-site', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    // The gate owns the build, so `npx playwright test -c playwright.docs-site.config.ts`
    // is self-sufficient and can never run against a stale site. --strict is the
    // production gate; mkdocs.test.yml INHERITs everything else from mkdocs.yml.
    command: `mkdocs build --strict --config-file mkdocs.test.yml && python3 -m http.server ${PORT} --directory site-test`,
    cwd: REPO_ROOT,
    url: `http://127.0.0.1:${PORT}/index.html`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
