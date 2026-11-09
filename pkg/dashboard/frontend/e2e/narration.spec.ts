import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';
import { installScaleFixture } from './layoutGate';

// G2 in a real browser, for every figure that autoplays on arrival (SPEC 6.2): it plays
// at most once per answer key in a session, never while it is off-screen or the tab is
// hidden, waits on its final frame with every mark at rest and no paused animation, and
// its Skip is disabled unless it plays. The flagship (impact-figure.spec.ts) and the
// revision validation rail (entity-figures.spec.ts) carry the same checks in their own
// specs; the figures that only Replay (the promotion figure, the explainers) are checked
// to rest on arrival.

type Fig = { name: string; sel: string; scale?: boolean; init?: () => void; open: (page: Page) => Promise<void> };

// The demo's provisioning requests all report no phase, which has nothing to tell; give
// each use's first request a reported phase, through the one function every API call
// goes through (see layoutGate.ts).
function reportedPhase(): void {
  type Serve = (method: string, path: string, body: string | null) => Promise<{ status: number; body: string; contentType: string }>;
  let real: Serve | null = null;
  const walk = (o: unknown): void => {
    if (Array.isArray(o)) { o.forEach(walk); return; }
    if (!o || typeof o !== 'object') return;
    const r = o as Record<string, unknown>;
    if (Array.isArray(r.provisioning) && r.provisioning.length) Object.assign(r.provisioning[0] as object, { phase: 'Provisioning', asOf: '2026-11-08T10:00:00Z' });
    Object.values(r).forEach(walk);
  };
  const wrapped: Serve = async (method, path, body) => {
    const res = await (real as Serve)(method, path, body);
    if (res.status !== 200 || !path.startsWith('/api/fleet/')) return res;
    const doc = JSON.parse(res.body);
    walk(doc);
    return { ...res, body: JSON.stringify(doc) };
  };
  Object.defineProperty(window, '__pactoServe', { configurable: true, get() { return real ? wrapped : undefined; }, set(v: Serve) { real = v; } });
}

const AUTOPLAY: Fig[] = [
  // Passive: a source is unavailable in the demo, so the trust story is told.
  { name: 'Overview trust figure', sel: '#fig-trust', open: (p) => p.goto('/#/fleet').then(() => undefined) },
  { name: 'Sources trust figure', sel: '#fig-sources', open: (p) => p.goto('/#/fleet/sources').then(() => undefined) },
  // Passive: a held change.
  { name: 'Delivery change rail', sel: '[data-testid="change-figure"]', open: (p) => p.goto('/#/delivery/changes/test/pay-agent-promote-17').then(() => undefined) },
  // Passive: errored and held changes wait in the inbox at fleet scale.
  { name: 'Inbox pipeline', sel: '[data-testid="inbox-figure"]', scale: true, open: (p) => p.goto('/#/delivery/inbox').then(() => undefined) },
  // Reader-caused: the reader picks a provisioning request.
  {
    name: 'Offering use detail',
    sel: '[data-testid="offering-use-figure"]',
    init: reportedPhase,
    open: async (p) => {
      await p.goto('/#/fleet/offerings/messaging');
      const pick = p.locator('main button[data-testid="provisioning-badge"]').first();
      await expect(pick).toBeVisible({ timeout: 20_000 });
      await pick.click();
    },
  },
];

const REPLAY_ONLY: Fig[] = [
  { name: 'Promotion figure', sel: '[data-testid="promotion-figure"]', open: (p) => p.goto('/#/delivery/environments?service=payments').then(() => undefined) },
];

const state = (page: Page, sel: string) => page.locator(sel).getAttribute('data-narration');
const narrations = (page: Page) => page.evaluate(() => document.getAnimations().filter((a) => String(a.id).startsWith('narr:')).length);
// The final frame: no mark hidden for a step to come or moved off its place (an unreached
// rail station rests at its own 0.75 opacity, which is the drawing, not a step).
const atRest = (page: Page, sel: string) => page.evaluate((s) => [...document.querySelectorAll(`${s} .mk`)]
  .filter((m) => m.getClientRects().length)
  .every((m) => { const cs = getComputedStyle(m); return cs.opacity !== '0' && !m.closest('.nar-hid') && cs.transform === 'none'; }), sel);
const skipOf = (page: Page, sel: string) => page.locator(sel).getByRole('button', { name: 'Skip to final frame' });

test.describe('narration (G2)', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  for (const f of AUTOPLAY) {
    test.describe(f.name, () => {
      test.beforeEach(async ({ page }) => {
        if (f.scale) await page.addInitScript(installScaleFixture);
        if (f.init) await page.addInitScript(f.init);
      });

      test('plays once per session, and Skip is disabled unless it plays', async ({ page }) => {
        await page.addInitScript(() => { if (!sessionStorage.getItem('g2-seen')) { sessionStorage.clear(); sessionStorage.setItem('g2-seen', '1'); } });
        await appReady(page);
        await f.open(page);
        await expect(page.locator(f.sel)).toBeVisible({ timeout: 20_000 });
        await page.locator(f.sel).scrollIntoViewIfNeeded();
        await expect.poll(() => state(page, f.sel), { timeout: 20_000 }).toMatch(/playing|done/);
        if ((await state(page, f.sel)) === 'playing') {
          await expect(skipOf(page, f.sel)).toBeEnabled();
          await skipOf(page, f.sel).click();
          // Skip reaches the final frame in one frame.
          await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => r(null))));
          expect(await state(page, f.sel)).toBe('done');
        }
        await expect.poll(() => state(page, f.sel)).toBe('done');
        await expect(skipOf(page, f.sel)).toBeDisabled();
        // The same answer again this session rests on its final frame, and a route
        // change left no narration behind.
        await page.goto('/#/fleet/owners');
        await expect(page.locator('h1').first()).toBeVisible({ timeout: 20_000 });
        expect(await narrations(page)).toBe(0);
        await f.open(page);
        await expect(page.locator(f.sel)).toBeVisible({ timeout: 20_000 });
        await page.waitForTimeout(300);
        expect(await state(page, f.sel)).toBe('done');
        expect(await narrations(page)).toBe(0);
        expect(await atRest(page, f.sel)).toBe(true);
      });

      test('does not autoplay off-screen, and waits on its final frame', async ({ page }) => {
        // Every page starts 3,000px down, so the figure first renders below the fold.
        await page.addInitScript(() => {
          sessionStorage.clear();
          document.addEventListener('DOMContentLoaded', () => {
            const st = document.createElement('style');
            st.textContent = 'main::before { content: ""; display: block; height: 3000px; }';
            document.head.appendChild(st);
          });
        });
        await appReady(page);
        await f.open(page);
        await expect(page.locator(f.sel)).toBeAttached({ timeout: 20_000 });
        await page.evaluate(() => window.scrollTo(0, 0));
        await page.waitForTimeout(400);
        expect(await page.locator(f.sel).evaluate((el) => el.getBoundingClientRect().top >= window.innerHeight)).toBe(true);
        expect(await state(page, f.sel)).toBe('pending');
        expect(await narrations(page)).toBe(0);
        expect(await atRest(page, f.sel)).toBe(true);
        await expect(skipOf(page, f.sel)).toBeDisabled();
        // In view, it tells its story and comes to rest.
        await page.locator(f.sel).scrollIntoViewIfNeeded();
        await expect.poll(() => state(page, f.sel), { timeout: 10_000 }).toBe('done');
      });

      test('does not autoplay while the tab is hidden', async ({ page }) => {
        // Ready on a page with no narrated figure, so the figure first renders hidden.
        await appReady(page, '/#/fleet/owners');
        await page.evaluate(() => {
          sessionStorage.clear();
          Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' });
        });
        await f.open(page);
        await expect(page.locator(f.sel)).toBeVisible({ timeout: 20_000 });
        await page.waitForTimeout(500);
        expect(await state(page, f.sel)).toBe('pending');
        expect(await narrations(page)).toBe(0);
        expect(await atRest(page, f.sel)).toBe(true);
      });
    });
  }

  for (const f of REPLAY_ONLY) {
    test(`${f.name} rests on arrival and moves only on Replay`, async ({ page }) => {
      await page.addInitScript(() => sessionStorage.clear());
      await appReady(page);
      await f.open(page);
      await expect(page.locator(f.sel)).toBeVisible({ timeout: 20_000 });
      await page.waitForTimeout(500);
      expect(await state(page, f.sel)).toBe('done');
      expect(await narrations(page)).toBe(0);
      expect(await atRest(page, f.sel)).toBe(true);
    });
  }
});
