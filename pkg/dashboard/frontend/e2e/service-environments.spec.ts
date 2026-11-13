import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// The Environments section of a service page (#467) over the browser demo:
// storefront-web 1.3.0 declares production, staging, preview (temporary, expired
// against the demo clock) and pr-482, and a canary target runs it undeclared.
// The test opens its own page, so the file passes alone in any shard.

const T = 30_000;

async function audit(page: Page, what: string) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

test('a service page shows each environment with what is declared, observed and inferred', async ({ page }) => {
  await page.goto('/#/fleet/services/storefront-web');
  const section = page.locator('#sec-environments');
  const env = (name: string) => section.locator(`[data-testid="service-environment"][data-env="${name}"]`);
  await expect(env('production')).toBeVisible({ timeout: T });
  for (const name of ['staging', 'preview', 'pr-482', 'canary']) await expect(env(name)).toBeVisible();

  await expect(env('preview').locator('[data-cell="declared"]')).toContainText('Expired (inferred');
  await expect(env('canary').locator('[data-cell="declared"]')).toContainText('Not declared by');
  await expect(env('staging').locator('[data-cell="declared"]')).toContainText('Owner storefront-qa (declared)');
  await expect(env('production').locator('[data-cell="declared"]')).toContainText('(service owner, inferred)');

  await expect(section.getByTestId('environment-differences').getByTestId('difference-row').filter({ hasText: 'pr-482' }).first()).toBeVisible();
  const history = section.getByTestId('environment-history');
  // 1.0.0 declares no environments, so 1.1.0 adds them; nothing reads declared since at least here.
  await expect(history.getByTestId('environment-event').filter({ hasText: 'staging added in' }).getByRole('link', { name: '1.1.0' })).toBeVisible();
  // 1.3.0 adds pr-482 and gives staging a lifetime and an owner; 1.3.1 retires pr-482.
  await expect(history.getByTestId('environment-event').filter({ hasText: 'pr-482 added in' }).getByRole('link', { name: '1.3.0' })).toBeVisible();
  await expect(history.getByTestId('environment-event').filter({ hasText: 'staging lifetime and owner changed in' }).getByRole('link', { name: '1.3.0' })).toBeVisible();
  await expect(history.getByTestId('environment-event').filter({ hasText: 'pr-482 removed in' }).getByRole('link', { name: '1.3.1' })).toBeVisible();
  await expect(history.getByTestId('environment-event').first()).toContainText('time not recorded');
  await audit(page, 'service environments');
});

test('a revision page reads an expired environment as inferred', async ({ page }) => {
  await page.goto('/#/fleet/revisions?service=storefront-web');
  const a = page.locator('main a[href*="/fleet/revisions/"]').filter({ hasText: /\b1\.3\.0\b/ }).first();
  await expect(a).toBeVisible({ timeout: T });
  await page.goto(`/${await a.getAttribute('href')}`);
  // The section starts closed when every value set passed: open it either way.
  const envs = page.locator('details').filter({ has: page.locator('summary.ps-head', { hasText: 'Environments' }) }).first();
  await expect(envs).toBeAttached({ timeout: 30_000 });
  await envs.evaluate((d) => { (d as HTMLDetailsElement).open = true; });
  const preview = page.getByTestId('environment').filter({ hasText: 'preview' });
  await expect(preview.getByTestId('environment-declared')).toContainText('Expired (inferred, as of', { timeout: T });
  await audit(page, 'revision entity with environments');
});
