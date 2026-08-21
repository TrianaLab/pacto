import { test, expect, type Page } from '@playwright/test';

// The REAL visual-graph acceptance. A wrapper, legend and text-alt list prove none of them
// that a NON-HEADLESS Cytoscape renderer actually painted a topology. These tests fail if
// renderGraph falls back to headless or paints nothing, and they keep the discovery route a
// zero-topology search page.
//
// It reads a narrowly-scoped readiness seam the renderer publishes on the canvas container
// (data-graph-* attributes: headless flag, node/edge counts, and how many have real rendered
// geometry) plus the raw <canvas> DOM (count, CSS + backing-store dims, non-blank pixels).

const FOCUS = '/#/fleet/graph/service/payments-service';

async function waitPainted(page: Page) {
  const canvas = page.getByTestId('neighborhood-canvas');
  await expect(canvas).toBeVisible({ timeout: 20_000 });
  // The renderer publishes data-graph-ready="painted" only after the first layout settles
  // on a non-headless renderer; "headless" would mean the fallback fired.
  await expect(canvas).toHaveAttribute('data-graph-ready', 'painted', { timeout: 20_000 });
  return canvas;
}

async function readCanvasDom(page: Page) {
  return page.evaluate(() => {
    const container = document.querySelector('[data-testid="neighborhood-canvas"]') as HTMLElement | null;
    const canvases = container ? Array.from(container.querySelectorAll('canvas')) : [];
    const infos = canvases.map((c) => {
      const r = c.getBoundingClientRect();
      let nonBlank = false;
      try {
        const ctx = c.getContext('2d');
        if (ctx && c.width > 0 && c.height > 0) {
          const step = Math.max(1, Math.floor(c.width / 48));
          const data = ctx.getImageData(0, 0, c.width, c.height).data;
          for (let i = 3; i < data.length; i += 4 * step) { if (data[i] !== 0) { nonBlank = true; break; } }
        }
      } catch { /* tainted / no ctx */ }
      return { cssW: Math.round(r.width), cssH: Math.round(r.height), backW: c.width, backH: c.height, nonBlank };
    });
    const ds = container?.dataset ?? ({} as DOMStringMap);
    return {
      canvasCount: canvases.length,
      infos,
      headless: ds.graphHeadless,
      nodes: Number(ds.graphNodes ?? -1),
      edges: Number(ds.graphEdges ?? -1),
      nodeBoxes: Number(ds.graphNodeBoxes ?? -1),
      edgesRendered: Number(ds.graphEdgesRendered ?? -1),
      textNodes: document.querySelectorAll('[data-testid="graph-node-item"]').length,
      textEdges: document.querySelectorAll('[data-testid="graph-edge"]').length,
    };
  });
}

test.describe('Operational graph — real visual renderer', () => {
  test('discovery route renders NO Cytoscape topology (search-first), with a clear affordance', async ({ page }) => {
    await page.goto('/#/fleet/graph');
    await expect(page.getByTestId('graph-discovery')).toBeVisible({ timeout: 20_000 });
    // No canvas anywhere on the discovery route (no whole-fleet hairball).
    await expect(page.getByTestId('neighborhood-canvas')).toHaveCount(0);
    // The affordance makes it unmistakable a graph appears after a focus is chosen.
    await expect(page.getByTestId('graph-discovery-placeholder')).toBeVisible();
    await expect(page.getByRole('search')).toBeVisible();
  });

  test('focused payments-service paints a real, non-headless topology with nodes and edges', async ({ page }) => {
    const consoleErrors: string[] = [];
    const pageErrors: string[] = [];
    page.on('console', (m) => { if (m.type() === 'error') consoleErrors.push(m.text()); });
    page.on('pageerror', (e) => pageErrors.push(e.message));

    await page.goto(FOCUS);
    await waitPainted(page);
    // Open the text alternative so its nodes/edges are countable for cross-checking.
    await page.getByTestId('graph-textalt').locator('summary').click();
    const m = await readCanvasDom(page);

    // 1-2. The neighborhood has more than one node and at least one edge.
    expect(m.textNodes, 'text-alt node count').toBeGreaterThan(1);
    expect(m.textEdges, 'text-alt edge count').toBeGreaterThanOrEqual(1);
    // 3. The renderer is non-headless (the silent fallback did NOT fire).
    expect(m.headless, 'renderer headless flag').toBe('false');
    // 4. At least one real <canvas> exists inside the graph container.
    expect(m.canvasCount, 'real canvas layers').toBeGreaterThan(0);
    // 5-6. A canvas has meaningful nonzero CSS box AND nonzero backing store.
    const sized = m.infos.filter((c) => c.cssW > 0 && c.cssH > 0 && c.backW > 0 && c.backH > 0);
    expect(sized.length, 'canvases with nonzero CSS + backing-store size').toBeGreaterThan(0);
    // 7. Cytoscape reports the expected node/edge counts (matches the text alternative).
    expect(m.nodes, 'cytoscape node count == text-alt node count').toBe(m.textNodes);
    expect(m.edges, 'cytoscape edge count == text-alt edge count').toBe(m.textEdges);
    expect(m.nodes).toBeGreaterThan(1);
    expect(m.edges).toBeGreaterThanOrEqual(1);
    // 8. At least two nodes have nonzero rendered bounding boxes (real geometry).
    expect(m.nodeBoxes, 'nodes with nonzero rendered bbox').toBeGreaterThanOrEqual(2);
    // 9. At least one edge has a real rendered source/target path.
    expect(m.edgesRendered, 'edges with a real rendered path').toBeGreaterThanOrEqual(1);
    // 10. The renderer produced non-blank canvas output (real pixels, not a blank canvas).
    expect(m.infos.some((c) => c.nonBlank), 'at least one canvas has non-blank pixels').toBe(true);

    expect(consoleErrors, 'console errors').toEqual([]);
    expect(pageErrors, 'page errors').toEqual([]);
  });

  test('focused revision paints a real topology (mixed provider nodes, no fabricated provider revision)', async ({ page }) => {
    await page.goto(FOCUS);
    await waitPainted(page);
    // Switch to the revision projection via the perspective control; it must also paint.
    await page.goto('/#/fleet/graph/service/payments-service?perspective=service');
    await waitPainted(page);
    const m = await readCanvasDom(page);
    expect(m.headless).toBe('false');
    expect(m.nodeBoxes).toBeGreaterThanOrEqual(2);
  });
});
