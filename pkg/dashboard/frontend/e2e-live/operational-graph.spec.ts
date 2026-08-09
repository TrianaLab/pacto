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

test('a seeded service focuses the live visual operational graph over live HTTP', async ({ page }) => {
  // Search for a reconciled service and follow the result into its focused view, loaded
  // over the live product HTTP API (never the raw snapshot). operational-graph.sh
  // already asserts checkout/orders/payments are in the snapshot.
  await page.goto('/#/fleet/graph');
  await page.getByRole('searchbox').fill('checkout');
  const result = page.getByTestId('graph-focus-link').first();
  await expect(result).toBeVisible({ timeout: 20_000 });
  await result.click();
  // The FOCUSED VISUAL-GRAPH view renders over live HTTP: its canvas controls (Fit,
  // Reset) and the perspective/knowledge toolbar appear, proving the real graph UI +
  // real HTTP API + real operator data render together. The live k8s fleet source is
  // deliberately dependency-light (it carries target/status, not contract dependency
  // edges), so a service neighborhood may be empty; the graph therefore resolves to
  // EITHER the drawn topology (non-empty) or the honest empty state -- both prove the
  // real graph rendered against live data. The rich node/edge/drawer/perspective
  // topology is proven exhaustively by the WASM demo suite (e2e/demo.spec.ts, O1-O11).
  await expect(page).toHaveURL(/\/fleet\/graph\//); // focused, not discovery
  await expect(page.getByRole('group', { name: 'Graph controls' })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByTestId('graph-fit')).toBeVisible();
  await expect(page.getByTestId('graph-reset')).toBeVisible();
  const drawn = page.getByTestId('neighborhood-canvas').or(page.getByTestId('graph-empty'));
  await expect(drawn.first()).toBeVisible({ timeout: 20_000 });
});
