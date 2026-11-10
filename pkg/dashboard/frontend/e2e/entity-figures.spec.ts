import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';

// Browser acceptance for the entity pages' narrations (SPEC 6, G2) and the marks only a
// real engine renders (G6). The validation rail of a revision whose policy check failed
// plays on arrival once per session, never off-screen or with the tab hidden, and its
// Skip is disabled unless it plays. The shared G1 gate (layout.spec.ts) owns layout.

const FIGURE = '[data-testid="revision-validation-figure"]';

// The demo's revisions all validate, so the failed run is put on the revision the page
// asks for, through the one function every API call goes through (see layoutGate.ts).
function failedRun(): void {
  type Serve = (method: string, path: string, body: string | null) => Promise<{ status: number; body: string; contentType: string }>;
  let real: Serve | null = null;
  const wrapped: Serve = async (method, path, body) => {
    const res = await (real as Serve)(method, path, body);
    if (res.status !== 200 || !path.startsWith('/api/fleet/entities/revision')) return res;
    const doc = JSON.parse(res.body);
    doc.revision.stages = [
      { name: 'structural', status: 'pass' }, { name: 'cross-field', status: 'pass' },
      { name: 'policy', status: 'fail', reason: 'missing owner' }, { name: 'environments', status: 'skip' },
    ];
    return { ...res, body: JSON.stringify(doc) };
  };
  Object.defineProperty(window, '__pactoServe', { configurable: true, get() { return real ? wrapped : undefined; }, set(v: Serve) { real = v; } });
}

async function revisionHref(page: Page): Promise<string> {
  await appReady(page);
  await page.goto('/#/fleet/revisions?service=payments-service');
  const a = page.locator('main a[href*="/fleet/revisions/"]').first();
  await expect(a).toBeVisible({ timeout: 20_000 });
  return (await a.getAttribute('href'))!;
}

const state = (page: Page) => page.locator(FIGURE).getAttribute('data-narration');
const anyNarration = (page: Page) => page.evaluate(() => document.getAnimations().filter((a) => String(a.id).startsWith('narr:')).length);
const atRest = (page: Page) => page.evaluate((sel) => [...document.querySelectorAll(`${sel} .mk`)]
  .filter((m) => m.getClientRects().length)
  .every((m) => { const cs = getComputedStyle(m); return cs.opacity === '1' && cs.transform === 'none'; }), FIGURE);

test.describe('revision validation rail (G2)', () => {
  test.use({ viewport: { width: 1280, height: 900 } });
  test.beforeEach(async ({ page }) => { await page.addInitScript(failedRun); });

  test('plays once per session, and Skip is disabled unless it plays', async ({ page }) => {
    await page.addInitScript(() => { if (!sessionStorage.getItem('g2-seen')) { sessionStorage.clear(); sessionStorage.setItem('g2-seen', '1'); } });
    const href = await revisionHref(page);
    await page.goto(`/${href}`);
    const skip = page.locator(FIGURE).getByRole('button', { name: 'Skip to final frame' });
    await expect.poll(() => state(page), { timeout: 20_000 }).toMatch(/playing|done/);
    if ((await state(page)) === 'playing') {
      await expect(skip).toBeEnabled();
      await skip.click();
      await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => r(null))));
    }
    await expect.poll(() => state(page)).toBe('done');
    await expect(skip).toBeDisabled();
    // The same answer again, this session: it rests on its final frame.
    await page.goto('/#/fleet');
    await page.goto(`/${href}`);
    await expect(page.locator(FIGURE)).toBeVisible({ timeout: 20_000 });
    await page.waitForTimeout(300);
    expect(await state(page)).toBe('done');
    expect(await anyNarration(page)).toBe(0);
    await expect(skip).toBeDisabled();
  });

  test('does not autoplay off-screen, and waits on its final frame', async ({ page }) => {
    await page.addInitScript(() => sessionStorage.clear());
    const href = await revisionHref(page);
    await page.setViewportSize({ width: 1280, height: 200 });
    await page.goto(`/${href}`);
    await expect(page.locator(FIGURE)).toBeAttached({ timeout: 20_000 });
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.waitForTimeout(300);
    expect(await page.locator(FIGURE).evaluate((f) => f.getBoundingClientRect().top >= window.innerHeight)).toBe(true);
    expect(await state(page)).toBe('pending');
    expect(await anyNarration(page)).toBe(0);
    expect(await atRest(page)).toBe(true);
    await expect(page.locator(FIGURE).getByRole('button', { name: 'Skip to final frame' })).toBeDisabled();
    await page.locator(FIGURE).scrollIntoViewIfNeeded();
    await expect.poll(() => state(page), { timeout: 10_000 }).toBe('done');
  });

  test('does not autoplay while the tab is hidden', async ({ page }) => {
    await page.addInitScript(() => {
      sessionStorage.clear();
      Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' });
    });
    const href = await revisionHref(page);
    await page.goto(`/${href}`);
    await expect(page.locator(FIGURE)).toBeVisible({ timeout: 20_000 });
    await page.waitForTimeout(500);
    expect(await state(page)).toBe('pending');
    expect(await anyNarration(page)).toBe(0);
    expect(await atRest(page)).toBe(true);
  });
});

// G6: the marks P4 adds, rendered: the info node, the hub and the provisioning badge
// glyph. Each stroke must reach 3:1 against what it is drawn on.
for (const theme of ['dark', 'light'] as const) {
  test.describe(`entity marks (${theme})`, () => {
    test.use({ colorScheme: theme });
    test('reach 3:1 where they are drawn', async ({ page }) => {
      if (theme === 'light') await page.addInitScript(() => { try { localStorage.setItem('pacto-theme', 'light'); } catch { /* private mode */ } });
      await appReady(page);
      // CI renders DejaVu, wider than the fonts on a laptop; the marks must not depend on it.
      await page.addStyleTag({ content: '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}' });
      const boxes = await page.evaluate(() => {
        const hub = (tone: string) => `<span class="hub t-${tone}"><svg width="44" height="44" viewBox="0 0 44 44"><rect class="hub-f" x="1" y="1" width="42" height="42" rx="9"></rect><path class="hub-g" transform="translate(10 10)" d="M4 9 L12 5 L20 9 L12 13 Z"></path></svg></span>`;
        const host = document.createElement('div');
        host.id = 'g6e';
        host.style.cssText = 'position:fixed;left:0;top:0;z-index:99999;display:flex';
        for (const ground of ['--c-bg', '--c-surface']) {
          const g = document.createElement('div');
          g.className = 'fig';
          g.dataset.ground = ground;
          // .fig scopes the tones; it is a size container, which would collapse a flex item.
          g.style.cssText = `background:var(${ground});padding:12px;display:flex;gap:16px;align-items:center;container-type:normal;flex:none`;
          g.innerHTML = `${hub('accent')}${hub('neutral')}`
            + '<span class="nd t-info"><svg width="28" height="28" viewBox="0 0 28 28"><circle class="f" cx="14" cy="14" r="9"></circle></svg></span>'
            + ['ok', 'err', 'info', 'neutral'].map((t) => `<svg class="pbg" width="14" height="14" viewBox="-7 -7 14 14" style="fill:none;stroke-width:1.8;stroke:var(--c-${t})"><path d="M-4 0 L4 0 M0 -4 L0 4"></path></svg>`).join('');
          host.appendChild(g);
        }
        document.documentElement.appendChild(host);
        const o = host.getBoundingClientRect();
        const rel = (el: Element) => { const r = el.getBoundingClientRect(); return { x: r.left - o.left, y: r.top - o.top, w: r.width, h: r.height }; };
        return [...host.children].map((g) => ({
          ground: (g as HTMLElement).dataset.ground!, g: rel(g),
          marks: [...g.children].map((m, i) => ({ name: `${m.getAttribute('class')}#${i}`, box: rel(m), hub: m.classList.contains('hub') })),
        }));
      });
      const png = (await page.locator('#g6e').screenshot()).toString('base64');
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
        const best = (x0: number, y0: number, w: number, h: number, ground: number[]) => {
          let m = 1;
          for (let x = x0; x < x0 + w; x++) for (let y = y0; y < y0 + h; y++) m = Math.max(m, ratio(px(x, y), ground));
          return m;
        };
        const out: { mark: string; ratio: number }[] = [];
        for (const b of boxes) {
          // Inside the padding, clear of the .fig's rounded corner: the page shows through
          // the corner, so a sample there measures whatever text lies under it.
          const ground = px(b.g.x + 6, b.g.y + b.g.h / 2);
          for (const m of b.marks) {
            out.push({ mark: `${m.name} edge on ${b.ground}`, ratio: best(m.box.x, m.box.y, m.box.w, m.box.h, ground) });
            // The hub glyph on the hub's own fill, sampled just inside its corner.
            if (m.hub) out.push({ mark: `${m.name} glyph on its fill`, ratio: best(m.box.x + 12, m.box.y + 12, 20, 20, px(m.box.x + 6, m.box.y + 38)) });
          }
        }
        return out;
      }, { b64: png, boxes });
      for (const r of results) expect(r.ratio, r.mark).toBeGreaterThanOrEqual(3);
    });
  });
}
