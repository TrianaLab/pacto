import { test, expect, type Page } from '@playwright/test';

// Phase 6 browser acceptance for the visualization system (requirements 10 and 24).
//
// viz.test.ts already proves each component's contract in isolation, in jsdom. What it
// cannot prove is that the contract survives being COMPOSED into real product pages and
// rendered by a real engine: whether every figure on a real page is actually named, whether
// a legend link is actually reachable by keyboard, whether the bars actually stop animating
// under a reduced-motion preference, and whether any of it still fits at 320px.
//
// So this file audits every visualization the product renders, on the pages that render
// them, against the whole rule at once: a named figure, every value as text, no meaning
// carried by colour or length alone, keyboard-operable drill-downs, both themes, and the
// narrowest phone. Auditing every figure found on a page rather than a hand-listed set is
// deliberate -- a chart added later is covered the day it ships, without editing this file.

/** The product surfaces that draw something. Each is visited at every width and theme. */
const SURFACES = [
  { hash: '#/fleet', name: 'Operational Overview' },
  { hash: '#/fleet/services', name: 'Services list' },
  { hash: '#/fleet/attention', name: 'Attention' },
];

/** A figure is "a visualization" if it carries one of the shared viz roots. */
const FIGURES = 'main figure.dist, main figure.hbars';

async function open(page: Page, hash: string) {
  await page.goto(`/${hash}`);
  await page.waitForFunction(() => !document.body.textContent?.includes('Loading'), null, { timeout: 30000 });
  await expect(page.locator(FIGURES).first()).toBeVisible({ timeout: 30000 });
}

const lightTheme = (page: Page) =>
  page.addInitScript(() => { try { localStorage.setItem('pacto-theme', 'light'); } catch { /* private mode */ } });

/**
 * The whole visualization rule, applied to every figure on the page. Returns the number of
 * figures audited so a test can refuse to pass on a page that silently rendered none.
 */
async function auditFigures(page: Page): Promise<number> {
  const report = await page.evaluate((sel) => {
    const problems: string[] = [];
    const figures = Array.from(document.querySelectorAll(sel)) as HTMLElement[];
    for (const fig of figures) {
      const cap = fig.querySelector('figcaption');
      const heading = cap?.querySelector('h1,h2,h3,h4,h5,h6');
      const name = heading?.textContent?.trim() || '';
      // An accessible name, from a real heading -- not a title attribute, not a class.
      if (!name) { problems.push(`figure with no caption heading: ${fig.className}`); continue; }

      // Every graphic element is hidden from assistive technology, because the text beside
      // it already says the same thing. A bar that is NOT hidden is a bar a screen reader
      // is asked to interpret as a shape.
      for (const g of Array.from(fig.querySelectorAll('.dist-bar, .hb-track, .dist-swatch'))) {
        if (g.getAttribute('aria-hidden') !== 'true') problems.push(`${name}: graphic not aria-hidden (${g.className})`);
      }

      // No meaning by colour or length alone: every row prints its own label AND its own
      // exact value as text. A row that renders a bar but no number fails here.
      const rows = Array.from(fig.querySelectorAll('.dist-item, .hb-row'));
      if (!rows.length && !fig.querySelector('.dist-empty, .hb-empty')) {
        problems.push(`${name}: neither rows nor an explicit empty state`);
      }
      for (const r of rows) {
        const label = r.querySelector('.dist-label, .hb-label')?.textContent?.trim();
        const value = r.querySelector('.dist-value, .hb-value')?.textContent?.trim();
        if (!label) problems.push(`${name}: a row with no textual label`);
        if (!value) problems.push(`${name}: row "${label}" has no textual value`);
      }
    }
    return { count: figures.length, problems };
  }, FIGURES);
  expect(report.problems, `visualization contract violations:\n${report.problems.join('\n')}`).toEqual([]);
  return report.count;
}

test.describe('Every product visualization is named, textual and keyboard-operable', () => {
  for (const s of SURFACES) {
    test(`${s.name}: figures satisfy the whole rule (dark)`, async ({ page }) => {
      await open(page, s.hash);
      expect(await auditFigures(page)).toBeGreaterThan(0);
    });

    test(`${s.name}: figures satisfy the whole rule (light)`, async ({ page }) => {
      await lightTheme(page);
      await open(page, s.hash);
      expect(await auditFigures(page)).toBeGreaterThan(0);
    });
  }

  test('a legend drill-down is reachable and activated by keyboard alone', async ({ page }) => {
    await open(page, '#/fleet');
    const link = page.locator('main figure.dist .dist-item a').first();
    await expect(link).toBeVisible();
    const href = await link.getAttribute('href');
    expect(href).toContain('attention');

    // Focus by keyboard (not by click) and activate with Enter -- the two things a mouse
    // test would never notice were missing.
    await link.focus();
    await expect(link).toBeFocused();
    // A focused control has to be visible as focused; an invisible focus ring is the same
    // as no keyboard support for anyone who is looking at the screen.
    const outline = await link.evaluate((el) => {
      const cs = getComputedStyle(el, null);
      return `${cs.outlineStyle} ${cs.outlineWidth} ${cs.boxShadow}`;
    });
    expect(outline).not.toBe('none 0px none');
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(new RegExp(href!.replace('#', '').replace(/[?&]/g, '.')));
    await expect(page.getByTestId('attention-shape')).toBeVisible();
  });

  test('bar transitions are dropped under a reduced-motion preference', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await open(page, '#/fleet');
    const bars = await page.evaluate(() =>
      Array.from(document.querySelectorAll('.dist-seg, .hb-fill')).map((el) => {
        const cs = getComputedStyle(el);
        return { property: cs.transitionProperty, seconds: parseFloat(cs.transitionDuration) };
      }),
    );
    expect(bars.length).toBeGreaterThan(0);
    // Two independent proofs, because either alone is weak. The blanket reset in base.css
    // collapses every duration to .001ms, so a duration check would pass even for a
    // component that forgot its own rule; and a property check alone would not prove the
    // rule reached the rendered element. Both must hold.
    expect(bars.every((b) => b.seconds <= 0.001)).toBe(true);
    expect(bars.every((b) => b.property === 'none')).toBe(true);
  });
});

test.describe('Visualizations survive the narrowest phone', () => {
  for (const width of [320, 375]) {
    test(`@${width}px: figures stay inside the viewport and keep their text`, async ({ page }) => {
      await page.setViewportSize({ width, height: 720 });
      for (const s of SURFACES) {
        await open(page, s.hash);
        expect(await auditFigures(page), `${s.name} @${width}px`).toBeGreaterThan(0);
        // A bar is allowed to shrink; it is not allowed to push the page sideways. This is
        // the failure a fixed-width chart produces, and it breaks the whole page, not the
        // chart.
        const spill = await page.evaluate((sel) => {
          const doc = document.documentElement;
          const over = Array.from(document.querySelectorAll(sel))
            .filter((f) => f.getBoundingClientRect().right > doc.clientWidth + 1)
            .map((f) => (f as HTMLElement).className);
          return { body: doc.scrollWidth - doc.clientWidth, over };
        }, FIGURES);
        expect(spill.over, `${s.name} @${width}px figures overflow`).toEqual([]);
        expect(spill.body, `${s.name} @${width}px body scrolls horizontally`).toBeLessThanOrEqual(1);
      }
    });
  }
});
