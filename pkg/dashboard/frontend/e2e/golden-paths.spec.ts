import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// Golden paths against the built WASM demo, at 1280px with the wide fallback font CI
// renders with: the tab, the http-service golden path page and gift-cards, the
// service created from it.

const T = 20_000;
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';
const noSideScroll = (page: Page) => page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1));

async function axeClean(page: Page) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) }))).toEqual([]);
}

test.describe('WASM demo — golden paths', () => {
  test('the Golden paths item opens the list and the page never scrolls sideways', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet');
    await page.addStyleTag({ content: WIDE_FONT });
    const tab = page.getByRole('navigation', { name: 'Primary' }).getByRole('link', { name: 'Golden paths' });
    await tab.scrollIntoViewIfNeeded();
    await tab.click();
    await expect(page).toHaveURL(/#\/fleet\/golden-paths$/);
    await expect(page.getByRole('heading', { level: 1, name: 'Golden paths' })).toBeVisible({ timeout: T });
    await expect(tab).toHaveAttribute('aria-current', 'page');
    await expect(page.getByRole('button', { name: 'Toggle theme' })).toBeInViewport();
    expect(await noSideScroll(page)).toBe(true);
  });

  test('the tab lists http-service and its page shows the plugins, the command and gift-cards', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/golden-paths');
    await page.addStyleTag({ content: WIDE_FONT });
    const list = page.getByTestId('golden-path-list');
    await expect(list).toBeVisible({ timeout: T });
    const row = list.locator('tbody tr').filter({ hasText: 'http-service' });
    await expect(row).toHaveCount(1);
    await row.locator('a.entity-link').click();
    const gp = page.getByTestId('golden-path');
    await expect(gp).toBeVisible({ timeout: T });
    await expect(gp.getByTestId('golden-path-plugins')).toContainText('dockerfile');
    await expect(gp.getByTestId('golden-path-create')).toContainText('pacto init');
    await expect(page.getByTestId('golden-path-created')).toContainText('gift-cards');
    expect(await noSideScroll(page)).toBe(true);
    await axeClean(page);
  });

  test('gift-cards shows where it was created from and its creation status', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    // Through the path's own "Services created" count, so the filter carries the path's
    // real key rather than one this spec guesses.
    await page.goto('/#/fleet/golden-paths');
    const row = page.getByTestId('golden-path-list').locator('tbody tr').filter({ hasText: 'http-service' });
    await row.getByTestId('path-created').click({ timeout: T });
    await expect(page).toHaveURL(/createdFrom=/);
    await page.getByTestId('service-list').locator('a.entity-link').filter({ hasText: 'gift-cards' }).click();
    const origin = page.getByTestId('service-origin');
    await expect(origin).toContainText('Created from', { timeout: T });
    await expect(origin).toContainText('declared');
    // The deployed step says what the engine established, read from the engine rather
    // than guessed: the demo runs gift-cards in prod, so it reads its target count.
    const step = await page.evaluate(async () => ((await (await fetch('/api/fleet/entities/service?key=gift-cards')).json())
      .service.creation as Array<{ step: string; status: string }>).find((c) => c.step === 'deployed'));
    const deployed = page.getByTestId('creation-status').locator('li[data-step="deployed"]');
    await expect(deployed).toContainText(step?.status === 'done' ? 'Done' : 'Unknown');
    await axeClean(page);
  });

  // 515-S7: the demo's Backstage catalog holds springboot-service, an inferred golden
  // path, and http-service, which the declared http-service golden path holds the name
  // of (TestDemoInferredTemplates).
  const CATALOG = 'https://backstage.acme.example/create/templates/default/';

  test('an inferred golden path is muted and opens in Backstage', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/golden-paths');
    const row = page.getByTestId('inferred-golden-path').filter({ hasText: 'springboot-service' });
    await expect(row).toHaveCount(1, { timeout: T });
    await expect(row).toHaveClass(/gp-inferred/);
    await expect(row).toContainText('Not declared');
    await expect(row).toContainText('A Spring Boot HTTP service with a PostgreSQL database and the platform CI');
    const open = row.getByRole('link', { name: 'Open in Backstage' });
    await expect(open).toHaveAttribute('href', `${CATALOG}springboot-service`);
    await expect(open).toHaveAttribute('target', '_blank');
    await expect(open).toHaveAttribute('rel', 'noopener noreferrer');
    await row.getByRole('button', { name: 'Inferred' }).click();
    const tip = row.locator('.help-body');
    await expect(tip).toContainText('Inferred from Backstage (backstage.acme.example), template:default/springboot-service');
    await expect(tip).toContainText('never offered for creation until a person accepts it');
    // The template http-service folds into the declared path: never an inferred row.
    await expect(page.getByTestId('inferred-golden-path').filter({ hasText: 'http-service' })).toHaveCount(0);
    expect(await noSideScroll(page)).toBe(true);
    await axeClean(page);
  });

  test('the inferred filter keeps only inferred paths', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/golden-paths');
    const seg = page.getByTestId('golden-path-declaration');
    await seg.getByRole('button', { name: /^Inferred/ }).click({ timeout: T });
    await expect(page).toHaveURL(/declaration=inferred/);
    const rows = page.getByTestId('golden-path-list').locator('tbody tr');
    await expect(rows).toHaveCount(1, { timeout: T });
    await expect(rows.first()).toHaveClass(/gp-inferred/);
    await expect(rows.first()).toContainText('springboot-service');
    await seg.getByRole('button', { name: 'Declared' }).click();
    await expect(page).toHaveURL(/declaration=declared/);
    await expect(page.getByTestId('inferred-golden-path')).toHaveCount(0, { timeout: T });
    await expect(rows.filter({ hasText: 'http-service' })).toHaveCount(1);
  });

  test('Accept as golden path opens the plan and saves a draft in the tab', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto('/#/fleet/golden-paths');
    const row = page.getByTestId('inferred-golden-path').filter({ hasText: 'springboot-service' });
    await row.getByTestId('inferred-accept').click({ timeout: T });
    const block = page.getByTestId('golden-path-accept');
    await expect(block.getByRole('heading', { name: 'Accept springboot-service as a golden path' })).toBeFocused();
    const panel = block.getByTestId('declare');
    await expect(panel.getByTestId('declare-command')).toContainText('pacto declare template:default/springboot-service', { timeout: T });
    await expect(panel.getByTestId('declare-signer')).toContainText('the demo has no sign-in');
    await panel.getByTestId('declare-save-draft').click();
    await expect(panel.getByTestId('declare-result')).toContainText('Saved draft-1', { timeout: T });
    await expect(panel.getByTestId('declare-result')).toContainText('update pactos in the installation namespace');
    await expect(panel.getByRole('button', { name: /^Publish/ })).toHaveCount(0);
  });

  test('the declared http-service path opens in Backstage', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/golden-paths');
    const row = page.getByTestId('golden-path-list').locator('tbody tr').filter({ has: page.locator('a.entity-link', { hasText: 'http-service' }) });
    await expect(row).toHaveCount(1, { timeout: T });
    await expect(row).not.toHaveClass(/gp-inferred/);
    await expect(row.getByTestId('path-open-catalog')).toHaveAttribute('href', `${CATALOG}http-service`);
    await row.getByRole('button', { name: 'Inferred link' }).click();
    await expect(row.locator('.help-body')).toContainText('Linked to template:default/http-service in Backstage (backstage.acme.example) by name');
  });
});
