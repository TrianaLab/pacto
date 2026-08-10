import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

/**
 * Exhaustive heading / landmark / page-title audit over EVERY canonical Fleet
 * route and the retained non-Fleet compatibility surface (Phase 5, requirement
 * 8.1).
 *
 * The general axe gate in `axe.spec.ts` runs the full WCAG A/AA rule set over a
 * REPRESENTATIVE set of states. That is the right shape for contrast and widget
 * semantics, which vary by state rather than by route. Document structure is the
 * opposite: it varies by ROUTE, and a route that nobody sampled is exactly where a
 * skipped heading level survives. So this spec trades rule breadth for route
 * coverage — a small structural rule set, run everywhere.
 *
 * Per route it asserts:
 *   1. exactly one non-empty h1 inside `main` (the page names itself),
 *   2. no skipped heading level anywhere in `main`,
 *   3. `document.title` names the page, and is not the same generic string on
 *      every route,
 *   4. exactly one main landmark and exactly one banner,
 *   5. every navigation landmark has an accessible name when there is more than
 *      one (otherwise "navigation" is announced twice with no way to tell them
 *      apart),
 *   6. the axe structural rules agree.
 */

// Structure-only rule set. Deliberately NOT the whole WCAG suite: contrast and
// widget rules are axe.spec.ts's job, and running them here would make a route
// sweep slow enough that nobody adds routes to it.
const STRUCTURE_RULES = [
  'page-has-heading-one',
  'heading-order',
  'empty-heading',
  'document-title',
  'landmark-one-main',
  'landmark-no-duplicate-main',
  'landmark-no-duplicate-banner',
  'landmark-no-duplicate-contentinfo',
  'landmark-banner-is-top-level',
  'landmark-unique',
  'bypass',
];

interface Structure {
  title: string;
  h1s: string[];
  levels: number[];
  mains: number;
  banners: number;
  navs: Array<string | null>;
}

async function structure(page: Page): Promise<Structure> {
  const doc = await page.evaluate(() => {
    const main = document.querySelector('main');
    const text = (e: Element) => (e.textContent || '').replace(/\s+/g, ' ').trim();
    const hs = Array.from(main?.querySelectorAll('h1,h2,h3,h4,h5,h6') ?? []);
    return {
      title: document.title,
      h1s: hs.filter((e) => e.tagName === 'H1').map(text),
      levels: hs.map((e) => Number(e.tagName.slice(1))),
    };
  });
  // Landmarks go through Playwright's ROLE engine, not a tag selector: `<header>` is a
  // banner only when it is not inside sectioning content, and role="navigation" counts
  // as much as `<nav>`. Reimplementing that in a querySelector is how an audit ends up
  // asserting something subtly different from what assistive tech computes.
  const navs = await page.getByRole('navigation').all();
  return {
    ...doc,
    mains: await page.getByRole('main').count(),
    banners: await page.getByRole('banner').count(),
    navs: await Promise.all(
      navs.map((n) =>
        n.evaluate((el) => {
          const by = el.getAttribute('aria-labelledby');
          const byEl = by ? document.getElementById(by) : null;
          return el.getAttribute('aria-label') || (byEl ? (byEl.textContent || '').trim() : null);
        }),
      ),
    ),
  };
}

/** firstSkip returns the first illegal level jump, or null. Going UP is always fine. */
function firstSkip(levels: number[]): string | null {
  for (let i = 1; i < levels.length; i++) {
    if (levels[i] > levels[i - 1] + 1) return `h${levels[i - 1]} -> h${levels[i]} (at heading ${i + 1})`;
  }
  return null;
}

async function assertStructure(page: Page, label: string): Promise<Structure> {
  const s = await structure(page);

  expect(s.h1s, `${label}: expected exactly one h1 in main, got ${JSON.stringify(s.h1s)}`).toHaveLength(1);
  expect(s.h1s[0].length, `${label}: the h1 is empty`).toBeGreaterThan(0);

  expect(firstSkip(s.levels), `${label}: skipped heading level, levels=${s.levels.join(',')}`).toBeNull();

  // The title must NAME the page. It used to be the literal string "Pacto Dashboard"
  // on every single route, which is a WCAG 2.4.2 failure and makes ten open tabs
  // indistinguishable.
  expect(s.title, `${label}: page title is the generic fallback`).not.toBe('Pacto Dashboard');
  expect(s.title, `${label}: page title does not contain the h1`).toContain(s.h1s[0]);

  expect(s.mains, `${label}: expected exactly one main landmark`).toBe(1);
  expect(s.banners, `${label}: expected exactly one banner landmark`).toBe(1);

  if (s.navs.length > 1) {
    expect(
      s.navs.filter((n) => !n),
      `${label}: ${s.navs.length} navigation landmarks and ${s.navs.filter((n) => !n).length} of them unnamed`,
    ).toHaveLength(0);
  }

  const results = await new AxeBuilder({ page }).withRules(STRUCTURE_RULES).analyze();
  expect(
    results.violations,
    `${label}: ${JSON.stringify(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`,
  ).toEqual([]);
  return s;
}

// A route sweep boots the WASM engine once per route, so the default per-test timeout
// does not fit. This is a budget for a many-route walk, not a latency assertion.
const SWEEP_TIMEOUT = 240_000;

let bootSeq = 0;

/**
 * boot loads `hash` as a genuine DEEP LINK: a fresh document, engine boot and first
 * render of that route.
 *
 * The cache-busting query string is load-bearing. `page.goto` to a URL that differs
 * only in its fragment is a same-document navigation, so the previous route's DOM --
 * including its h1 and its document.title -- survives the call, and every assertion
 * below it can pass against the page we already audited. Forcing a real navigation
 * also makes this an honest deep-link test: each route is entered cold, the way a
 * shared link enters it.
 */
async function boot(page: Page, hash: string) {
  await page.goto(`/index.html?boot=${++bootSeq}${hash}`);
  await page.waitForFunction(() => !document.body.textContent?.includes('Loading Pacto'), null, { timeout: 60_000 });
  // Wait for the page's own h1, not a fixed sleep: a detail route only grows its
  // heading once the entity request lands, and the title mirrors the heading.
  await expect(page.locator('main h1')).toBeVisible({ timeout: 30_000 });
}

/**
 * Canonical keys are DISCOVERED from the Product API rather than hardcoded, so this
 * spec keeps covering the real routes when the demo fixture changes.
 */
async function canonicalKeys(page: Page) {
  await boot(page, '#/fleet');
  return page.evaluate(async () => {
    const j = async (u: string) => (await fetch(u)).json();
    const first = async (kind: string) => {
      const r = await j(`/api/fleet/entities?kinds=${kind}&limit=1`);
      return (r.entities || [])[0]?.key || '';
    };
    return {
      service: 'payments-service',
      revision: await first('revision'),
      target: await first('target'),
      owner: await first('owner'),
      source: await first('source'),
    };
  });
}

test.describe('document structure on every canonical Fleet route', () => {
  test('every canonical product route', async ({ page }) => {
    test.setTimeout(SWEEP_TIMEOUT);
    const k = await canonicalKeys(page);
    const e = encodeURIComponent;

    // EVERY canonical route in section 3 of the redesign ledger. A route missing from
    // this list is a route nothing audits.
    const routes: Array<[string, string]> = [
      ['Overview', '#/fleet'],
      ['Services list', '#/fleet/services'],
      ['Service detail', `#/fleet/services/${e(k.service)}`],
      ['Revision detail', `#/fleet/revisions/${e(k.revision)}`],
      ['Target detail', `#/fleet/targets/${e(k.target)}`],
      ['Owners list', '#/fleet/owners'],
      ['Owner detail', `#/fleet/owners/${e(k.owner)}`],
      ['Data sources list', '#/fleet/sources'],
      ['Data source detail', `#/fleet/sources/${e(k.source)}`],
      ['Needs attention', '#/fleet/attention'],
      ['Revision inventory', '#/fleet/revisions'],
      ['Scoped revision inventory', `#/fleet/revisions?service=${e(k.service)}`],
      ['Target inventory', '#/fleet/targets'],
      ['Scoped target inventory', `#/fleet/targets?service=${e(k.service)}`],
      ['Graph discovery', '#/fleet/graph'],
      ['Graph focused', `#/fleet/graph/service/${e(k.service)}`],
      ['Change analysis (unscoped)', '#/fleet/changes'],
      ['Change analysis (scoped)', `#/fleet/changes/${e(k.service)}`],
    ];

    const titles: string[] = [];
    for (const [label, hash] of routes) {
      await boot(page, hash);
      titles.push((await assertStructure(page, label)).title);
    }

    // A sweep is only worth its runtime if it actually moved. Distinct titles are the
    // cheap proof that eighteen navigations produced eighteen pages rather than one page
    // audited eighteen times -- the exact failure mode a fragment-only goto introduces.
    // A few routes legitimately share a title (a scoped list is still that list, a
    // focused graph is still the graph), so the bar is "mostly distinct", not "all".
    expect(new Set(titles).size, `titles were not distinct enough: ${JSON.stringify(titles)}`)
      .toBeGreaterThanOrEqual(routes.length - 4);
  });

  test('an entity that does not exist still names itself', async ({ page }) => {
    // The honest not-found state is a page like any other: it needs an h1, a title and
    // a landmark structure. It is also the state most likely to be built out of a bare
    // empty-state component, which is exactly where a skipped heading level hides.
    await boot(page, '#/fleet/services/no-such-service-anywhere');
    await assertStructure(page, 'Service detail (not found)');
  });
});

test.describe('document structure on the retained non-Fleet compatibility surface', () => {
  // The offline `pacto doc` export has no Product API, so the legacy views are its only
  // UI and are retained deliberately. "Retained" has to mean accessible too.
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/capabilities', async (route) => {
      const res = await route.fetch();
      const body = await res.json().catch(() => ({}));
      await route.fulfill({ json: { ...body, fleet: false } });
    });
  });

  test('legacy routes', async ({ page }) => {
    test.setTimeout(SWEEP_TIMEOUT);
    await boot(page, '#/services');
    await assertStructure(page, 'Legacy services list');

    const name = await page.evaluate(async () => {
      const j = await (await fetch('/api/services')).json();
      return (j.services || j || [])[0]?.name || '';
    });
    test.skip(!name, 'no legacy service to open');

    for (const [label, hash] of [
      ['Legacy service detail', `#/services/${encodeURIComponent(name)}`],
      ['Legacy service compare', `#/services/${encodeURIComponent(name)}/diff`],
      ['Legacy owners', '#/owners'],
      ['Legacy graph', '#/graph'],
      ['Legacy readiness', '#/readiness'],
      ['Legacy standalone compare', '#/diff'],
    ] as Array<[string, string]>) {
      await boot(page, hash);
      await assertStructure(page, label);
    }

    // The legacy owner detail is only reachable by id, so take the id the list itself
    // links to rather than guessing one.
    await boot(page, '#/owners');
    const ownerLink = page.locator('a[href^="#/owners/"]').first();
    const ownerHref = (await ownerLink.count()) ? await ownerLink.getAttribute('href') : null;
    if (ownerHref) {
      await boot(page, ownerHref);
      await assertStructure(page, 'Legacy owner detail');
    }
  });
});
