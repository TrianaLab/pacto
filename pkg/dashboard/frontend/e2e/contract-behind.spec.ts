import { test, expect } from '@playwright/test';

// The demo's api-gateway bundles declare pactoVersion 2.0, older than the version this
// Pacto knows. Against the built WASM demo: the revision page's Pacto version fact
// carries a "behind" badge whose title names the declared version.

const T = 20_000;

test('a revision with an older pactoVersion reads as behind', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto('/#/fleet/revisions?service=api-gateway');
  const link = page.locator('main a[href*="/fleet/revisions/"]').first();
  await expect(link).toBeVisible({ timeout: T });
  await page.goto(`/${await link.getAttribute('href')}`);
  const fact = page.locator('.re-fact').filter({ hasText: 'Pacto version' });
  await expect(fact).toBeVisible({ timeout: T });
  const badge = fact.locator('.tag');
  await expect(badge).toHaveText(/^behind \d+\.\d+$/);
  await expect(badge).toHaveAttribute('title', /declares pactoVersion 2\.0\./);
  expect(errors).toEqual([]);
});
