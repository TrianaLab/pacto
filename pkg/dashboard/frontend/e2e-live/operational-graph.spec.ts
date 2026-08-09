import { test, expect } from '@playwright/test';

// LIVE Kind browser SMOKE check: runs against the operator-managed dashboard
// (port-forwarded), seeded by tests/e2e/kind/operational-graph.sh. It proves the
// real frontend bundle + the real HTTP API + real operator data render together in
// Chromium (the WASM demo suite cannot cover live HTTP wiring). It is a smoke check —
// the deep declared/observed/reconciled-layer, difference and a11y assertions live in
// the WASM demo suite; this one only confirms the live search-first graph renders and
// a seeded service is focusable over the live HTTP API.

test('the live operational graph opens search-first (discovery, no fleet hairball)', async ({ page }) => {
  // /fleet/graph with no focus is the search-first discovery state (requirement K/R):
  // no topology is rendered until an entity is focused.
  await page.goto('/#/fleet/graph');
  await expect(page.getByTestId('graph-discovery')).toBeVisible({ timeout: 20_000 });
  await expect(page.getByTestId('neighborhood-canvas')).toHaveCount(0); // zero topology nodes
  await expect(page.getByRole('searchbox')).toBeVisible();
});

test('a seeded service is searchable and focuses a live bounded neighborhood', async ({ page }) => {
  // Search for a reconciled service and follow the result into its focused
  // neighborhood, loaded over the live product HTTP API (never the raw snapshot).
  // operational-graph.sh already asserts checkout/orders/payments in the snapshot.
  await page.goto('/#/fleet/graph');
  await page.getByRole('searchbox').fill('checkout');
  // Follow the SERVICE result (a service focus link carries no perspective param, unlike
  // the revision/target results): orders declares a dependency on checkout, so the
  // checkout service neighborhood is non-empty and renders an actual visual topology.
  const result = page.locator('a[data-testid="graph-focus-link"]:not([href*="perspective="])').first();
  await expect(result).toBeVisible({ timeout: 20_000 });
  await result.click();
  // The bounded neighborhood renders as an actual visual topology over live HTTP.
  await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
  await expect(page.getByTestId('graph-legend')).toBeVisible();
  // The focus identity is the seeded service, live from the operator (shown in the
  // accessible relationship list alongside the visual graph).
  await expect(page.getByRole('heading', { name: 'Operational graph' })).toBeVisible();
  await page.getByTestId('graph-textalt').locator('summary').click();
  await expect(page.getByTestId('graph-node-item').filter({ hasText: 'checkout' }).first()).toBeVisible();
});
