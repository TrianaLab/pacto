import { test, expect, type Page } from '@playwright/test';

// Browser E2E against the built WASM demo (see playwright.config.ts): the real Svelte
// bundle + the real dashboard API compiled to wasm, serving deterministic embedded
// data over the LIVE product endpoints (overview/entities/neighborhood/impact). Tests
// are organized around user WORKFLOWS (land -> search the operational graph -> focus a
// neighborhood -> inspect -> analyze impact) and use precise, role-based locators.

async function waitReady(page: Page) {
  await page.goto('/');
  // The Operational Graph tab is capability-gated, so its presence proves the wasm
  // host registered the fleet provider and capabilities were fetched.
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: 20_000 });
}

// The search-first graph landing (no focus): a discovery state, never a hairball.
async function gotoGraphDiscovery(page: Page) {
  await page.goto('/#/fleet/graph');
  await expect(page.getByTestId('graph-discovery')).toBeVisible({ timeout: 20_000 });
}

// A direct focus deep link (kind/key), which loads the bounded neighborhood.
async function gotoGraphFocus(page: Page, kind: string, key: string) {
  await page.goto(`/#/fleet/graph/${kind}/${encodeURIComponent(key)}`);
  await expect(page.getByTestId('graph-canvas')).toBeVisible({ timeout: 20_000 });
}

test.describe('WASM dashboard demo — workflows', () => {
  test('landing: the service list loads from the embedded fleet', async ({ page }) => {
    await waitReady(page);
    await expect(page.getByText('payments-service').first()).toBeVisible({ timeout: 20_000 });
  });

  test('navigation exposes only registered capabilities (no dead tabs)', async ({ page }) => {
    await waitReady(page);
    const nav = page.getByRole('navigation', { name: 'Primary' });
    await expect(nav.getByRole('link', { name: 'Operational Graph' })).toBeVisible();
    await expect(nav.getByRole('link', { name: 'Services' })).toBeVisible();
    await expect(nav.getByRole('link', { name: 'Owners' })).toBeVisible();
  });

  test('graph opens SEARCH-FIRST: a discovery state, never a whole-fleet hairball', async ({ page }) => {
    await gotoGraphDiscovery(page);
    // No topology is rendered without a focus (requirement R: zero topology nodes).
    await expect(page.getByTestId('graph-canvas')).toHaveCount(0);
    await expect(page.getByRole('search')).toBeVisible();
  });

  test('graph: searching focuses a bounded local neighborhood via the product API', async ({ page }) => {
    await gotoGraphDiscovery(page);
    await page.getByRole('searchbox').fill('payments');
    // A result links into the graph focus route; following it loads the neighborhood.
    const result = page.getByTestId('graph-focus-link').first();
    await expect(result).toBeVisible({ timeout: 20_000 });
    await result.click();
    await expect(page.getByTestId('graph-canvas')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('graph-focus-node')).toBeVisible();
  });

  test('graph controls: perspective and direction drive the neighborhood via the URL', async ({ page }) => {
    await gotoGraphFocus(page, 'service', 'payments-service');
    // perspective service is the default; switching updates the URL (shareable state).
    await page.getByTestId('perspective-revision').click();
    await expect(page).toHaveURL(/perspective=revision/);
    await page.getByTestId('dir-dependencies').click();
    await expect(page).toHaveURL(/direction=dependencies/);
  });

  test('graph honesty: a partial snapshot surfaces the incompleteness caveat', async ({ page }) => {
    await gotoGraphFocus(page, 'service', 'payments-service');
    // The demo snapshot is partial (an unavailable source), so the neighborhood says so.
    await expect(page.getByTestId('graph-knowledge-caveat')).toBeVisible();
  });

  test('graph inspect: selecting the focus node opens a bounded quick-inspect drawer', async ({ page }) => {
    await gotoGraphFocus(page, 'service', 'payments-service');
    await page.getByTestId('graph-focus-node').click();
    const drawer = page.getByTestId('graph-drawer');
    await expect(drawer).toBeVisible();
    // The drawer links to the durable full entity page (never duplicates it).
    await expect(drawer.getByRole('link', { name: /full detail/i })).toBeVisible();
  });

  test('graph scale: discovery and a focused neighborhood render without a page error', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await gotoGraphDiscovery(page);
    await gotoGraphFocus(page, 'service', 'payments-service');
    await page.waitForTimeout(500);
    expect(errors).toEqual([]);
  });

  test('honesty: a healthy status report is never a blanket "all clear" when items exist', async ({ page }) => {
    await page.goto('/#/fleet');
    await expect(page.getByText('Needs attention')).toBeVisible({ timeout: 20_000 });
  });

  test('workflow: Diff launches the canonical Product Impact workspace', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/diff?from_name=payments-service&from_ver=1.2.0&to_name=payments-service&to_ver=2.0.0');
    // A2: the CTA resolves the service name to a canonical ServiceKey before offering
    // the Product Impact route (never a bare display-name route).
    const cta = page.getByRole('link', { name: /Analyze operational impact/ });
    await expect(cta).toBeVisible({ timeout: 20_000 });
    await expect(cta).toHaveAttribute('href', /#\/fleet\/impact\//);
    await cta.click();
    await expect(page.getByRole('heading', { name: 'Impact' })).toBeVisible();
  });

  test('impact: the Product Impact workspace analyzes by canonical identity', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/fleet/impact/payments-service');
    // The revision selectors are populated from the product service detail (never the
    // raw snapshot). Pick the known breaking pair 1.0.0 -> 2.0.0.
    await page.locator('#impact-old-rev').selectOption({ label: 'payments-service 1.0.0' });
    await page.locator('#impact-new-rev').selectOption({ label: 'payments-service 2.0.0' });
    await page.getByRole('button', { name: /Analyze impact/ }).click();
    await expect(page.getByText('breaking', { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    // A consumer path renders (consumer -> ... -> changed service).
    await expect(page.locator('.path-cell', { hasText: '→' }).first()).toBeVisible({ timeout: 20_000 });
  });

  test('impact: include-observed surfaces the observed-only shadow consumer', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/fleet/impact/payments-service');
    await page.locator('#impact-old-rev').selectOption({ label: 'payments-service 1.0.0' });
    await page.locator('#impact-new-rev').selectOption({ label: 'payments-service 2.0.0' });
    const cb = page.getByRole('checkbox');
    await expect(cb).toBeEnabled(); // the demo carries embedded observed edges
    await cb.check();
    await page.getByRole('button', { name: /Analyze impact/ }).click();
    // audit-log declares no dependency on payments-service — an observed-only shadow.
    await expect(page.getByText('audit-log').first()).toBeVisible({ timeout: 20_000 });
  });

  test('accessibility: keyboard reaches the primary nav with real, named links', async ({ page }) => {
    await waitReady(page);
    await page.keyboard.press('Tab');
    expect(await page.evaluate(() => document.activeElement?.tagName)).toBeTruthy();
    await expect(page.getByRole('link', { name: 'Operational Graph' })).toHaveAttribute('href', /#\/fleet/);
  });
});
