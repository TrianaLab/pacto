import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

// Automated WCAG A/AA accessibility gate over
// representative REAL product states of the built WASM demo, in BOTH themes. It is wired
// into the blocking dashboard browser CI (playwright, desktop project). NO rule is
// blanket-disabled -- color-contrast is now ENFORCED (the design tokens were measured and
// deepened so every rendered text pair clears AA 4.5:1 in dark and light; see tokens.css).
// Every WCAG 2.0/2.1 A and AA rule, contrast included, is asserted here.
const TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'];

// settle waits for the page to STOP MOVING, so axe samples it at rest: a fade's
// mid-flight opacity transiently dips muted text below the AA ratio and reports a
// contrast failure against a colour the reader never sees.
//
// Two hazards, and a single wait catches only one of them. Waiting once for the
// animations that exist right now misses every entrance that has not started yet --
// each test gates on a heading, and the list under it arrives later, so at that instant
// there is frequently nothing running and the audit proceeds straight into the fade.
// Hence two consecutive quiet samples rather than one: an entrance that begins between
// them resets the count.
//
// Every product animation is finite by design (see the alarm note in
// FleetAttentionView -- it is three pulses and not an infinite loop precisely so this
// can be a real wait), so the deadline is a backstop against a bug, not the normal path.
async function settle(page: Page) {
  const deadline = Date.now() + 6_000;
  const moving = () => page.evaluate(() => (document.getAnimations?.() ?? []).some((a) => a.playState === 'running'));
  let quiet = 0;
  while (quiet < 2 && Date.now() < deadline) {
    quiet = (await moving()) ? 0 : quiet + 1;
    await page.waitForTimeout(200);
  }
}

async function audit(page: Page, testInfo: { title: string }) {
  await settle(page);
  const results = await new AxeBuilder({ page })
    .withTags(TAGS)
    .analyze();
  expect(results.violations, `${testInfo.title}: ${JSON.stringify(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

// lightTheme forces the light palette before load (the app reads pacto-theme early), so the
// same product state can be contrast-audited in light as well as the default dark.
async function lightTheme(page: Page) {
  await page.addInitScript(() => { try { localStorage.setItem('pacto-theme', 'light'); } catch { /* private mode */ } });
}

async function ready(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: 20_000 });
}

test.describe('WCAG A/AA axe gate over product states', () => {
  // Headless Chromium prefers light, and the app honors prefers-color-scheme, so without
  // this the "default" audits would silently run LIGHT. Emulate a dark preference so these
  // tests genuinely exercise the DARK palette; the LIGHT tests below force light via
  // localStorage (which wins over the media preference).
  test.use({ colorScheme: 'dark' });

  test('Operational Overview', async ({ page }, ti) => {
    await ready(page);
    await page.goto('/#/fleet');
    await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Services list', async ({ page }, ti) => {
    await page.goto('/#/fleet/services');
    await expect(page.getByRole('heading', { level: 1, name: 'Services' })).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Service detail', async ({ page }, ti) => {
    await page.goto('/#/fleet/services/payments-service');
    await expect(page.getByTestId('graph-legend').or(page.locator('.ev-body'))).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Attention', async ({ page }, ti) => {
    await page.goto('/#/fleet/attention');
    await expect(page.getByRole('heading', { level: 1, name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Change analysis', async ({ page }, ti) => {
    await page.goto('/#/fleet/changes/payments-service');
    await expect(page.getByRole('heading', { level: 1, name: 'Change analysis' })).toBeVisible({ timeout: 20_000 });
    // Audit the SETTLED page, not the transient "Loading service revisions..." spinner
    // (whose fadeIn is caught mid-animation otherwise): the selector is enabled once the
    // revision universe has loaded.
    await expect(page.locator('#impact-old-rev')).toBeEnabled({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Operational Graph discovery', async ({ page }, ti) => {
    await page.goto('/#/fleet/graph');
    await expect(page.getByTestId('graph-discovery')).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Focused visual graph', async ({ page }, ti) => {
    await page.goto('/#/fleet/graph/service/payments-service');
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Graph quick-inspection drawer open', async ({ page }, ti) => {
    await page.goto('/#/fleet/graph/service/payments-service');
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
    await page.getByTestId('graph-textalt').locator('summary').click();
    await page.getByTestId('graph-node-item').first().click();
    await expect(page.getByTestId('graph-drawer')).toBeVisible();
    await audit(page, ti);
  });

  // A degraded data source: the source-health tones, the two landmarks in the page
  // (contents rail and the health chip strip), and an error box drawn in the err
  // palette -- the highest contrast risk on the page and the one a reader most needs
  // to be able to read.
  test('Data source detail (degraded)', async ({ page }, ti) => {
    await page.goto('/#/fleet/sources/edge-cluster');
    await expect(page.getByRole('heading', { level: 2, name: 'Reported failure' })).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Data sources inventory (health tally + filters)', async ({ page }, ti) => {
    await page.goto('/#/fleet/sources');
    await expect(page.getByTestId('source-tally')).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  // ── Light theme: BOTH themes must clear AA contrast ──────────────────────
  // The badge/score/accent-heavy states are the highest contrast risk in light mode.
  test('LIGHT: Operational Overview', async ({ page }, ti) => {
    await lightTheme(page);
    await page.goto('/#/fleet');
    await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('LIGHT: Attention (severity + category badges)', async ({ page }, ti) => {
    await lightTheme(page);
    await page.goto('/#/fleet/attention');
    await expect(page.getByRole('heading', { level: 1, name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('LIGHT: Services list (status badges + links)', async ({ page }, ti) => {
    await lightTheme(page);
    await page.goto('/#/fleet/services');
    await expect(page.getByRole('heading', { level: 1, name: 'Services' })).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('LIGHT: Data source detail (degraded) + inventory tally', async ({ page }, ti) => {
    await lightTheme(page);
    await page.goto('/#/fleet/sources/edge-cluster');
    await expect(page.getByRole('heading', { level: 2, name: 'Reported failure' })).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
    await page.goto('/#/fleet/sources');
    await expect(page.getByTestId('source-tally')).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('LIGHT: Focused visual graph (active segmented controls + legend)', async ({ page }, ti) => {
    await lightTheme(page);
    await page.goto('/#/fleet/graph/service/payments-service');
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });
});
