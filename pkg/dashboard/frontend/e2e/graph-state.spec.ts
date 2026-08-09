import { test, expect, type Page } from '@playwright/test';

// Browser acceptance for Operational Graph SPATIAL STATE (requirement 13.4) and for
// requirement 14 (semantics update while the arrangement holds still).
//
// The regression these prove is gone: an ordinary background refresh used to destroy and
// rebuild the canvas, so every poll threw away wherever the user had dragged things and
// however they had framed the view. "It looks about the same" is not evidence -- a
// screenshot cannot tell a preserved layout from a recomputed one that happens to settle
// somewhere similar. So every assertion here reads the LIVE Cytoscape geometry (node model
// positions, pan, zoom) through the seam the graph publishes on its container, and the
// semantic assertions read the live element data the stylesheet actually renders from.
//
// Forcing a CHANGED backend answer: the demo runs the real engine in wasm and boot.js
// shims window.fetch to call it, so Playwright's network interception never sees an API
// call. The seam used instead is `window.__pactoServe`, the single function boot.js routes
// every API request through. An init script installs an accessor for it BEFORE the wasm
// runtime assigns it, so the test can post-process the real engine's real answer. Nothing
// is stubbed: the fleet, the product API and the rendering are all genuine; only the one
// neighborhood response under test is amended, to simulate the fleet changing between two
// polls -- which is exactly the condition a live dashboard is in.

const PAYMENTS = '/#/fleet/graph/service/payments-service';
const NEW_KEY = 'synthetic-probe-service';

interface Point { x: number; y: number }
interface Spatial { positions: Record<string, Point>; pan: Point; zoom: number }
interface Semantics {
  nodes: { id: string; status: string; label: string }[];
  edges: { id: string; state: string }[];
}

/** Amends the ONE neighborhood response under test, in the page, before the app parses it. */
function installServeInterceptor(): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null) => ServeResult;
  const w = window as unknown as { __pactoMutate?: string };
  let real: Serve | null = null;
  const NEW = 'synthetic-probe-service';
  const wrapped: Serve = (method, path, body) => {
    const res = (real as Serve)(method, path, body);
    const mode = w.__pactoMutate;
    if (!mode || !path.startsWith('/api/fleet/neighborhood') || res.status !== 200) return res;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let doc: any;
    try { doc = JSON.parse(res.body); } catch { return res; }
    if (!doc || !Array.isArray(doc.nodes) || !doc.nodes.length) return res;
    if (mode === 'semantic') {
      // Same topology, different truth: statuses, labels and reconciliation verdicts move.
      for (const n of doc.nodes) {
        if (n.focus) continue;
        n.status = 'NonCompliant';
        // A PREFIX, not a suffix: canvas labels are truncated at 18 characters, so a
        // suffix would be invisible on the canvas and the assertion would be vacuous.
        n.ref.label = `rechecked-${n.ref.label || n.ref.key}`;
      }
      for (const e of doc.edges || []) {
        if (e.relation === 'runs') continue;
        if (e.serviceCorroboration) e.serviceCorroboration = 'expected-not-observed';
        e.difference = 'observed-not-expected';
      }
    } else if (mode === 'addNode' && !doc.nodes.some((n: { ref: { key: string } }) => n.ref.key === NEW)) {
      // Topology change: one genuinely new provider appears next to the focus.
      const focus = doc.nodes.find((n: { focus?: boolean }) => n.focus) || doc.nodes[0];
      const ref = { kind: 'service', key: NEW, label: NEW, href: `/fleet/services/${NEW}` };
      doc.nodes.push({ ref, depth: 1, status: 'Unknown' });
      doc.edges = doc.edges || [];
      doc.edges.push({
        id: `${focus.ref.key}->${NEW}`, from: focus.ref, to: ref, relation: 'dependency',
        expected: true, observed: false, provenance: 'declared', difference: 'expected-not-observed',
        declaredClaims: { total: 0, count: 0, truncated: false },
        observationSources: { total: 0, count: 0, truncated: false },
      });
    }
    return { status: res.status, body: JSON.stringify(doc), contentType: res.contentType };
  };
  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}

async function waitPainted(page: Page) {
  const canvas = page.getByTestId('neighborhood-canvas');
  await expect(canvas).toBeVisible({ timeout: 20_000 });
  await expect(canvas).toHaveAttribute('data-graph-ready', 'painted', { timeout: 20_000 });
  return canvas;
}

async function readSpatial(page: Page): Promise<Spatial | null> {
  return page.evaluate(() => {
    const c = document.querySelector('[data-testid="neighborhood-canvas"]') as
      (HTMLElement & { pactoGraphSpatial?: () => Spatial | null }) | null;
    return c?.pactoGraphSpatial?.() ?? null;
  });
}

/** The live element data Cytoscape styles from -- the canvas's own semantics, not the list's. */
async function readSemantics(page: Page): Promise<Semantics | null> {
  return page.evaluate(() => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const c = document.querySelector('[data-testid="neighborhood-canvas"]') as any;
    const cy = c?._cyreg?.cy;
    if (!cy) return null;
    return {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      nodes: cy.nodes().map((n: any) => ({ id: n.id(), status: n.data('status') || '', label: n.data('label') || '' })),
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      edges: cy.edges().map((e: any) => ({ id: e.id(), state: e.data('state') || '' })),
    };
  });
}

/** Marks the live Cytoscape instance, so a later read can tell "reconciled in place" from
 *  "torn down and rebuilt". Browser-local persistence would restore the arrangement even
 *  across a rebuild, so without this the layout assertions alone could pass while the
 *  canvas was still being destroyed on every poll -- which is the actual regression. */
async function tagInstance(page: Page): Promise<void> {
  await page.evaluate(() => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const cy = (document.querySelector('[data-testid="neighborhood-canvas"]') as any)?._cyreg?.cy;
    if (cy) cy.pactoInstanceTag = 'tagged';
  });
}
async function instanceTag(page: Page): Promise<string> {
  return page.evaluate(() => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const cy = (document.querySelector('[data-testid="neighborhood-canvas"]') as any)?._cyreg?.cy;
    return cy?.pactoInstanceTag ?? 'rebuilt';
  });
}

const sig = (s: Spatial | null) => JSON.stringify(s && {
  p: Object.entries(s.positions).sort().map(([k, v]) => `${k}:${Math.round(v.x)},${Math.round(v.y)}`),
  pan: [Math.round(s.pan.x), Math.round(s.pan.y)], z: s.zoom.toFixed(3),
});

/** Waits until the geometry stops moving, so nothing is asserted mid-animation. */
async function waitStable(page: Page, tries = 25): Promise<Spatial> {
  let prev = await readSpatial(page);
  for (let i = 0; i < tries; i++) {
    await page.waitForTimeout(200);
    const next = await readSpatial(page);
    if (next && prev && sig(next) === sig(prev)) return next;
    prev = next;
  }
  expect(prev, 'graph geometry became readable').not.toBeNull();
  return prev as Spatial;
}

/** Drags a node by a real mouse gesture, toward the middle of the canvas so it stays in view. */
async function dragNode(page: Page, id: string, by = 130): Promise<void> {
  const geom = await page.evaluate((nid) => {
    const c = document.querySelector('[data-testid="neighborhood-canvas"]') as
      (HTMLElement & { pactoGraphSpatial?: () => Spatial | null }) | null;
    const s = c?.pactoGraphSpatial?.();
    if (!c || !s || !s.positions[nid]) return null;
    const r = c.getBoundingClientRect();
    const p = s.positions[nid];
    return {
      x: r.left + s.pan.x + s.zoom * p.x, y: r.top + s.pan.y + s.zoom * p.y,
      cx: r.left + r.width / 2, cy: r.top + r.height / 2,
    };
  }, id);
  expect(geom, `node ${id} has rendered geometry`).not.toBeNull();
  const g = geom as NonNullable<typeof geom>;
  const dx = (g.cx >= g.x ? 1 : -1) * by;
  const dy = (g.cy >= g.y ? 1 : -1) * by * 0.6;
  await page.mouse.move(g.x, g.y);
  await page.mouse.down();
  await page.mouse.move(g.x + dx, g.y + dy, { steps: 12 });
  await page.mouse.up();
}

const moved = (a: Point, b: Point) => Math.hypot(a.x - b.x, a.y - b.y);

/** Arranges the graph deliberately: one dragged node plus a changed zoom (and so pan). */
async function arrange(page: Page, id: string): Promise<Spatial> {
  const before = await waitStable(page);
  await dragNode(page, id);
  await page.getByTestId('graph-zoom-in').click();
  const after = await waitStable(page);
  expect(moved(after.positions[id], before.positions[id]), 'the drag moved the node').toBeGreaterThan(25);
  expect(after.zoom, 'the zoom changed').not.toBeCloseTo(before.zoom, 3);
  return after;
}

function expectSamePlace(after: Spatial, before: Spatial, ids: string[], tol = 1.5) {
  for (const id of ids) {
    expect(after.positions[id], `node ${id} still present`).toBeTruthy();
    expect(moved(after.positions[id], before.positions[id]), `node ${id} stayed put`).toBeLessThan(tol);
  }
  expect(Math.abs(after.pan.x - before.pan.x), 'pan x held').toBeLessThan(tol);
  expect(Math.abs(after.pan.y - before.pan.y), 'pan y held').toBeLessThan(tol);
  expect(after.zoom, 'zoom held').toBeCloseTo(before.zoom, 3);
}

test.describe('Operational graph — spatial state survives refresh, reload and topology change', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(installServeInterceptor);
  });

  test('A-E + 14: a refresh updates the semantics and does NOT move the graph', async ({ page }) => {
    await page.goto(PAYMENTS);
    await waitPainted(page);
    const first = await waitStable(page);
    const ids = Object.keys(first.positions).sort();
    expect(ids.length, 'a real neighborhood').toBeGreaterThan(1);
    const dragged = ids[0];

    // A-B. The user arranges the graph: drags a node, then changes the zoom.
    const arranged = await arrange(page, dragged);
    const semBefore = await readSemantics(page);
    expect(semBefore, 'live canvas semantics readable').not.toBeNull();

    // C. The fleet changes underneath, and the dashboard refreshes.
    await tagInstance(page);
    await page.evaluate(() => { (window as unknown as { __pactoMutate?: string }).__pactoMutate = 'semantic'; });
    await page.getByRole('button', { name: 'Refresh' }).click();

    // 14. The semantics update IMMEDIATELY on the canvas itself -- status, label and the
    // reconciliation verdict the edge is drawn from -- with the topology unchanged. The
    // wait is on the LABEL marker, not on a status: several demo services are already
    // NonCompliant, so waiting for one of those would be satisfied before the refresh
    // even landed and every assertion after it would be vacuous.
    const nonCompliant = (s: Semantics | null) => s?.nodes.filter((n) => n.status === 'NonCompliant').length ?? 0;
    await expect.poll(async () => (await readSemantics(page))?.nodes.filter((n) => n.label.startsWith('rechecked-')).length ?? 0,
      { timeout: 15_000 }).toBeGreaterThan(0);
    const semAfter = (await readSemantics(page)) as Semantics;
    expect(semAfter.nodes.map((n) => n.id).sort(), 'topology unchanged').toEqual(semBefore!.nodes.map((n) => n.id).sort());
    expect(nonCompliant(semAfter), 'compliance verdicts updated on the canvas').toBeGreaterThan(nonCompliant(semBefore));
    expect(semAfter.edges.some((e) => e.state === 'drift'), 'an edge reconciliation state updated').toBe(true);
    // The text alternative tells the same story, in words.
    await page.getByTestId('graph-textalt').locator('summary').click();
    await expect(page.getByTestId('edge-difference').first()).toContainText('Observed, not expected');

    // D-E. The node the user dragged, and the viewport they framed, did not move -- and
    // the canvas was reconciled, not rebuilt.
    expect(await instanceTag(page), 'the graph was updated in place, not rebuilt').toBe('tagged');
    const after = await waitStable(page);
    expectSamePlace(after, arranged, ids);
  });

  test('F: a browser reload restores the arrangement for the same graph URL', async ({ page }) => {
    await page.goto(PAYMENTS);
    await waitPainted(page);
    const ids = Object.keys((await waitStable(page)).positions).sort();
    const arranged = await arrange(page, ids[0]);
    await page.waitForTimeout(600); // the save is debounced

    await page.reload();
    await waitPainted(page);
    const restored = await waitStable(page);
    // Persisted coordinates are rounded to whole pixels, so the tolerance is a pixel.
    expectSamePlace(restored, arranged, ids, 2);
  });

  test('G-I: a topology change keeps the arrangement and places the new node meaningfully', async ({ page }) => {
    await page.goto(PAYMENTS);
    await waitPainted(page);
    const ids = Object.keys((await waitStable(page)).positions).sort();
    const arranged = await arrange(page, ids[0]);

    // G. A new provider appears in the next answer.
    await tagInstance(page);
    await page.evaluate(() => { (window as unknown as { __pactoMutate?: string }).__pactoMutate = 'addNode'; });
    await page.getByRole('button', { name: 'Refresh' }).click();
    await expect.poll(async () => Object.keys((await readSpatial(page))?.positions ?? {}).includes(NEW_KEY),
      { timeout: 15_000 }).toBe(true);
    const after = await waitStable(page);

    // H. Every node that survived is exactly where it was, on the same instance.
    expect(await instanceTag(page), 'the topology change was reconciled, not rebuilt').toBe('tagged');
    expectSamePlace(after, arranged, ids);
    // I. The arrival is placed somewhere meaningful: a real position, not stacked on top
    // of an existing node, and near the arrangement rather than off in empty space.
    const fresh = after.positions[NEW_KEY];
    expect(Number.isFinite(fresh.x) && Number.isFinite(fresh.y)).toBe(true);
    const xs = ids.map((i) => after.positions[i].x);
    const ys = ids.map((i) => after.positions[i].y);
    const extent = Math.max(Math.max(...xs) - Math.min(...xs), Math.max(...ys) - Math.min(...ys), 200);
    const dists = ids.map((i) => moved(fresh, after.positions[i]));
    expect(Math.min(...dists), 'not stacked on an existing node').toBeGreaterThan(60);
    expect(Math.min(...dists), 'attached near the graph, not exiled').toBeLessThan(extent + 600);
  });

  test('J: Reset layout discards the arrangement, and the discarded one does not come back', async ({ page }) => {
    await page.goto(PAYMENTS);
    await waitPainted(page);
    const ids = Object.keys((await waitStable(page)).positions).sort();
    const arranged = await arrange(page, ids[0]);

    await page.getByTestId('graph-reset-layout').click();
    const relaid = await waitStable(page);
    // Some node genuinely moved: this is a relayout, not a re-frame.
    expect(ids.some((i) => moved(relaid.positions[i], arranged.positions[i]) > 25), 'the layout was recomputed').toBe(true);

    // A reload must not resurrect what the user just threw away.
    await page.waitForTimeout(600);
    await page.reload();
    await waitPainted(page);
    const afterReload = await waitStable(page);
    expect(ids.some((i) => moved(afterReload.positions[i], arranged.positions[i]) > 25), 'the discarded arrangement stayed discarded').toBe(true);
  });

  test('K-L: arrangements never leak between two different graphs', async ({ page }) => {
    await page.goto(PAYMENTS);
    await waitPainted(page);
    await waitStable(page);
    // payments-service is a node in BOTH neighborhoods (orders-service depends on it), so
    // a leak would be visible as the same id sitting at the same dragged coordinates.
    const arranged = await arrange(page, 'payments-service');
    await page.waitForTimeout(600);

    await page.evaluate(() => { location.hash = '#/fleet/graph/service/orders-service'; });
    await waitPainted(page);
    // notification-worker is unique to the orders neighborhood: its presence proves the
    // canvas is answering the NEW question before anything is asserted about positions.
    await expect.poll(async () => Object.keys((await readSpatial(page))?.positions ?? {}).includes('notification-worker'),
      { timeout: 20_000 }).toBe(true);
    const other = await waitStable(page);
    expect(moved(other.positions['payments-service'], arranged.positions['payments-service']),
      'the other graph laid itself out, it did not inherit an arrangement').toBeGreaterThan(25);

    // ...and going back is not a fresh layout either: the first graph is as it was left.
    await page.evaluate(() => { location.hash = '#/fleet/graph/service/payments-service'; });
    await waitPainted(page);
    await expect.poll(async () => Object.keys((await readSpatial(page))?.positions ?? {}).includes('notification-worker'),
      { timeout: 20_000 }).toBe(false);
    const back = await waitStable(page);
    expectSamePlace(back, arranged, ['payments-service'], 2);
  });
});
