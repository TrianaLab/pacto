import { test, expect } from '@playwright/test';

// LIVE Kind browser acceptance: runs against the operator-managed dashboard
// (port-forwarded), seeded by tests/e2e/kind/operational-graph.sh with two
// declared Pacto services (checkout, orders) the operator reconciled and an
// ingested external evidence target (payments). This proves the real frontend
// bundle + the real HTTP API + real operator data render together in Chromium —
// the WASM demo suite cannot cover the live HTTP wiring.

test('the live operational graph renders the seeded fleet', async ({ page }) => {
  await page.goto('/#/fleet');
  await expect(page.getByRole('heading', { name: 'Operational Graph' })).toBeVisible();

  // The snapshot is non-empty (served live over HTTP).
  const svcTile = page.locator('.metric-tile', { hasText: 'Services' });
  await expect(svcTile.locator('.metric-value')).not.toHaveText('0');

  // The graph canvas actually renders (Cytoscape draws stacked canvas layers).
  await expect(page.locator('.graph-container canvas').first()).toBeVisible();
});

test('the live service list shows the reconciled services and the evidence target', async ({ page }) => {
  await page.goto('/#/');
  // Declared services the operator reconciled from Pacto CRs.
  await expect(page.getByText('checkout', { exact: false }).first()).toBeVisible();
  await expect(page.getByText('orders', { exact: false }).first()).toBeVisible();
  // The external target ingested from a signed EvidenceEnvelope.
  await expect(page.getByText('payments', { exact: false }).first()).toBeVisible();
});
