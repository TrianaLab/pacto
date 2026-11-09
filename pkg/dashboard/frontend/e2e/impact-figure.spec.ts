import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { appReady, settle } from './ready';
import { installScaleFixture } from './layoutGate';

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
// Every narr: animation the document still holds, finished or paused included.
const anyNarration = (page: Page) => page.evaluate(() => document.getAnimations().filter((a) => String(a.id).startsWith('narr:')).length);
// Every visible mark of the figure at full opacity and in place: the final frame.
const atRest = (page: Page) => page.evaluate((sel) => [...document.querySelectorAll(`${sel} .mk`)]
  .filter((m) => m.getClientRects().length)
  .every((m) => { const cs = getComputedStyle(m); return cs.opacity === '1' && cs.transform === 'none'; }), FIGURE);
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
      // The impact story is gone with its page; the overview it lands on may tell its
      // own (the trust figure, when a source is unavailable), which is a new story.
      expect(await page.evaluate(() => document.getAnimations().filter((a) => a.id === 'narr:impact').length)).toBe(0);
    });

    // G2 (r5): a figure waiting for autoplay shows its final frame and holds no paused
    // animation; it plays only once it is in view.
    test('does not autoplay off-screen, and waits on its final frame', async ({ page }) => {
      await page.addInitScript(() => sessionStorage.clear());
      await page.setViewportSize({ width: 1280, height: 320 });
      await pick(page);
      await page.getByRole('button', { name: /Compare revisions/ }).click();
      await expect(page.locator(`${FIGURE} svg[role="img"]`)).toBeAttached({ timeout: 30_000 });
      // The answer lands under the fold: the browser does not scroll to it.
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.waitForTimeout(300);
      expect(await page.locator(FIGURE).evaluate((f) => f.getBoundingClientRect().top >= window.innerHeight)).toBe(true);
      expect(await state(page)).toBe('pending');
      expect(await anyNarration(page)).toBe(0);
      expect(await atRest(page)).toBe(true);
      await page.locator(FIGURE).scrollIntoViewIfNeeded();
      await expect.poll(() => state(page), { timeout: 10_000 }).toMatch(/playing|done/);
      await expect.poll(() => state(page), { timeout: 10_000 }).toBe('done');
      expect(await narrations(page)).toBe(0);
    });

    test('does not autoplay while the tab is hidden', async ({ page }) => {
      await page.addInitScript(() => {
        sessionStorage.clear();
        Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' });
      });
      await pick(page);
      await page.getByRole('button', { name: /Compare revisions/ }).click();
      await expect(page.locator(`${FIGURE} svg[role="img"]`)).toBeAttached({ timeout: 30_000 });
      await page.locator(FIGURE).scrollIntoViewIfNeeded();
      await page.waitForTimeout(500);
      expect(await state(page)).toBe('pending');
      expect(await anyNarration(page)).toBe(0);
      expect(await atRest(page)).toBe(true);
    });

    // C8a and G3 (r5): on the G1 scale fixture (1,284 consumers, names up to 200
    // characters) the flagship at 1280 names at least 4 consumers, every lane with
    // highlights names one, and each "N more" counts what its lane does not name.
    test('names at least 4 consumers at 1280 on the scale fixture', async ({ page }) => {
      await page.addInitScript(installScaleFixture);
      await analyse(page);
      await page.evaluate(() => document.getAnimations().filter((a) => String(a.id).startsWith('narr:')).forEach((a) => a.finish()));
      await settle(page);
      const lanes = await page.locator(`${FIGURE} .lane:not(.ghost)`).evaluateAll((ls) => ls.map((l) => ({
        name: l.querySelector('.lane-name')?.textContent ?? '',
        shown: [...l.querySelectorAll('.chip')].filter((c) => c.getClientRects().length).length,
        more: l.querySelector('.lane-more')?.textContent ?? '',
        n: Number(l.querySelector('.lane-count b')?.textContent?.replace(/[^\d]/g, '') ?? 0),
        candidates: l.querySelectorAll('.chip').length,
        // C6: an elided chip keeps a whole head segment and at least 5 tail characters (or
        // none, the head alone, when a lane's only chip has no room for a tail).
        cut: [...l.querySelectorAll('.chip bdi')].filter((b) => b.getClientRects().length && (b.textContent ?? '').includes('…'))
          .map((b) => (b.textContent ?? '').split('…'))
          .filter((p) => !/[-_./:]$/.test(p[0]) || [1, 2, 3, 4].includes([...p[p.length - 1]].length)).map((p) => p.join('…')),
      })));
      expect(lanes.reduce((a, l) => a + l.shown, 0), JSON.stringify(lanes)).toBeGreaterThanOrEqual(4);
      for (const l of lanes) {
        if (l.candidates) expect(l.shown, l.name).toBeGreaterThanOrEqual(1);
        expect(l.cut, l.name).toEqual([]);
        expect(l.more, l.name).toBe(l.shown ? (l.n > l.shown ? `${(l.n - l.shown).toLocaleString('en-US')} more` : '') : `List all ${l.n.toLocaleString('en-US')}`);
      }
    });

    // G6 (r5): a pattern is not a colour, so each hatched, frosted and open-capped mark is
    // rendered on the drawing's face and on the page, and the pixels of its solid edge
    // are sampled: they must reach 3:1 against the ground.
    test('pattern edges reach 3:1 on the face and the page', async ({ page }) => {
      await appReady(page);
      await page.goto('/#/fleet/changes/payments-service');
      await expect(page.locator('h1').first()).toBeVisible({ timeout: 20_000 });
      const boxes = await page.evaluate(() => {
        const host = document.createElement('div');
        host.id = 'g6';
        host.style.cssText = 'position:fixed;left:0;top:0;z-index:99999;margin:0;padding:0;border:0;border-radius:0;display:flex';
        for (const ground of ['--c-iso-face', '--c-bg']) {
          const g = document.createElement('div');
          g.dataset.ground = ground;
          g.style.cssText = `background:var(${ground});padding:12px;display:flex;gap:16px;align-items:center`;
          g.innerHTML = '<span class="mbar" style="width:64px"><i class="hatch" style="width:48px"></i></span>'
            + '<span class="nd unk t-neutral"><svg width="28" height="28" viewBox="0 0 28 28"><circle class="f" cx="14" cy="14" r="9"></circle></svg></span>'
            + '<svg class="opencap" width="12" height="16" viewBox="0 0 12 16"><path d="M4 1 L1.5 15 M10.5 1 L8 15"></path></svg>'
            // Frost (SPEC 2.2 r4), frozen only: a queue bar and a node.
            + '<span class="qbar" style="width:64px;flex:none"><i class="t-frost" style="width:48px"></i></span>'
            + '<span class="nd t-frost" style="--tone:var(--c-info)"><svg width="28" height="28" viewBox="0 0 28 28"><circle class="f" cx="14" cy="14" r="9"></circle></svg></span>';
          host.appendChild(g);
        }
        document.documentElement.appendChild(host);
        const o = host.getBoundingClientRect();
        const rel = (el: Element) => { const r = el.getBoundingClientRect(); return { x: r.left - o.left, y: r.top - o.top, w: r.width, h: r.height }; };
        return [...host.children].map((g) => ({
          ground: (g as HTMLElement).dataset.ground!, g: rel(g),
          // Where each edge is: the hatch's left edge column, the node's top arc, the cap's strokes.
          hatch: rel(g.querySelector('.mbar i')!), node: rel(g.querySelector('.nd.unk svg')!), cap: rel(g.querySelector('.opencap')!),
          frost: rel(g.querySelector('.qbar i')!), frostNode: rel(g.querySelector('.nd.t-frost svg')!),
        }));
      });
      const png = (await page.locator('#g6').screenshot()).toString('base64');
      const results = await page.evaluate(async ({ b64, boxes }) => {
        const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
        const bmp = await createImageBitmap(new Blob([bytes], { type: 'image/png' }));
        const cv = document.createElement('canvas');
        cv.width = bmp.width; cv.height = bmp.height;
        const cx = cv.getContext('2d')!;
        cx.drawImage(bmp, 0, 0);
        const px = (x: number, y: number) => Array.from(cx.getImageData(Math.floor(x), Math.floor(y), 1, 1).data.slice(0, 3));
        const lum = ([r, g, b]: number[]) => { const l = (c: number) => ((c /= 255) <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4); return 0.2126 * l(r) + 0.7152 * l(g) + 0.0722 * l(b); };
        const ratio = (a: number[], b: number[]) => { const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); };
        const best = (pts: [number, number][], ground: number[]) => Math.max(...pts.map(([x, y]) => ratio(px(x, y), ground)));
        const out: { ground: string; mark: string; ratio: number }[] = [];
        for (const b of boxes) {
          const ground = px(b.g.x + 2, b.g.y + 2);
          out.push({ ground: b.ground, mark: 'hatch edge', ratio: ratio(px(b.hatch.x + 0.5, b.hatch.y + b.hatch.h / 2), ground) });
          const top: [number, number][] = [];
          for (let y = 0; y < 9; y++) top.push([b.node.x + 14, b.node.y + y]);
          out.push({ ground: b.ground, mark: 'hatched node edge', ratio: best(top, ground) });
          const cap: [number, number][] = [];
          for (let x = 0; x < b.cap.w; x++) for (let y = 0; y < b.cap.h; y++) cap.push([b.cap.x + x, b.cap.y + y]);
          out.push({ ground: b.ground, mark: 'open cap', ratio: best(cap, ground) });
          out.push({ ground: b.ground, mark: 'frost edge', ratio: ratio(px(b.frost.x + 0.5, b.frost.y + b.frost.h / 2), ground) });
          const ftop: [number, number][] = [];
          for (let y = 0; y < 9; y++) ftop.push([b.frostNode.x + 14, b.frostNode.y + y]);
          out.push({ ground: b.ground, mark: 'frost node edge', ratio: best(ftop, ground) });
        }
        return out;
      }, { b64: png, boxes });
      for (const r of results) expect(r.ratio, `${r.mark} on ${r.ground}`).toBeGreaterThanOrEqual(3);
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
