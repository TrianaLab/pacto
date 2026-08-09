import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

// Automated WCAG A/AA accessibility gate (requirement 8.9) over representative REAL
// product states of the built WASM demo. It is wired into the blocking dashboard browser
// CI (playwright, desktop project). No rule is blanket-disabled EXCEPT color-contrast:
// requirement 8.8 says not to claim formal contrast conformance without measurement, so
// contrast is audited visually/by design tokens rather than asserted by this automated
// gate. Every other WCAG 2.0/2.1 A and AA rule is enforced.
const TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'];

async function audit(page: Page, testInfo: { title: string }) {
  const results = await new AxeBuilder({ page })
    .withTags(TAGS)
    .disableRules(['color-contrast']) // measured separately; see requirement 8.8
    .analyze();
  expect(results.violations, `${testInfo.title}: ${JSON.stringify(results.violations.map((v) => v.id))}`).toEqual([]);
}

async function ready(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: 20_000 });
}

test.describe('WCAG A/AA axe gate over product states', () => {
  test('Operational Overview', async ({ page }, ti) => {
    await ready(page);
    await page.goto('/#/fleet');
    await expect(page.getByText('Needs attention')).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Services list', async ({ page }, ti) => {
    await page.goto('/#/fleet/services');
    await expect(page.getByRole('heading', { level: 1, name: 'Services' })).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Service detail', async ({ page }, ti) => {
    await page.goto('/#/fleet/services/payments-service');
    await expect(page.getByTestId('graph-legend').or(page.locator('.ev-head'))).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Attention', async ({ page }, ti) => {
    await page.goto('/#/fleet/attention');
    await expect(page.getByRole('heading', { level: 1, name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
    await audit(page, ti);
  });

  test('Product Impact', async ({ page }, ti) => {
    await page.goto('/#/fleet/impact/payments-service');
    await expect(page.getByRole('heading', { level: 1, name: 'Impact' })).toBeVisible({ timeout: 20_000 });
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
});
