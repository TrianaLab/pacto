import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// What changed in a service, against the built WASM demo at 1280px with the wide
// fallback font CI renders with: an owner handover reads as a change (payments-service
// 2.1.0), a real dispute keeps its banner (orders-service, a staging target still on
// 1.1.0), the story's payments service carries reached times from signed records, and
// Delivery history names revisions, recorded impact and what each Stage ran at a time.

const T = 30_000;
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';
const STORY = '#/fleet/services/registry.example%252Fstory%2Fpayments';
const noSideScroll = (page: Page) => page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1));

async function open(page: Page, hash: string) {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(`/${hash}`);
  await page.addStyleTag({ content: WIDE_FONT });
}

async function axeClean(page: Page) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) }))).toEqual([]);
}

test.describe('WASM demo — what changed in a service', () => {
  test('payments-service 2.1.0 is an owner change and its page shows no dispute', async ({ page }) => {
    await open(page, '#/fleet/services/payments-service?section=changes');
    const changes = page.getByTestId('service-changes');
    const owner = changes.getByTestId('service-change-row').filter({ hasText: 'owner' }).filter({ hasText: '2.1.0' });
    await expect(owner.first()).toContainText('team:team/payments', { timeout: T });
    await expect(owner.first()).toContainText('team:payments-team');
    await expect(page.getByText('Ownership conflict.')).toHaveCount(0);
    expect(await noSideScroll(page)).toBe(true);
    await axeClean(page);
  });

  test('the kind filter narrows the table and its count matches the rows', async ({ page }) => {
    await open(page, '#/fleet/services/payments-service?section=changes');
    const changes = page.getByTestId('service-changes');
    const chip = changes.locator('[data-testid="service-changes-kind"][data-kind="owner"]');
    await expect(chip).toBeVisible({ timeout: T });
    const n = Number((await chip.innerText()).replace(/\D+/g, ''));
    await chip.click();
    await expect(page).toHaveURL(/changesKind=owner/);
    await expect(changes.getByTestId('service-changes-filtered')).toHaveText(`${n} change${n === 1 ? '' : 's'} of kind owner`, { timeout: T });
    const rows = changes.getByTestId('service-change-row');
    await expect(rows).toHaveCount(n);
    expect(await rows.evaluateAll((rs) => rs.every((r) => r.getAttribute('data-kind') === 'owner'))).toBe(true);
  });

  test('the owner ranking counts payments-service under payments-team', async ({ page }) => {
    await open(page, '#/fleet/owners');
    const aggregate = page.getByTestId('owners-aggregate');
    await expect(aggregate.locator('.dist-legend')).toBeVisible({ timeout: T });
    await aggregate.getByText('Per-owner breakdown').click();
    const ranking = aggregate.locator('.hbars').filter({ hasText: 'Services per owner' });
    await ranking.locator('a.hb-inner').filter({ has: page.getByText('payments-team', { exact: true }) }).first().click({ timeout: T });
    await expect(page).toHaveURL(/ownerKey=team%3Apayments-team/, { timeout: T });
    await expect(page.getByTestId('service-list')).toContainText('payments-service', { timeout: T });
  });

  test('orders-service keeps its conflict and says the link is inferred', async ({ page }) => {
    await open(page, '#/fleet/services/orders-service');
    const banner = page.locator('.se-conflicts');
    await expect(banner).toContainText('Ownership conflict.', { timeout: T });
    await expect(banner).toContainText('linked by tag (inferred) to revisions that declare different owners');
    await axeClean(page);
  });

  test('the story payments service says when its revisions reached a Stage', async ({ page }) => {
    await open(page, `${STORY}?section=changes`);
    const changes = page.getByTestId('service-changes');
    const reached = changes.getByTestId('service-change-reached').filter({ has: page.locator('a[href*="#/delivery/history"]') });
    await expect(reached.first()).toBeVisible({ timeout: T });
    await expect(changes.getByTestId('service-changes-caption')).toContainText('times from delivery records');
    expect(await noSideScroll(page)).toBe(true);
    await axeClean(page);

    const analyse = changes.getByRole('link', { name: /^Analyse against today's graph: / }).first();
    await analyse.click();
    await expect(page).toHaveURL(/#\/fleet\/changes\//, { timeout: T });
    await expect(page.getByTestId('changes-what-changed')).toBeVisible({ timeout: T });
  });

  test('Delivery history names the revision and the recorded impact of a promotion', async ({ page }) => {
    await open(page, '#/delivery/history?namespace=test&stage=payments');
    const impact = page.getByTestId('history-impact').first();
    await expect(impact).toContainText('Impact recorded', { timeout: T });
    await expect(page.locator('a[href*="#/fleet/revisions/"]').first()).toBeVisible();
    await expect(page.getByTestId('history-analyse').first()).toHaveText("Analyse against today's graph");
    expect(await noSideScroll(page)).toBe(true);
    await axeClean(page);
  });

  test('As of answers what each Stage ran, known where a signed record holds and unknown before it', async ({ page }) => {
    await open(page, '#/delivery/history?at=1m');
    const rows = page.getByTestId('history-at-row');
    await expect(rows.first()).toBeVisible({ timeout: T });
    await expect(rows.filter({ has: page.getByTestId('history-at-state').getByText('known', { exact: true }) }).first()).toBeVisible();
    await expect(rows.filter({ has: page.getByTestId('history-at-state').getByText('unknown', { exact: true }) }).first()).toContainText('no record before this time');
    await expect(page.getByTestId('history-at-caption')).toContainText('As of');
    expect(await noSideScroll(page)).toBe(true);
    await axeClean(page);
  });
});
