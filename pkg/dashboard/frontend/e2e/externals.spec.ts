import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// External systems against the built WASM demo, at 1280px with the wide fallback font CI
// renders with: the Overview services line, the list and its class filter, an external
// system's page with its consumers and provenance, and the operational graph focused on
// one. carrier-gateway declares parcel-carrier as a ref-less external; the default
// catalog has no entry for it, so it is joined by name only.

const T = 20_000;
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';
const noSideScroll = (page: Page) => page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1));

async function audit(page: Page, what: string) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

test.describe('WASM demo — external systems', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('the Overview splits the services line and links the external systems', async ({ page }) => {
    await page.goto('/#/fleet');
    await page.addStyleTag({ content: WIDE_FONT });
    const line = page.getByTestId('overview-services-line');
    await expect(line).toBeVisible({ timeout: T });
    await expect(line).toContainText(/\d+ declared, \d+ external/);
    await line.getByTestId('overview-externals').click();
    await expect(page).toHaveURL(/#\/fleet\/externals$/);
    await expect(page.getByRole('heading', { level: 1, name: 'External systems' })).toBeVisible({ timeout: T });
    // Services hosts the list: it is marked beneath that item, and no item is added.
    const nav = page.getByRole('navigation', { name: 'Primary' });
    await expect(nav.getByRole('link', { name: 'Services', exact: true })).toHaveAttribute('aria-current', 'true');
    await expect(nav.getByRole('link', { name: /External/ })).toHaveCount(0);
  });

  test('the list names parcel-carrier, joined by name only, and the class filter narrows it', async ({ page }) => {
    await page.goto('/#/fleet/externals');
    await page.addStyleTag({ content: WIDE_FONT });
    const list = page.getByTestId('external-list');
    await expect(list).toBeVisible({ timeout: T });
    const row = list.locator('tbody tr').filter({ has: page.locator('a.entity-link[href*="/fleet/externals/parcel-carrier"]') });
    await expect(row.locator('[data-label="Class"]')).toHaveText('Unclassified');
    await expect(row.locator('[data-label="Joined by"]')).toHaveText('name only');
    await expect(row.locator('[data-label="Consumers"]')).toHaveText('1');
    expect(await noSideScroll(page)).toBe(true);
    await audit(page, 'external list');
    await page.getByTestId('external-class-filter').getByRole('link', { name: 'Vendor API' }).click();
    await expect(page).toHaveURL(/class=vendor-api/);
    await expect(list.locator('a.entity-link[href*="/fleet/externals/parcel-carrier"]')).toHaveCount(0, { timeout: T });
  });

  test('an external system page lists its consumer and how Pacto knows it', async ({ page }) => {
    await page.goto('/#/fleet/externals/parcel-carrier');
    await page.addStyleTag({ content: WIDE_FONT });
    const consumers = page.getByTestId('external-consumers');
    await expect(consumers).toBeVisible({ timeout: T });
    await expect(consumers.locator('tbody tr')).toHaveCount(1);
    await expect(consumers).toContainText('carrier-gateway');
    await expect(consumers).toContainText('carrier-api/v3');
    await expect(page.getByTestId('external-name-only')).toHaveText('Joined by name only: consumers that spell it differently do not join.');
    const sources = page.getByTestId('external-sources');
    await sources.locator('summary').click();
    await expect(sources.locator('li').first()).toContainText('pacto.yaml dependencies[0] (name: parcel-carrier, version: carrier-api/v3), declared');
    await expect(page.getByRole('navigation', { name: 'Breadcrumb' })).toContainText('External systems');
    expect(await noSideScroll(page)).toBe(true);
    await audit(page, 'external entity');
  });

  test('the graph focused on an external system draws it with its consumers', async ({ page }) => {
    await page.goto('/#/fleet/graph/external/parcel-carrier');
    await page.addStyleTag({ content: WIDE_FONT });
    await expect(page.getByTestId('graph-legend')).toContainText('External system', { timeout: T });
    const textalt = page.getByTestId('graph-textalt');
    await textalt.locator('summary').click();
    await expect(textalt.getByRole('button', { name: /carrier-gateway/ }).first()).toBeVisible();
    await expect(textalt.getByRole('button', { name: /parcel-carrier/ }).first()).toBeVisible();
    await expect(page.locator('main')).toContainText('parcel-carrier is an external system');
    expect(await noSideScroll(page)).toBe(true);
  });
});
