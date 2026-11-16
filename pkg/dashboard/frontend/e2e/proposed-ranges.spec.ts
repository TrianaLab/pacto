import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';
import { ROUTES, installScaleFixture, loaded } from './layoutGate';

// compatibility: unknown (pactoVersion 2.5) is what pacto init writes for a range it only
// proposes. On the scale fixture, which gives such a dependency to the routes below, every
// page names it "Range not declared" and none reads it as a range a consumer accepts.

async function open(page: Page, name: string) {
  const r = ROUTES.find((x) => x.name === name)!;
  await appReady(page);
  if (r.open) await r.open(page);
  else await page.goto(`/${r.hash}`);
  await loaded(page);
}

async function neverAccepted(page: Page) {
  await expect(page.locator('main')).not.toContainText(/accepts\s+unknown/i);
  expect(await page.locator('main [title="Accepts unknown"], main [title="Range unknown"]').count()).toBe(0);
}

test.describe('a dependency with no declared range', () => {
  test.beforeEach(async ({ page }) => { await page.addInitScript(installScaleFixture); });

  test('change analysis names the consumer\'s range not declared', async ({ page }) => {
    test.setTimeout(120_000);
    await open(page, 'change analysis answered');
    await expect(page.getByTestId('consumer-range-undeclared').first()).toHaveText('Range not declared');
    await neverAccepted(page);
  });

  test('the service page names its claim range not declared', async ({ page }) => {
    await open(page, 'service entity');
    await expect(page.getByTestId('service-relationships').getByText('Range not declared').first()).toBeVisible();
    await neverAccepted(page);
  });

  test('the offering page titles the consumer range not declared', async ({ page }) => {
    await open(page, 'offering entity');
    await expect(page.locator('[data-testid="offering-consumer"] [title="Range not declared"]').first()).toBeAttached();
    await neverAccepted(page);
  });
});
