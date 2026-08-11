import { test, expect, type Page } from '@playwright/test';

// Phase 6 browser acceptance for SCALE and HOSTILE IDENTITY (requirements 21, 22, 25).
//
// The demo fleet is small on purpose -- it is a narrative, not a stress test -- so the
// two questions this file answers cannot be answered by browsing it:
//
//   1. When the backend answers with a population far larger than a page, does the
//      product stay bounded, and does it tell the truth about what it is not showing?
//   2. When an identity is hostile (markup, bidi overrides, a key full of separators,
//      pathological length), does the product still render it as TEXT, keep the key
//      canonical through a round-trip, and refuse to flatten one identity into another?
//
// Both are answered against the REAL engine and the REAL bundle. Only the specific
// response under test is amended, in the page, through `window.__pactoServe` -- the single
// function boot.js routes every API call through (the same seam graph-state.spec.ts uses,
// because Playwright's network interception never sees a wasm-served fetch). Nothing else
// is mocked: the app parses, routes, renders and paginates exactly as it does in production.
//
// Timings are RECORDED, never asserted against an invented millisecond budget. A CI runner
// has no service-level objective, and a threshold nobody derived is a flake generator. The
// assertions are on invariants that hold at any speed: the rendered row count stays at the
// page size no matter how large the answer, and the DOM does not grow with the population.

const LARGE_TOTAL = 25000;
const PAGE_SIZE = 25;

/**
 * Amends product LIST responses to describe a very large population while returning a
 * page-sized slice -- exactly the contract the Product API promises. Every derived tally
 * is scaled to the same population, so the page stays internally consistent and the charts
 * are not asked to draw a distribution that contradicts its own total.
 */
function installScaleInterceptor(): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null) => ServeResult;
  const w = window as unknown as { __pactoScale?: string };
  let real: Serve | null = null;

  // Hostile-but-legal identities. Each is something a real registry, a real cluster label
  // or a real careless commit can produce; none of them may become markup, and none may
  // lose its key. ‮ is a right-to-left override -- the classic filename spoof.
  const HOSTILE = [
    { key: 'domain-x/<img src=x onerror="window.__pactoXss=1">', label: '<img src=x onerror="window.__pactoXss=1">' },
    { key: 'domain-x/svc‮gnp.exe', label: 'svc‮gnp.exe' },
    { key: 'domain-x/a/b/c/d/e/f/g', label: 'a/b/c/d/e/f/g' },
    { key: 'domain-x/' + 'x'.repeat(400), label: 'x'.repeat(400) },
    { key: 'domain-x/quote"and\'apostrophe', label: 'quote"and\'apostrophe' },
    { key: 'domain-x/emoji-\u{1F680}-service', label: 'emoji-\u{1F680}-service' },
    { key: 'domain-x/already%2Fencoded', label: 'already%2Fencoded' },
  ];

  const STATUSES = ['Compliant', 'NonCompliant', 'Unknown', 'NotEvaluated'];
  const name = (i: number) => `service-${String(i).padStart(6, '0')}`;
  // Hrefs are emitted the way the backend emits them -- a plain "/fleet/..." path built
  // from the escaped key, with no "#". The router's hashForHref adds the hash. Building
  // them any other way here would test a shape production never sends.
  const hrefFor = (kind: string, key: string) =>
    `/fleet/${kind === 'service' ? 'services' : kind + 's'}/${encodeURIComponent(key)}`;
  const ref = (i: number, kind: string) => {
    const key = `domain-load/${name(i)}`;
    return {
      kind, key, label: name(i), domain: 'domain-load', status: STATUSES[i % STATUSES.length],
      href: hrefFor(kind, key),
    };
  };

  // Split a population into n buckets that sum EXACTLY to it. A chart whose segments do
  // not add up to its own stated total is a bug the test would otherwise manufacture.
  const split = (total: number, n: number) => {
    const out = Array.from({ length: n }, () => Math.floor(total / n));
    out[0] += total - out.reduce((a, b) => a + b, 0);
    return out;
  };

  const wrapped: Serve = (method, path, body) => {
    const res = (real as Serve)(method, path, body);
    const mode = w.__pactoScale;
    if (!mode || res.status !== 200) return res;
    let doc: Record<string, unknown>;
    try { doc = JSON.parse(res.body); } catch { return res; }

    const query = new URLSearchParams(path.split('?')[1] || '');
    const offset = Number(query.get('offset') || 0);
    const limit = Number(query.get('limit') || 25);

    if (path.split('?')[0] === '/api/fleet/entities') {
      const entities = [];
      for (let i = 0; i < limit; i++) entities.push(ref(offset + i, 'service'));
      if (mode === 'hostile' && offset === 0) {
        for (let i = 0; i < HOSTILE.length; i++) {
          entities[i] = {
            ...entities[i], key: HOSTILE[i].key, label: HOSTILE[i].label, domain: 'domain-x',
            href: hrefFor('service', HOSTILE[i].key),
          };
        }
      }
      doc.entities = entities;
      doc.total = 25000; doc.count = entities.length; doc.offset = offset; doc.limit = limit;
      doc.truncated = true; doc.nextOffset = offset + limit;
      // The inventory figures are computed by the BACKEND over the whole matching
      // population, so they are part of this response and must describe the same 25,000
      // services the rest of it describes. Leaving the demo's real aggregate here would
      // put an 18-service distribution beside a 25,000-service total -- a contradiction
      // this fixture manufactured rather than one the product committed.
      const [compliant, nonCompliant, unknown, notEvaluated] = split(25000, 4);
      const [consistent, conflicting, unowned] = split(25000, 3);
      doc.aggregate = {
        matched: 25000,
        serviceCompliance: { compliant, nonCompliant, unknown, notEvaluated },
        ownership: { consistent, conflicting, unowned },
        byOwner: Array.from({ length: 10 }, (_, i) => ({
          owner: `team-${String(i).padStart(3, '0')}`, services: 2000 - i * 100, targets: 4000 - i * 200,
        })),
        distinctOwners: 900, otherOwners: 10500,
      };
      return { status: res.status, body: JSON.stringify(doc), contentType: res.contentType };
    }

    if (path.split('?')[0] === '/api/fleet/attention') {
      const cats = ['non-compliant', 'invalid', 'unknown', 'stale', 'unresolved', 'readiness'];
      const sevs = ['error', 'warning', 'info'];
      const items = [];
      for (let i = 0; i < limit; i++) {
        const e = ref(offset + i, 'target');
        items.push({
          entity: e, category: cats[i % cats.length], severity: sevs[i % sevs.length],
          code: 'LOAD', label: e.label, summary: `synthetic attention item ${offset + i}`,
          reason: 'synthetic', nextStep: 'Open the target', service: e.key,
        });
      }
      const catCounts = split(25000, cats.length);
      const sevCounts = split(25000, sevs.length);
      doc.items = items;
      doc.total = 25000; doc.count = items.length; doc.offset = offset; doc.limit = limit;
      doc.truncated = true; doc.nextOffset = offset + limit;
      doc.categories = cats.map((c, i) => ({ category: c, count: catCounts[i] }));
      doc.severities = { errors: sevCounts[0], warnings: sevCounts[1], infos: sevCounts[2], unknown: 0 };
      return { status: res.status, body: JSON.stringify(doc), contentType: res.contentType };
    }
    return res;
  };

  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}

/**
 * Records a wall-clock measurement as a baseline. No threshold: a number nobody derived is
 * not an objective, and asserting one turns an unrelated slow runner into a red build.
 */
const baselines: { what: string; ms: number; nodes: number }[] = [];
async function record(page: Page, what: string, run: () => Promise<void>) {
  // Measured from the test process, not from the page: `performance.now()` restarts on a
  // real document load, so an in-page clock silently measures the wrong interval for the
  // first navigation and the right one for every hash change after it.
  const t0 = Date.now();
  await run();
  const ms = Date.now() - t0;
  const nodes = await page.evaluate(() => document.querySelectorAll('*').length);
  baselines.push({ what, ms, nodes });
  return { ms, nodes };
}

test.afterAll(() => {
  if (!baselines.length) return;
  const lines = baselines.map(
    (b) => `  ${b.what.padEnd(44)} ${String(b.ms).padStart(6)} ms  ${String(b.nodes).padStart(6)} DOM nodes`,
  );
  console.log(['', 'Render baselines (recorded, not asserted):', ...lines, ''].join('\n'));
});

test.describe('Product lists stay bounded and honest at scale', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(installScaleInterceptor);
  });

  test('a 25,000-service answer renders one page, states the true total and pages forward', async ({ page }) => {
    await page.addInitScript(() => { (window as unknown as { __pactoScale: string }).__pactoScale = 'large'; });
    const first = await record(page, 'services list, 25k population', async () => {
      await page.goto('/#/fleet/services');
      await expect(page.getByTestId('service-list')).toBeVisible({ timeout: 30000 });
      await expect(page.getByTestId('service-list').locator('li')).toHaveCount(PAGE_SIZE);
    });

    // Both truths, and neither standing in for the other: what exists, and what is shown.
    await expect(page.getByText(/Showing\s+1[–-]25\s+of\s+25000/)).toBeVisible();
    // The figures on this page describe the POPULATION, not the 25 rendered rows, and they
    // say which -- a page distribution read as a fleet distribution is the exact lie
    // requirement 8 exists to prevent. The denominator is 25,000 and the legend adds up
    // to it, on a screen showing 25 services.
    await expect(page.getByText('All 25000 services in the snapshot.').first()).toBeVisible();
    await expect(page.getByText('(25% of 25000)').first()).toBeVisible();
    // And the per-owner ranking says how much of the population it leaves out, rather
    // than reading as a complete breakdown of 900 owners.
    await page.locator('.sv-inv-more summary').click();
    await expect(page.getByText(/Top 10 of 900 declared owners by service count\./)).toBeVisible();
    await expect(page.getByText(/The remaining 890 of 900 owners account for 10500 more services\./)).toBeVisible();

    // Paging forward is real navigation into the same bounded page size.
    await page.getByTestId('svc-next').click();
    await expect(page.getByText(/Showing\s+26[–-]50\s+of\s+25000/)).toBeVisible();
    await expect(page.getByTestId('service-list').locator('li')).toHaveCount(PAGE_SIZE);

    // The invariant that matters more than any millisecond: the document does not grow
    // with the population. A page-2 DOM materially larger than page 1 means accumulation.
    const second = await page.evaluate(() => document.querySelectorAll('*').length);
    expect(Math.abs(second - first.nodes)).toBeLessThan(first.nodes * 0.25);
  });

  test('a 25,000-item attention backlog renders one page while its charts cover the backlog', async ({ page }) => {
    await page.addInitScript(() => { (window as unknown as { __pactoScale: string }).__pactoScale = 'large'; });
    await record(page, 'attention backlog, 25k population', async () => {
      await page.goto('/#/fleet/attention');
      await expect(page.getByTestId('attention-shape')).toBeVisible({ timeout: 30000 });
    });

    await expect(page.locator('.attn-list li')).toHaveCount(PAGE_SIZE);
    await expect(page.getByText(/Showing\s+1[–-]25\s+of\s+25000/)).toBeVisible();

    // Unlike the services list, these two charts are drawn from backend tallies over EVERY
    // matched item, so they must describe the backlog, not the page -- and must therefore
    // NOT carry a page-scope note. Proportions that changed as you paged would be worse
    // than no chart at all.
    const shape = page.getByTestId('attention-shape');
    await expect(shape).toContainText('8333');
    await expect(shape).not.toContainText(/This page only/);

    await page.getByTestId('attn-next').click();
    await expect(page.getByText(/Showing\s+26[–-]50\s+of\s+25000/)).toBeVisible();
    await expect(page.locator('.attn-list li')).toHaveCount(PAGE_SIZE);
    await expect(shape).toContainText('8333');
  });
});

test('render baseline for every product surface, on real demo data', async ({ page }) => {
  // A recorded baseline, not a gate. The value of this test is the numbers it prints and
  // the regression a human can see in them; inventing a threshold here would assert a
  // budget nobody derived from a requirement, on a runner whose speed nobody controls.
  // What IS asserted is that each surface reached a genuinely RENDERED state -- each wait
  // is on a marker that only exists once that surface's data is on screen, so a fast
  // number can never be a measurement of an empty page.
  //
  // Cold boot is recorded once and separately, because it is dominated by instantiating
  // the demo's 57 MB wasm bundle and says more about the demo than about the product.
  // Everything after it is a warm client-side route render, which is what a user
  // experiences for every navigation after the first.
  await record(page, 'cold boot (wasm instantiate + Overview)', async () => {
    await page.goto('/#/fleet');
    await expect(page.locator('main figure.dist').first()).toBeVisible({ timeout: 30000 });
  });

  const surfaces: [string, string, string][] = [
    ['Services list', '#/fleet/services', '[data-testid="service-list"] li'],
    ['Attention', '#/fleet/attention', '.attn-list li'],
    ['Service detail', '#/fleet/services/' + encodeURIComponent('payments-service'), '.ev-body'],
    ['Operational Graph (focused)', '#/fleet/graph/service/payments-service', '[data-testid="neighborhood-canvas"] canvas'],
    ['Change analysis', '#/fleet/changes', '[data-testid="impact-service-picker"]'],
    ['Overview', '#/fleet', 'main figure.dist'],
  ];
  for (const [name, hash, ready] of surfaces) {
    await record(page, `${name} (demo data)`, async () => {
      await page.goto(`/${hash}`);
      await expect(page.locator(ready).first()).toBeVisible({ timeout: 30000 });
    });
  }
});

test.describe('Hostile identities render as text and keep their key', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(installScaleInterceptor);
    await page.addInitScript(() => { (window as unknown as { __pactoScale: string }).__pactoScale = 'hostile'; });
  });

  test('markup, bidi overrides, quotes and a 400-character key are escaped, not executed', async ({ page }) => {
    await record(page, 'services list, hostile identities', async () => {
      await page.goto('/#/fleet/services');
      await expect(page.getByTestId('service-list')).toBeVisible({ timeout: 30000 });
      await expect(page.getByTestId('service-list').locator('li')).toHaveCount(PAGE_SIZE);
    });

    // Nothing executed, nothing injected: the label reached the DOM as text.
    expect(await page.evaluate(() => (window as unknown as { __pactoXss?: number }).__pactoXss)).toBeUndefined();
    expect(await page.locator('[data-testid="service-list"] img').count()).toBe(0);
    await expect(page.getByTestId('service-list')).toContainText('onerror');
    await expect(page.getByTestId('service-list')).toContainText('quote"and\'apostrophe');

    // The layout survives a 400-character identity. An unbroken key in a flex row pushes
    // the whole page sideways, which is how one bad name breaks every other row.
    const overflow = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="service-list"]') as HTMLElement;
      return Math.max(el.scrollWidth - el.clientWidth, document.documentElement.scrollWidth - document.documentElement.clientWidth);
    });
    expect(overflow).toBeLessThanOrEqual(2);
  });

  test('a key full of separators round-trips to the SAME entity, never a flattened one', async ({ page }) => {
    await page.goto('/#/fleet/services');
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: 30000 });

    // "domain-x/a/b/c/d/e/f/g" is ONE canonical ServiceKey that happens to contain six
    // slashes, and "domain-x/already%2Fencoded" is a key whose literal text contains a
    // percent escape. Both must survive the hash round-trip byte for byte: a key that
    // decodes twice silently becomes a different service.
    const link = page.locator('[data-testid="service-list"] a[href*="a%2Fb%2Fc"]').first();
    await expect(link).toHaveCount(1);
    await link.click();
    await expect(page).toHaveURL(/a%2Fb%2Fc%2Fd%2Fe%2Ff%2Fg/);
    // Whatever the engine then says about a service it has never heard of, the page must
    // be about the key that was asked for. Resolving a DIFFERENT key would be the failure,
    // and the page heading is where the router's decoded key becomes visible.
    await expect(page.locator('h1')).toHaveText('Service: domain-x/a/b/c/d/e/f/g');

    await page.goBack();
    await expect(page.getByTestId('service-list')).toBeVisible();
    const encoded = page.locator('[data-testid="service-list"] a[href*="already%252Fencoded"]').first();
    await expect(encoded).toHaveCount(1);
    await encoded.click();
    // Decoded exactly once: a second decode would silently turn this into "already/encoded".
    await expect(page.locator('h1')).toHaveText('Service: domain-x/already%2Fencoded');
  });
});
