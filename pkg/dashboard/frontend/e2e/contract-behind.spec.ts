import { test, expect } from '@playwright/test';
import { installScaleFixture } from './layoutGate';

// The demo's api-gateway bundles declare pactoVersion 2.0, older than the version this
// Pacto knows. Against the built WASM demo: the revision page's Pacto version fact
// carries a "behind" badge whose title names the declared version.

const T = 20_000;

async function openRevision(page: import('@playwright/test').Page) {
  await page.goto('/#/fleet/revisions?service=api-gateway');
  const link = page.locator('main a[href*="/fleet/revisions/"]').first();
  await expect(link).toBeVisible({ timeout: T });
  await page.goto(`/${await link.getAttribute('href')}`);
}

test('a revision with an older pactoVersion reads as behind', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await openRevision(page);
  const fact = page.locator('.re-fact').filter({ hasText: 'Pacto version' });
  await expect(fact).toBeVisible({ timeout: T });
  const badge = fact.locator('.tag');
  await expect(badge).toHaveText(/^behind \d+\.\d+$/);
  await expect(badge).toHaveAttribute('title', /declares pactoVersion 2\.0\./);
  expect(errors).toEqual([]);
});

// No demo bundle was published by pacto ci, so the scale fixture's revision override
// (installScaleFixture) carries the migration when the __migrated flag is set.
test('a migrated revision reads as inferred', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.addInitScript(installScaleFixture);
  await page.addInitScript(() => { (window as unknown as { __migrated?: string }).__migrated = 'plain'; });
  await openRevision(page);
  await page.reload();
  const fact = page.locator('.re-fact').filter({ hasText: 'Pacto version' });
  await expect(fact).toContainText('migrated from 2.4', { timeout: T });
  await expect(fact.locator('.tag')).toHaveCount(0);
  const mark = fact.getByRole('button', { name: 'inferred pactoVersion migration' });
  await mark.click();
  await expect(fact.locator('.prov-tip')).toBeVisible();
  await expect(fact.locator('.prov-tip')).toContainText('pacto.inferred.yaml');
  expect(errors).toEqual([]);
});
