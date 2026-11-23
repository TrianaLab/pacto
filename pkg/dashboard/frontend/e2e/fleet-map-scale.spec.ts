import { test, expect } from '@playwright/test';

// The fleet map at real sizes (509-S12, AC12): the demo serves a generated fleet of N
// services (?fleet=N, internal/fleetgen) and the full-screen map draws it. On every PR, 400
// and 3000 assert function only: no refusal, the Groups level asks for no node page and the
// map paints. Times are logged there and asserted only under PACTO_LAYOUT_FULL=1 (make
// test-browser-layout-full: nightly and on the Version PR), which also runs 10000:
// first paint after the demo is ready and the p95 frame time while zooming, within the
// plan's budgets (Review note N5). The engine's snapshot build in wasm is recorded,
// never asserted: it is the demo engine, not the product path.
test.describe.configure({ mode: 'serial' });

const FULL = !!process.env.PACTO_LAYOUT_FULL;
const SIZES = FULL ? [400, 3000, 10000] : [400, 3000];
// First paint after ready and p95 frame while zooming, in ms.
const BUDGET: Record<number, { paint: number; frame: number }> = { 3000: { paint: 1500, frame: 20 }, 10000: { paint: 3000, frame: 33 } };

type Hooks = { bench: () => { frames: number[]; p95: number }; idle: () => Promise<void>; view: () => { k: number; level: number } };
type Window2 = Window & { __fleetMap?: Hooks; __pactoReady?: Promise<void>; __mapPaths?: string[] };

for (const n of SIZES) {
  test(`the map draws a generated fleet of ${n} services`, async ({ page }) => {
    // The wasm engine builds the whole snapshot at boot: minutes at 10000 on a CI runner.
    test.setTimeout(n >= 10000 ? 900_000 : 300_000);
    // Every API path the app asks, read where boot.js routes it (Playwright's
    // interception never sees a wasm-served fetch).
    await page.addInitScript(() => {
      const w = window as unknown as Window2 & { __pactoServe?: unknown };
      type Serve = (...a: unknown[]) => unknown;
      let real: Serve | null = null;
      w.__mapPaths = [];
      Object.defineProperty(window, '__pactoServe', {
        configurable: true,
        get() { return real ? (...a: unknown[]) => { w.__mapPaths!.push(String(a[1])); return real!(...a); } : undefined; },
        set(v: Serve) { real = v; },
      });
    });
    const t0 = Date.now();
    await page.goto(`/?fleet=${n}#/fleet/graph`);
    await page.evaluate(() => (window as unknown as Window2).__pactoReady);
    const ready = Date.now();
    await page.waitForFunction(() => ((window as unknown as Window2).__fleetMap?.bench().frames.length ?? 0) > 0, null, { timeout: 120_000 });
    const paint = Date.now() - ready;

    await expect(page.getByTestId('fleet-map').getByRole('alert'), 'the map never refuses a fleet').toHaveCount(0);
    await expect(page.getByTestId('fleet-map-summary')).toContainText(/\d/);
    await page.evaluate(() => (window as unknown as Window2).__fleetMap!.idle());
    const fit = await page.evaluate(() => (window as unknown as Window2).__fleetMap!.view());
    const nodePages = () => page.evaluate(() => (window as unknown as Window2).__mapPaths!.filter((p) => p.startsWith('/api/fleet/map/nodes')).length);
    if (n >= 3000) expect(fit.level, `${n} services fit at the Groups level`).toBe(0);
    if (fit.level === 0) expect(await nodePages(), 'the Groups level asks for no node page').toBe(0);

    // Zoom in about the centre, through Services and Names, the way a reader does.
    const box = (await page.getByTestId('fleet-map').locator('canvas[role="img"]').boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    for (let i = 0; i < 24; i++) {
      await page.mouse.wheel(0, -120);
      await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => r(null))));
    }
    await page.evaluate(() => (window as unknown as Window2).__fleetMap!.idle());
    const zoomed = await page.evaluate(() => (window as unknown as Window2).__fleetMap!.view());
    expect(zoomed.level, 'zooming in leaves the Groups level').toBeGreaterThan(0);
    expect(await nodePages(), 'past the Groups level the groups in view load their nodes').toBeGreaterThan(0);
    const frames = (await page.evaluate(() => (window as unknown as Window2).__fleetMap!.bench().frames)).slice(-24).sort((a, b) => a - b);
    const p95 = frames[Math.min(frames.length - 1, Math.floor(frames.length * 0.95))] ?? 0;

    const timing = `fleet=${n}: snapshot build and boot ${ready - t0} ms, first map paint ${paint} ms after ready, zoom p95 frame ${p95.toFixed(1)} ms`;
    console.log(timing);
    test.info().annotations.push({ type: 'timing', description: timing });
    const budget = BUDGET[n];
    if (FULL && budget) {
      expect(paint, `first map paint at ${n} services`).toBeLessThanOrEqual(budget.paint);
      expect(p95, `p95 frame while zooming at ${n} services`).toBeLessThanOrEqual(budget.frame);
    }
  });
}
