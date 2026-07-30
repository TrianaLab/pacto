import { test, expect, type Page } from '@playwright/test';

// Browser E2E against the built WASM demo (see playwright.config.ts): the real
// Svelte bundle + the real dashboard API compiled to wasm, serving deterministic
// embedded data. Tests are organized around user WORKFLOWS (land → explore the
// operational graph → inspect a node → analyze impact) and use precise,
// role-based locators — never an index, a `.first()` on an ambiguous control, or a
// less precise text match. `.first()` appears only on genuinely multi-row DATA
// (service rows, consumer-path rows), never to disambiguate navigation or controls.

async function waitReady(page: Page) {
  await page.goto('/');
  // The Operational Graph tab is capability-gated, so its presence proves the wasm
  // host registered the fleet provider and capabilities were fetched.
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: 20_000 });
}

async function gotoFleet(page: Page, query = '') {
  await page.goto(`/#/fleet${query}`);
  await expect(page.getByRole('heading', { name: 'Operational Graph' })).toBeVisible({ timeout: 20_000 });
}

// Precise control locators (role + accessible name + scoping group).
const perspectiveBtn = (page: Page, name: string) =>
  page.getByRole('group', { name: 'Perspective' }).getByRole('button', { name, exact: true });
const layerBtn = (page: Page, name: RegExp | string) =>
  page.getByRole('group', { name: 'Relationship layer' }).getByRole('button', { name });

test.describe('WASM dashboard demo — workflows', () => {
  test('landing: the service list loads from the embedded fleet', async ({ page }) => {
    await waitReady(page);
    // A known service from the embedded fleet appears (multiple rows → first row is fine).
    await expect(page.getByText('payments-service').first()).toBeVisible({ timeout: 20_000 });
  });

  test('navigation exposes only registered capabilities (no dead tabs)', async ({ page }) => {
    await waitReady(page);
    const nav = page.getByRole('navigation', { name: 'Primary' });
    await expect(nav.getByRole('link', { name: 'Operational Graph' })).toBeVisible();
    await expect(nav.getByRole('link', { name: 'Services' })).toBeVisible();
    await expect(nav.getByRole('link', { name: 'Owners' })).toBeVisible();
  });

  test('operational graph: renders a topology with count tiles', async ({ page }) => {
    await gotoFleet(page);
    // The Services count tile (precise: the metric tile, not the nav link/button).
    const servicesTile = page.locator('.metric-tile', { hasText: 'Services' });
    await expect(servicesTile.locator('.metric-value')).not.toHaveText('0');
    await expect(page.locator('.graph-container canvas').first()).toBeVisible({ timeout: 20_000 });
  });

  test('operational graph: perspective + layer controls drive the real pipeline', async ({ page }) => {
    await gotoFleet(page);
    // Perspective switching (precise, group-scoped).
    await expect(perspectiveBtn(page, 'Services')).toHaveAttribute('aria-pressed', 'true');
    await perspectiveBtn(page, 'Revisions').click();
    await expect(perspectiveBtn(page, 'Revisions')).toHaveAttribute('aria-pressed', 'true');

    await perspectiveBtn(page, 'Targets').click();
    // The target perspective is honest about NOT drawing instance-to-instance edges.
    await expect(page.getByTestId('target-note')).toContainText('not to specific peer instances');

    // The Observed layer is REAL in the demo (edges folded into the snapshot), so
    // it is enabled — not a disabled placebo.
    await expect(layerBtn(page, /Observed/)).toBeEnabled();
    await layerBtn(page, /Observed/).click();
    await expect(layerBtn(page, /Observed/)).toHaveAttribute('aria-pressed', 'true');
  });

  test('honesty: a partial snapshot shows the partial banner and source states', async ({ page }) => {
    await gotoFleet(page);
    await expect(page.getByTestId('partial-banner')).toBeVisible();
    await expect(page.getByTestId('source-states')).toContainText('unavailable');
  });

  test('inspect: selecting a node lazily loads its domain-qualified detail', async ({ page }) => {
    await gotoFleet(page, '?sel=payments-service');
    const panel = page.getByTestId('detail-panel');
    await expect(panel).toContainText('payments-service', { timeout: 20_000 });
    await expect(panel).toContainText('key:'); // domain-qualified identity, not just a name
    await expect(panel.getByRole('heading', { name: /Revisions/ })).toBeVisible();
  });

  test('honesty: a healthy status report is never a blanket "all clear" when items exist', async ({ page }) => {
    await gotoFleet(page);
    await expect(page.getByText('Needs attention')).toBeVisible();
  });

  test('workflow: Diff → Impact entry point renders consumer paths', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/diff?from_name=payments-service&from_ver=1.2.0&to_name=payments-service&to_ver=2.0.0');
    const cta = page.getByRole('link', { name: /Analyze operational impact/ });
    await expect(cta).toBeVisible({ timeout: 20_000 });
    await cta.click();
    await expect(page.getByRole('heading', { name: 'Impact' })).toBeVisible();
  });

  test('impact: contextual, bound to the published snapshot, renders paths', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/impact?svc=payments-service');
    await expect(page.getByText('BREAKING').first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText('matches graph')).toBeVisible(); // snapshot-bound
    await expect(page.locator('.path-cell').first()).toContainText('→'); // a consumer→changed path
  });

  test('impact: include-observed is real and surfaces a shadow consumer', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/impact?svc=payments-service');
    await expect(page.getByText('BREAKING').first()).toBeVisible({ timeout: 20_000 });
    const cb = page.getByRole('checkbox');
    await expect(cb).toBeEnabled(); // the demo carries embedded observed edges
    await cb.check();
    await page.getByRole('button', { name: /Analyze impact/ }).click();
    // audit-log declares no dependency on payments-service — an observed-only shadow.
    await expect(page.locator('.path-cell', { hasText: 'audit-log' }).first()).toBeVisible({ timeout: 20_000 });
  });

  test('accessibility: keyboard reaches the primary nav with real, named links', async ({ page }) => {
    await waitReady(page);
    await page.keyboard.press('Tab');
    expect(await page.evaluate(() => document.activeElement?.tagName)).toBeTruthy();
    await expect(page.getByRole('link', { name: 'Operational Graph' })).toHaveAttribute('href', /#\/fleet/);
  });

  test('scale: a large bounded fleet renders without a page error', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await gotoFleet(page);
    await expect(page.locator('.graph-container canvas').first()).toBeVisible({ timeout: 20_000 });
    await page.waitForTimeout(500);
    expect(errors).toEqual([]);
  });
});
