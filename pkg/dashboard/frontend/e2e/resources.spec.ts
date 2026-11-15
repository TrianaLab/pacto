import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { appReady, settle } from './ready';

// The service page's Resources section against the built WASM demo. The in-browser demo
// dates provider reports 30 minutes before it loads, so this is the only place that sees
// notification-worker's email-provider resource Failed and raising PROVISIONING_FAILED;
// the CLI demo reads the same report stale (examples/demo/demo_lock_test.go).

const T = 30_000;
const TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'];
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';

async function openResources(page: Page, service: string) {
  await appReady(page);
  await page.goto(`/#/fleet/services/${service}`);
  await expect(page.getByRole('heading', { level: 1, name: /^Service: / })).toBeVisible({ timeout: T });
  const list = page.getByTestId('service-resources');
  await expect(list).toBeVisible({ timeout: T });
  return list;
}

async function expectAxeClean(page: Page) {
  await settle(page);
  const res = await new AxeBuilder({ page }).include('#sec-resources').withTags(TAGS).analyze();
  expect(res.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual([]);
}

test.describe('WASM demo — resources a service consumes', () => {
  test('catalog-service: the declared request, its Ready phase with provenance and signed usage', async ({ page }) => {
    const list = await openResources(page, 'catalog-service');
    const card = list.getByTestId('service-resource').filter({ has: page.getByTestId('resource-requested') });
    await expect(card).toHaveCount(1);
    await expect(card.locator('a.entity-link[href$="/fleet/services/postgresql"]').first()).toBeVisible();
    await expect(card.locator('a.rl-chip', { hasText: /^postgresql$/ })).toHaveAttribute('href', /\/fleet\/offerings\/postgresql$/);
    await expect(card).toContainText('infra-data');
    const requested = card.getByTestId('resource-requested');
    await expect(requested).toContainText('Requested (declared)');
    await expect(requested.locator('dt', { hasText: /^size$/ })).toBeVisible();
    await expect(requested.locator('dt', { hasText: /^storageGi$/ })).toBeVisible();
    const prod = card.getByTestId('resource-status').filter({ hasText: 'production' });
    await expect(prod).toContainText('Ready');
    await expect(prod).toContainText('unsigned');
    // The WASM demo stands in for the Evidence Server: its usage report is signed and fresh.
    const usage = card.getByTestId('resource-usage-entry').filter({ hasText: 'production' });
    await expect(usage).toHaveAttribute('data-state', 'reported');
    await expect(usage).toContainText('Reported (observed)');
    await expect(usage.locator('dt', { hasText: /^storage-used$/ })).toBeVisible();
    await expect(usage.locator('dd', { hasText: /^31 Gi$/ })).toBeVisible();
    await expect(usage.locator('dd', { hasText: /^212\.40 USD per P30D$/ })).toBeVisible();
    await expect(usage).toContainText('reported by platform-adapter, signed');

    // The rail opens from the badge and closes again.
    const badge = prod.locator('button[data-testid="provisioning-badge"]');
    await badge.click();
    await expect(card.getByTestId('offering-use-figure')).toBeVisible({ timeout: T });
    await expect(badge).toHaveAttribute('aria-pressed', 'true');
    await badge.click();
    await expect(card.getByTestId('offering-use-figure')).toHaveCount(0);

    await expectAxeClean(page);
  });

  test('orders-service: usage not reported', async ({ page }) => {
    const list = await openResources(page, 'orders-service');
    const usage = list.getByTestId('resource-usage').first();
    await expect(usage).toContainText('Usage');
    await expect(usage).toContainText('Not reported');
    await expect(list.getByTestId('resource-usage-entry')).toHaveCount(0);
  });

  test('notification-worker: a resource with no offering that failed, and its attention item', async ({ page }) => {
    const list = await openResources(page, 'notification-worker');
    const card = list.getByTestId('service-resource').filter({ has: page.locator('a.entity-link[href$="/fleet/services/email-provider"]') });
    await expect(card).toHaveCount(1);
    await expect(card).toContainText('no offering');
    await expect(card.getByTestId('resource-status').first()).toContainText('Failed');
    await expect(card.getByTestId('resource-status').first()).toContainText('adapter mail-adapter');
    // Its signed usage report is two days old: unknown, the figures apart and never in the ok tone.
    const usage = card.getByTestId('resource-usage-entry');
    await expect(usage).toHaveAttribute('data-state', 'unknown');
    await expect(usage).toContainText('Unknown: the last usage report is older than the provisioning window');
    await expect(usage).toContainText('reported, not trusted');
    await expect(usage.locator('.rl-figures.rl-untrusted dd', { hasText: /^48\.00 USD per P30D$/ })).toBeVisible();
    await expect(usage.locator('.rl-figures:not(.rl-untrusted)')).toHaveCount(0);
    await expect(usage.locator('.tone-ok')).toHaveCount(0);
    await expect(usage).toContainText('reported by mail-adapter, signed');
    await expectAxeClean(page);

    await page.goto('/#/fleet/attention?category=provisioning');
    await expect(page.getByRole('heading', { level: 1, name: 'Needs attention' })).toBeVisible({ timeout: T });
    const item = page.getByTestId('attention-list').locator('li.attn-item').filter({ has: page.locator('a.entity-link[href$="/fleet/services/notification-worker"]') });
    await expect(item.first()).toBeVisible({ timeout: T });
    await expect(item.first()).toContainText('Resource provisioning');
    await item.first().locator('a.entity-link[href$="/fleet/services/notification-worker"]').click();
    await expect(page).toHaveURL(/#\/fleet\/services\/notification-worker$/, { timeout: T });
  });

  test('a wide font keeps every resource card inside the page', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    const list = await openResources(page, 'catalog-service');
    await page.addStyleTag({ content: WIDE_FONT });
    await expect(list.getByTestId('service-resource').first()).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(1280);
    const over = await list.locator('.rl-card').evaluateAll((els) =>
      els.filter((el) => el.scrollWidth > el.clientWidth + 1).map((el) => el.textContent?.trim().slice(0, 80) ?? ''));
    expect(over).toEqual([]);
  });
});
