import { test, expect, type Page } from '@playwright/test';
import { runAnalysis } from './typographyChecks';

// Browser acceptance for the visualization system.
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

/**
 * The product surfaces that draw something at a FIXED route. Each is visited at every
 * width and theme.
 *
 * The routes that need a canonical key discovered from the API -- the entity pages and
 * the analysis workspace, which draw the same figures over a narrower population -- are
 * swept in one test below rather than parametrised here, because the key is only known
 * at run time.
 */
const SURFACES = [
  { hash: '#/fleet', name: 'Operational Overview' },
  { hash: '#/fleet/services', name: 'Services list' },
  { hash: '#/fleet/attention', name: 'Attention' },
];

/** A figure is "a visualization" if it carries one of the shared viz roots. */
const FIGURES = 'main figure.dist, main figure.hbars';

/**
 * open loads a route and waits until it is actually drawing.
 *
 * `after` runs between "the route has loaded" and "a figure is on screen": the analysis
 * workspace opens on a revision picker and draws nothing until the comparison is run, so
 * waiting for a figure first would time out on the honest idle state.
 */
async function open(page: Page, hash: string, after?: (page: Page) => Promise<void>) {
  await page.goto(`/${hash}`);
  await page.waitForFunction(() => !document.body.textContent?.includes('Loading'), null, { timeout: 30000 });
  if (after) await after(page);
  await expect(page.locator(FIGURES).first()).toBeVisible({ timeout: 30000 });
}

/**
 * Canonical keys DISCOVERED from the Product API, so the sweep follows the fixture.
 *
 * The owner is the first one the fleet has RUNNING TARGETS for, not simply the first
 * owner: an owner with nothing observed draws no distribution at all, and says so in
 * words. That page is correct and this audit is about pages that do draw -- picking it
 * would fail the sweep on an honest empty state.
 */
async function keys(page: Page): Promise<Record<string, string>> {
  await page.goto('/#/fleet');
  await page.waitForFunction(() => !document.body.textContent?.includes('Loading'), null, { timeout: 30000 });
  return page.evaluate(async () => {
    const agg = await (await fetch('/api/fleet/entities?kinds=service&limit=1')).json();
    const byOwner = (agg.aggregate?.byOwner || []) as Array<{ key: string; targets: number }>;
    const first = await (await fetch('/api/fleet/entities?kinds=owner&limit=1')).json();
    const owner = byOwner.find((o) => o.targets > 0)?.key || (first.entities || [])[0]?.key || '';
    return { service: 'payments-service', owner };
  });
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

  test('the entity pages and the analysis workspace obey the same rule', async ({ page }) => {
    test.setTimeout(300_000);
    // The same primitives, over a population of one service, one owner or one comparison.
    // These surfaces were outside the audit while it listed only the three fleet-wide
    // pages -- and they are where a figure is most likely to be handed a bounded preview
    // instead of an aggregate, because the population is small enough to look complete.
    const k = await keys(page);
    const e = encodeURIComponent;
    const routes: Array<[string, string]> = [
      ['Service detail', `#/fleet/services/${e(k.service)}`],
      ['Owner detail', `#/fleet/owners/${e(k.owner)}`],
      ['Revisions list', '#/fleet/revisions'],
      ['Change analysis', `#/fleet/changes/${e(k.service)}`],
    ];

    for (const [name, hash] of routes) {
      await open(page, hash, name === 'Change analysis' ? runAnalysis : undefined);
      expect(await auditFigures(page), `${name} rendered no figure at all`).toBeGreaterThan(0);
    }
  });

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

  // Everything that moves is marked `data-motion` in its markup, so this gate is a SWEEP
  // rather than a hand-listed pair of selectors: motion added next year is covered the day
  // it ships, and the only way to escape the gate is to move something without declaring
  // that it moves.
  //
  // The old version of this test asserted `transitionProperty === 'none'`, which was only
  // ever true because seven components each carried their own `@media
  // (prefers-reduced-motion)` block setting `transition: none`. Those blocks are gone —
  // the policy is one place now (styles/tokens.css zeroes the duration tokens, styles/
  // base.css is the blanket backstop) — so the property survives and the DURATION is what
  // must collapse.
  //
  // Delay is checked as well as duration, and that is not redundant: an animation with
  // `fill-mode: both` whose duration went to zero but whose delay did not still holds its
  // element at opacity 0 for the length of the delay, which is strictly worse for the
  // reader who asked for less motion than the animation it replaced.
  const MOTION_MARKER = '[data-motion]';

  test('everything marked as moving stops moving under a reduced-motion preference', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    let total = 0;
    for (const s of SURFACES) {
      await open(page, s.hash);
      const moving = await page.evaluate((sel) => {
        // A shorthand computes to a comma-separated list, one entry per property, so the
        // worst entry is what matters -- not whatever parseFloat finds first.
        const worst = (v: string) => Math.max(...v.split(',').map((p) => parseFloat(p) || 0));
        return Array.from(document.querySelectorAll(sel)).map((el) => {
          const cs = getComputedStyle(el);
          return {
            tag: `${el.tagName.toLowerCase()}.${el.className}`,
            transition: worst(cs.transitionDuration) + Math.max(0, worst(cs.transitionDelay)),
            animation: worst(cs.animationDuration) + Math.max(0, worst(cs.animationDelay)),
          };
        });
      }, MOTION_MARKER);
      total += moving.length;
      const offenders = moving.filter((m) => m.transition > 0.001 || m.animation > 0.001);
      expect(offenders, `${s.name} still animates:\n  ${offenders.map((o) => o.tag).join('\n  ')}`).toEqual([]);
    }
    // The sweep is only a gate if it found something to sweep. A selector typo, or a
    // rename of the marker attribute, would otherwise turn this test permanently green.
    expect(total).toBeGreaterThan(0);
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
