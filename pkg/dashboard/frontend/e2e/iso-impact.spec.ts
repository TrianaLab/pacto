import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { appReady, settle } from './ready';

// Browser acceptance for the isometric impact diagram in Change analysis, and for the
// isometric backdrop behind the Overview header and the idle empty states.
//
// The unit tests prove the layout model and the component in jsdom. What only a real
// engine can prove is that the drawn labels do not collide or spill once a real font
// renders them (Verdana is forced here because CI renders DejaVu, which is wider than
// Inter), that the motion settles for axe and collapses under reduced motion, and that
// "+N more" lands the reader on the table below the sticky navbar.
//
// The assertions read whatever the demo answers for its known breaking pair, never the
// literals of a particular fixture publish.

const TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'];
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';
const FIGURE = '[data-testid="iso-impact"]';

// The revision pickers read "1.0.0 · <short digest>": pick the one of a version once the
// revisions have loaded (the same helper as demo.spec.ts).
async function chooseRevision(page: Page, sel: string, version: string) {
  const read = () => page.locator(`${sel} option`).evaluateAll(
    (os, v) => (os as HTMLOptionElement[]).find((o) => (o.textContent ?? '').startsWith(`${v} `))?.value ?? '', version);
  await expect.poll(read, { message: `no ${version} revision in ${sel}`, timeout: 20_000 }).not.toBe('');
  await page.locator(sel).selectOption(await read());
}

async function analyse(page: Page) {
  await appReady(page);
  await page.goto('/#/fleet/changes/payments-service');
  await page.addStyleTag({ content: WIDE_FONT });
  await chooseRevision(page, '#impact-old-rev', '1.0.0');
  await chooseRevision(page, '#impact-new-rev', '2.0.1');
  await page.getByRole('button', { name: /Compare revisions/ }).click();
  await expect(page.locator(`${FIGURE} svg[role="img"]`)).toBeVisible({ timeout: 30_000 });
}

/**
 * The layout gate: no two labels outside the sheet overlap, every label sits inside the
 * drawing, and the page never scrolls sideways. Skewed text (the sheet's rows and the
 * provider's face label) is skipped: its axis-aligned box overlaps its neighbours by
 * construction. Lone surrogates are checked too: a cut emoji renders as a replacement box.
 */
async function assertLayout(page: Page, where: string) {
  const r = await page.evaluate((sel) => {
    const svg = document.querySelector(`${sel} svg`) as SVGSVGElement;
    const box = svg.getBoundingClientRect();
    const texts = Array.from(svg.querySelectorAll('text'))
      .filter((t) => !t.closest('.lift') && !t.hasAttribute('transform') && (t.textContent ?? '').trim());
    const rects = texts.map((t) => ({ s: (t.textContent ?? '').trim(), b: t.getBoundingClientRect() }));
    const overlaps: string[] = [];
    for (let i = 0; i < rects.length; i++) {
      for (let j = i + 1; j < rects.length; j++) {
        const a = rects[i].b, b = rects[j].b;
        if (a.left < b.right - 0.5 && b.left < a.right - 0.5 && a.top < b.bottom - 0.5 && b.top < a.bottom - 0.5) {
          overlaps.push(`"${rects[i].s}" x "${rects[j].s}"`);
        }
      }
    }
    const outside = rects
      .filter(({ b }) => b.left < box.left - 1 || b.right > box.right + 1 || b.top < box.top - 1 || b.bottom > box.bottom + 1)
      .map(({ s }) => s);
    const lone = Array.from(svg.querySelectorAll('text'))
      .map((t) => t.textContent ?? '')
      .filter((s) => /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/.test(s));
    const doc = document.documentElement;
    return { n: rects.length, overlaps, outside, lone, spill: doc.scrollWidth - doc.clientWidth };
  }, FIGURE);
  expect(r.n, `${where}: no labels measured`).toBeGreaterThan(0);
  expect(r.overlaps, `${where}: labels overlap`).toEqual([]);
  expect(r.outside, `${where}: labels outside the drawing`).toEqual([]);
  expect(r.lone, `${where}: a label splits a surrogate pair`).toEqual([]);
  expect(r.spill, `${where}: the page scrolls sideways`).toBeLessThanOrEqual(0);
}

/**
 * Amends the impact answer to twelve direct consumers with 200-character identities (one
 * with an emoji at every possible cut, one with a right-to-left override), highlights
 * capped at 8 as the backend caps them, a next page, partial evidence and three consumers
 * the viewer may not see. Through `window.__pactoServe`, the one function boot.js routes
 * every API call through: Playwright's interception never sees a wasm-served fetch.
 */
function installImpactScale(): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null) => Promise<ServeResult>;
  let real: Serve | null = null;
  const long = (s: string) => (s + '-').repeat(200).slice(0, 200);
  const NAMES = [
    'svc-' + '\u{1F680}x'.repeat(98),
    'svc‮gnp.exe-' + 'y'.repeat(180),
    ...Array.from({ length: 10 }, (_, i) => long(`consumer-${i}`)),
  ];
  const VERDICTS = ['incompatible', 'unknown', 'compatible'];
  const consumer = (name: string, i: number) => {
    const key = `domain-load/${name}`;
    return {
      service: { kind: 'service', key, label: name, domain: 'domain-load', href: `/fleet/services/${encodeURIComponent(key)}` },
      direct: true, depth: 1, confidence: 'declared',
      compatibilityVerdict: VERDICTS[i % 3], compatibility: '^' + long(`1.${i}.0`).slice(1),
      blocking: i === 0, owner: long(`team-${i}`),
      path: [], pathTotal: 0, pathTruncated: false,
    };
  };
  const wrapped: Serve = async (method, path, body) => {
    const res = await (real as Serve)(method, path, body);
    if (method !== 'POST' || path.split('?')[0] !== '/api/fleet/impact' || res.status !== 200) return res;
    const doc = JSON.parse(res.body);
    const all = NAMES.map(consumer);
    doc.consumers = {
      ...doc.consumers,
      items: all.slice(0, 10), highlights: all.slice(0, 8),
      total: 12, direct: 12, count: 10, offset: 0, limit: 10, nextOffset: 10, truncated: true, hidden: 3,
    };
    doc.meta = { ...doc.meta, completeness: 'partial' };
    return { ...res, body: JSON.stringify(doc) };
  };
  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}

for (const theme of ['dark', 'light'] as const) {
  test.describe(`isometric impact (${theme})`, () => {
    test.use({ colorScheme: theme, viewport: { width: 1280, height: 900 } });
    test.beforeEach(async ({ page }) => {
      if (theme === 'light') {
        await page.addInitScript(() => { try { localStorage.setItem('pacto-theme', 'light'); } catch { /* private mode */ } });
      }
    });

    test('draws the breaking pair, settles for axe and fits at 1280 and 1024', async ({ page }, testInfo) => {
      test.setTimeout(90_000);
      await analyse(page);
      const svg = page.locator(`${FIGURE} svg[role="img"]`);
      const label = (await svg.getAttribute('aria-label')) ?? '';
      expect(label).toContain('breaking');
      const names = await page.locator(`${FIGURE} [data-verdict] text.em`).allTextContents();
      expect(names.length).toBeGreaterThan(0);
      // The drawn label may be cut; the aria-label carries the full name, so its visible
      // stem (before any ellipsis) must be in it.
      for (const n of names) expect(label).toContain(n.replace(/…$/, ''));
      await expect(page.locator(`${FIGURE} [data-verdict="incompatible"]`).first()).toBeVisible();
      const blocked = await page.getByText('Blocked', { exact: true }).count();
      await expect(page.locator(`${FIGURE} .ring`)).toHaveCount(blocked ? 1 : 0);
      await expect(page.locator(`${FIGURE} .stamp text`)).toContainText('BREAKING');

      await settle(page);
      const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
      expect(results.violations, JSON.stringify(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))).toEqual([]);

      await assertLayout(page, '1280');
      await testInfo.attach(`iso-impact-${theme}-1280.png`, { body: await page.locator(FIGURE).screenshot(), contentType: 'image/png' });
      await page.setViewportSize({ width: 1024, height: 900 });
      await assertLayout(page, '1024');
      await testInfo.attach(`iso-impact-${theme}-1024.png`, { body: await page.locator(FIGURE).screenshot(), contentType: 'image/png' });
    });

    test('"more" opens the consumers table below the sticky navbar', async ({ page }) => {
      await analyse(page);
      await page.getByTestId('iso-impact-more').click();
      const fold = page.locator('#sec-affected-consumers');
      await expect(fold).toHaveAttribute('open', '');
      const summary = fold.locator('> summary');
      await expect(summary).toBeFocused();
      // WCAG 2.2 2.4.11: the focused summary is not hidden under the sticky navbar.
      await expect.poll(() => page.evaluate(() => {
        const s = document.querySelector('#sec-affected-consumers > summary')!.getBoundingClientRect();
        const nav = document.querySelector('.navbar')!.getBoundingClientRect();
        return s.top >= nav.bottom && s.bottom <= window.innerHeight;
      })).toBe(true);
    });

    test('is static under a reduced-motion preference', async ({ page }) => {
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await analyse(page);
      const durations = await page.evaluate((sel) => {
        const worst = (v: string) => Math.max(...v.split(',').map((p) => parseFloat(p) || 0));
        return Array.from(document.querySelectorAll(`${sel} [data-motion]`)).map((el) => {
          const cs = getComputedStyle(el);
          return { tag: el.getAttribute('class'), t: worst(cs.animationDuration) + Math.max(0, worst(cs.animationDelay)) };
        });
      }, FIGURE);
      expect(durations.length).toBeGreaterThan(0);
      expect(durations.filter((d) => d.t > 0.001)).toEqual([]);
      await page.waitForTimeout(300);
      const running = await page.evaluate((sel) => document.getAnimations()
        .filter((a) => a.playState === 'running')
        .filter((a) => ((a.effect as KeyframeEffect | null)?.target as Element | null)?.closest(sel)).length, FIGURE);
      expect(running).toBe(0);
      const blocked = await page.getByText('Blocked', { exact: true }).count();
      await expect(page.locator(`${FIGURE} .ring`)).toHaveCount(blocked ? 1 : 0);
      // Reduced motion collapses travel, never the rest state: the ring stays drawn at its
      // resting opacity and nothing is left mid-transform.
      const rest = await page.evaluate((sel) => Array.from(document.querySelectorAll(`${sel} .ring, ${sel} .stamp-in, ${sel} .c`))
        .map((el) => { const cs = getComputedStyle(el); return { cls: el.getAttribute('class'), opacity: cs.opacity, transform: cs.transform }; }), FIGURE);
      for (const r of rest) {
        expect(r.transform, r.cls ?? '').toBe('none');
        expect(Number(r.opacity), r.cls ?? '').toBeGreaterThan(0);
      }
    });

    test('labels sit on the page ground their halo assumes', async ({ page }) => {
      await analyse(page);
      // The label halo strokes with --c-bg; it only hides wires if the figure's nearest
      // painted ancestor is that same ground.
      const { ground, page: pageBg } = await page.evaluate((sel) => {
        let el: Element | null = document.querySelector(sel);
        let bg = 'rgba(0, 0, 0, 0)';
        while (el && (bg === 'rgba(0, 0, 0, 0)' || bg === 'transparent')) {
          bg = getComputedStyle(el).backgroundColor;
          el = el.parentElement;
        }
        const probe = document.createElement('div');
        probe.style.backgroundColor = 'var(--c-bg)';
        document.body.appendChild(probe);
        const want = getComputedStyle(probe).backgroundColor;
        probe.remove();
        return { ground: bg, page: want };
      }, FIGURE);
      expect(ground).toBe(pageBg);
    });

    test('twelve consumers with hostile 200-character identities stay bounded', async ({ page }) => {
      test.setTimeout(90_000);
      await page.addInitScript(installImpactScale);
      await analyse(page);
      await expect(page.locator(`${FIGURE} [data-verdict]`)).toHaveCount(8);
      await expect(page.getByTestId('iso-impact-more')).toContainText('4 more in Affected consumers');
      await expect(page.getByTestId('iso-impact-hidden')).toHaveText('3 consumers you may not see');
      const label = (await page.locator(`${FIGURE} svg`).getAttribute('aria-label')) ?? '';
      expect(label).toContain('12 direct');
      expect(label).toContain('evidence partial');
      expect(label).toContain('3 you may not see');
      await assertLayout(page, 'scale 1280');
      await page.setViewportSize({ width: 1024, height: 900 });
      await assertLayout(page, 'scale 1024');
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
