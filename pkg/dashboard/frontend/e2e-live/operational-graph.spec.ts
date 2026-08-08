import { test, expect } from '@playwright/test';

// LIVE Kind browser SMOKE check: runs against the operator-managed dashboard
// (port-forwarded), seeded by tests/e2e/kind/operational-graph.sh. It proves the
// real frontend bundle + the real HTTP API + real operator data render together
// in Chromium (the WASM demo suite cannot cover live HTTP wiring). It is a smoke
// check — the deep declared/observed/reconciled-layer, findings, snapshot-id
// parity and a11y assertions live in the WASM demo suite; this one only confirms
// the live page renders and a seeded service is navigable.

test('the live operational graph page renders (canvas + non-zero service count)', async ({ page }) => {
  // The operational graph moved to /fleet/graph in the Phase-2 IA (/fleet is now the
  // operational overview).
  await page.goto('/#/fleet/graph');
  await expect(page.getByRole('heading', { name: 'Operational Graph' })).toBeVisible();

  // The snapshot is non-empty (served live over HTTP).
  const svcTile = page.locator('.metric-tile', { hasText: 'Services' });
  await expect(svcTile.locator('.metric-value')).not.toHaveText('0');

  // The graph canvas actually renders (Cytoscape draws stacked canvas layers).
  await expect(page.locator('.graph-container canvas').first()).toBeVisible();
});

test('a seeded service is navigable in the live operational graph', async ({ page }) => {
  // The operational graph merges every source (k8s CRs + ingested evidence); the
  // dashboard's legacy landing list only shows its own detected sources, so the
  // evidence-only service is asserted via the graph snapshot (operational-graph.sh
  // already asserts checkout/orders/payments in /api/fleet/snapshot). Here we prove
  // the UI renders and is navigable: deep-link a reconciled service (a default-
  // domain k8s key) and confirm its bounded detail loads over the live HTTP API.
  await page.goto('/#/fleet/graph?sel=checkout');
  const panel = page.getByTestId('detail-panel');
  await expect(panel).toContainText('checkout', { timeout: 20_000 });
  await expect(panel).toContainText('key:'); // domain-qualified identity, live from the operator
});
