import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { appReady, settle } from './ready';

// Browser acceptance for the Change analysis figure (SPEC 7, flagship) and its narration
// (SPEC 6, G2), and for the isometric backdrop behind the Overview header and the idle
// empty states. The shared G1 gate (layout.spec.ts) owns overlap, scale and copy-in-box;
// this file owns what only a real engine can show: the narration plays when the reader
// asks, once per answer, Skip ends it in one frame, reduced motion steps instead, a route
// change ends it, and the rest state is every mark at full opacity in place.
//
// The assertions read whatever the demo answers for its known breaking pair, never the
// literals of a particular fixture publish.

const TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'];
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';
const FIGURE = '[data-testid="impact-figure"]';

// The revision pickers read "1.0.0 · <short digest>": pick the one of a version once the
// revisions have loaded (the same helper as demo.spec.ts).
async function chooseRevision(page: Page, sel: string, version: string) {
  const read = () => page.locator(`${sel} option`).evaluateAll(
    (os, v) => (os as HTMLOptionElement[]).find((o) => (o.textContent ?? '').startsWith(`${v} `))?.value ?? '', version);
  await expect.poll(read, { message: `no ${version} revision in ${sel}`, timeout: 20_000 }).not.toBe('');
  await page.locator(sel).selectOption(await read());
}

async function pick(page: Page) {
  await appReady(page);
  await page.goto('/#/fleet/changes/payments-service');
  await page.addStyleTag({ content: WIDE_FONT });
  await chooseRevision(page, '#impact-old-rev', '1.0.0');
  await chooseRevision(page, '#impact-new-rev', '2.0.1');
}

async function analyse(page: Page) {
  await pick(page);
  await page.getByRole('button', { name: /Compare revisions/ }).click();
  await expect(page.locator(`${FIGURE} svg[role="img"]`)).toBeAttached({ timeout: 30_000 });
}

const narrations = (page: Page) => page.evaluate(() => document.getAnimations()
  .filter((a) => String(a.id).startsWith('narr:') && a.playState !== 'finished').length);
const state = (page: Page) => page.locator(FIGURE).getAttribute('data-narration');

for (const theme of ['dark', 'light'] as const) {
  test.describe(`impact figure (${theme})`, () => {
    test.use({ colorScheme: theme, viewport: { width: 1280, height: 900 } });
    test.beforeEach(async ({ page }) => {
      if (theme === 'light') {
        await page.addInitScript(() => { try { localStorage.setItem('pacto-theme', 'light'); } catch { /* private mode */ } });
      }
    });

    test('draws the breaking pair and settles for axe', async ({ page }) => {
      test.setTimeout(90_000);
      await analyse(page);
      await page.evaluate(() => document.getAnimations().filter((a) => String(a.id).startsWith('narr:')).forEach((a) => a.finish()));
      const label = (await page.locator(`${FIGURE} svg[role="img"]`).getAttribute('aria-label')) ?? '';
      expect(label).toMatch(/\d+ incompatible, \d+ unknown, \d+ compatible/);
      await expect(page.locator(`${FIGURE} .lane`).first()).toBeVisible();
      await expect(page.locator(`${FIGURE} .lane-name`).first()).toHaveText('Incompatible');
      await expect(page.locator(`${FIGURE} .stamp`).first()).toBeVisible();
      await expect(page.locator(`${FIGURE} [data-ring]`)).toHaveCount(await page.getByText('Blocked', { exact: true }).count() ? 1 : 0);
      await settle(page);
      const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
      expect(results.violations, JSON.stringify(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))).toEqual([]);
    });

    test('a lane\'s "more" opens the consumers table below the sticky navbar', async ({ page }) => {
      await analyse(page);
      await page.locator(`${FIGURE} .lane-more:visible`).first().click();
      const fold = page.locator('#sec-affected-consumers');
      await expect(fold).toHaveAttribute('open', '');
      await expect(fold.locator('> summary')).toBeFocused();
      // WCAG 2.2 2.4.11: the focused summary is not hidden under the sticky navbar.
      await expect.poll(() => page.evaluate(() => {
        const s = document.querySelector('#sec-affected-consumers > summary')!.getBoundingClientRect();
        const nav = document.querySelector('.navbar')!.getBoundingClientRect();
        return s.top >= nav.bottom && s.bottom <= window.innerHeight;
      })).toBe(true);
    });

    test('tells the answer once when the reader compares, and Skip ends it in one frame', async ({ page }) => {
      await page.addInitScript(() => sessionStorage.clear());
      await pick(page);
      await page.getByRole('button', { name: /Compare revisions/ }).click();
      await expect.poll(() => state(page), { timeout: 30_000 }).toMatch(/playing|done/);
      const skip = page.getByRole('button', { name: 'Skip to final frame' });
      if ((await state(page)) === 'playing') {
        await skip.click();
        await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => r(null))));
      }
      await expect.poll(() => state(page)).toBe('done');
      await expect(skip).toBeDisabled();
      expect(await narrations(page)).toBe(0);
      // The same answer again: told already this session, so it rests on its final frame.
      await page.getByRole('button', { name: /Compare revisions/ }).click();
      await expect(page.locator(`${FIGURE} svg[role="img"]`)).toBeAttached({ timeout: 30_000 });
      await page.waitForTimeout(100);
      expect(await narrations(page)).toBe(0);
      await expect.poll(() => state(page)).toBe('done');
    });

    test('a route change leaves no narration running', async ({ page }) => {
      await page.addInitScript(() => sessionStorage.clear());
      await pick(page);
      await page.getByRole('button', { name: /Compare revisions/ }).click();
      await expect(page.locator(`${FIGURE} svg[role="img"]`)).toBeAttached({ timeout: 30_000 });
      await page.evaluate(() => { location.hash = '#/fleet'; });
      await expect(page.locator(FIGURE)).toHaveCount(0);
      expect(await narrations(page)).toBe(0);
    });

    test('steps instead of moving under reduced motion, and rests with every mark in place', async ({ page }) => {
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await analyse(page);
      await expect(page.locator(FIGURE).getByText(/^Step 4 of 4$/)).toBeVisible();
      expect(await narrations(page)).toBe(0);
      const rest = () => page.evaluate((sel) => [...document.querySelectorAll(`${sel} .mk`)]
        .filter((m) => m.getClientRects().length)
        .map((m) => { const cs = getComputedStyle(m); return { cls: m.getAttribute('class'), opacity: cs.opacity, transform: cs.transform }; }), FIGURE);
      for (const r of await rest()) {
        expect(r.opacity, r.cls ?? '').toBe('1');
        expect(r.transform, r.cls ?? '').toBe('none');
      }
      await page.locator(FIGURE).getByRole('button', { name: 'Previous' }).click();
      await expect(page.locator(FIGURE).getByText(/^Step 3 of 4$/)).toBeVisible();
      await expect(page.locator(`${FIGURE} [data-m="gate"]`)).toHaveCSS('opacity', '0');
      await page.locator(FIGURE).getByRole('button', { name: 'Next' }).click();
      await expect(page.locator(`${FIGURE} [data-m="gate"]`)).toHaveCSS('opacity', '1');
    });

    // axe reports every text node over a large ::before as "incomplete" (pseudoContent)
    // rather than measuring it, so the backdrop takes those nodes out of the enforced
    // contrast check (isoContrast.test.ts replaces it). Assert that it takes out nothing
    // else: every incomplete node must sit inside the backdrop for that reason.
    for (const [name, hash, ready] of [
      ['Overview', '#/fleet', 'Needs attention'],
      ['Change analysis idle', '#/fleet/changes/payments-service', /Ready to compare|Pick two revisions/],
    ] as const) {
      test(`${name}: only the backdrop leaves the contrast check incomplete`, async ({ page }) => {
        await appReady(page);
        await page.goto(`/${hash}`);
        await expect(page.getByRole('heading', { name: ready }).first()).toBeVisible({ timeout: 20_000 });
        await expect(page.locator('.iso-backdrop').first()).toBeVisible();
        await settle(page);
        const res = await new AxeBuilder({ page }).withRules(['color-contrast']).analyze();
        expect(res.violations.map((v) => v.nodes.map((n) => n.target))).toEqual([]);
        const nodes = (res.incomplete.find((r) => r.id === 'color-contrast')?.nodes ?? [])
          .map((n) => ({ target: String(n.target[0]), key: n.any.map((c) => (c.data as { messageKey?: string } | null)?.messageKey).join(',') }));
        // Other incompletes (a non-BMP icon glyph, say) are axe's own limits and predate the
        // backdrop; a pseudoContent incomplete outside it would be a new blind spot.
        const stray = await page.evaluate((ns) => ns.filter((n) =>
          n.key.includes('pseudoContent') && !document.querySelector(n.target)?.closest('.iso-backdrop')), nodes);
        expect(nodes.some((n) => n.key.includes('pseudoContent')), 'the backdrop drew nothing axe noticed').toBe(true);
        expect(stray).toEqual([]);
      });
    }
  });
}
