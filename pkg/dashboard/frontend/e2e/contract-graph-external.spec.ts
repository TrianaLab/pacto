import { test, expect } from '@playwright/test';
import { DOC_ORIGIN } from './ports';

/**
 * The legacy contract graph tells an external system apart from an unresolved ref.
 *
 * The legacy views have one host, the offline `pacto doc --format html` export, which
 * carries its answers inline as window.__PACTO_STATIC__. Its graph answer is replaced
 * with one service that declares a ref-less external (keyed external:<identity>, the
 * way pkg/contractview keys it) and a ref Pacto cannot fetch, so both node kinds are on
 * one page whatever bundle the export was built from.
 */
const GRAPH = {
  nodes: [
    { id: 'external:stripe', serviceName: 'Stripe', status: 'external', version: '2024-06' },
    { id: 'postgres', serviceName: 'postgres', status: 'unresolved', reason: 'non_oci_ref' },
    {
      id: 'checkout', serviceName: 'checkout', status: 'Compliant', version: '1.0.0',
      edges: [
        { targetId: 'external:stripe', targetName: 'Stripe', required: true, resolved: false, type: 'dependency' },
        { targetId: 'postgres', targetName: 'postgres', required: true, resolved: false, type: 'dependency' },
      ],
    },
  ],
};

test('the contract graph legend and nodes tell external systems apart from unresolved refs', async ({ page }) => {
  await page.route(`${DOC_ORIGIN}/index.html*`, async (route) => {
    const res = await route.fetch();
    const html = await res.text();
    const m = html.match(/window\.__PACTO_STATIC__ = (\{.*?\});?\s*<\/script>/s);
    expect(m, 'the offline export carries no inline answers').not.toBeNull();
    const data = JSON.parse(m![1]) as { routes: Array<{ path: string; response: unknown }> };
    const graph = data.routes.find((r) => r.path === '/api/graph');
    expect(graph, 'the offline export carries no graph answer').toBeTruthy();
    graph!.response = GRAPH;
    const json = JSON.stringify(data).replace(/</g, '\\u003c');
    await route.fulfill({ response: res, body: html.replace(m![1], () => json) });
  });
  await page.goto(`${DOC_ORIGIN}/index.html#/graph`);
  await expect(page.locator('main h1')).toHaveText('Dependency Graph', { timeout: 60_000 });

  const legend = page.locator('.graph-legend');
  const ext = legend.locator('[data-legend="external"]');
  const unresolved = legend.locator('[data-legend="unresolved"]');
  await expect(ext).toHaveText('External system');
  await expect(unresolved).toHaveText('Unresolved ref');
  await expect(legend.locator('.legend-item', { hasText: /^\s*External\s*$/ })).toHaveCount(0);
  // Two different markers: a dashed ring and a filled dot.
  await expect(ext.locator('.legend-dot')).toHaveCSS('border-top-style', 'dashed');
  await expect(unresolved.locator('.legend-dot')).not.toHaveCSS('border-top-style', 'dashed');

  // The connections table labels each node by what it is; neither opens a service page.
  const rows = page.locator('table.data-table tbody tr').filter({ has: page.getByTestId('graph-node-kind') });
  const stripe = rows.filter({ hasText: 'Stripe' });
  const pg = rows.filter({ hasText: 'postgres' });
  await expect(stripe.getByTestId('graph-node-kind')).toHaveText('External system');
  await expect(pg.getByTestId('graph-node-kind')).toHaveText('Unresolved ref');
  await expect(stripe.locator('a')).toHaveCount(0);
  await expect(pg.locator('a')).toHaveCount(0);
  // The service that declares them names both without linking either.
  const checkout = page.locator('table.data-table tbody tr', { hasText: 'checkout' });
  await expect(checkout.locator('td[data-label="Dependencies"] a')).toHaveCount(0);
  await expect(checkout.locator('td[data-label="Dependencies"]')).toContainText('Stripe');
});
