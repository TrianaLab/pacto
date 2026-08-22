import { test, expect, type Page } from '@playwright/test';
import { boot, canonicalKeys, runAnalysis } from './typographyChecks';

/**
 * Page-scaffold acceptance in real Chromium, from MEASURED bounding boxes and computed
 * styles.
 *
 * The Operational graph and Change analysis are the product's two workspaces, and they
 * sat at visibly different distances from the app bar with differently-framed control
 * panels. A source scan cannot prove that fixed. Every page "used the scaffold" in
 * source terms both before and after; what differed was where the boxes landed, and
 * only the browser knows that.
 *
 * It also cannot be proved by asserting pixel constants. `page title starts 88px below
 * main` would break on the next deliberate spacing change and would still not say that
 * the two workspaces AGREE. So every claim here is a comparison between routes measured
 * in the same run: same offset from the app bar, same content column, same rhythm from
 * breadcrumbs to title to first block, same panel.
 *
 * The reference set is deliberately the whole product, not just the two workspaces. Two
 * pages can agree with each other and disagree with the other nine -- that is how the
 * mismatch arose in the first place, from eight private copies of one page shell that
 * had drifted to two different gap values.
 */

// One WASM boot per route; a budget for a multi-route walk, not a latency assertion.
const SWEEP_TIMEOUT = 300_000;

interface Panel {
  top: number;
  left: number;
  width: number;
  padding: string;
  radius: string;
  border: string;
  background: string;
}

interface Geometry {
  /** The page root's inset inside `main` -- the app bar to first-content distance. */
  insetTop: number;
  insetLeft: number;
  /** The content column the page is laid out in. */
  width: number;
  /** Breadcrumbs bottom to page title top, and page title bottom to first block top. */
  crumbsToTitle: number;
  titleToBody: number;
  /** The shared control panel, where the route has one. */
  panel: Panel | null;
}

/**
 * measure reads the page's real geometry.
 *
 * Everything is expressed RELATIVE to `main` or to the element above it, so the numbers
 * survive scrolling, a different viewport height and any change to the app bar. They are
 * rounded to whole pixels: sub-pixel layout noise differs between a box laid out after a
 * one-line breadcrumb trail and one after a two-line trail, and a half-pixel is not the
 * defect this is looking for.
 */
async function measure(page: Page): Promise<Geometry> {
  return page.evaluate(() => {
    const px = (n: number) => Math.round(n);
    const main = document.querySelector('main');
    const root = document.querySelector('main .product-page');
    if (!main || !root) throw new Error('no product page shell on this route');

    const mainBox = main.getBoundingClientRect();
    const rootBox = root.getBoundingClientRect();
    const crumbs = root.querySelector(':scope > .breadcrumbs');
    const header = root.querySelector(':scope > .page-hd');
    if (!crumbs || !header) throw new Error('product page without breadcrumbs or shared header');

    // The first block of actual page content: whatever the shared header hands off to.
    const body = header.nextElementSibling;
    if (!body) throw new Error('product page with a header and nothing under it');

    const crumbsBox = crumbs.getBoundingClientRect();
    const headerBox = header.getBoundingClientRect();
    const bodyBox = body.getBoundingClientRect();

    const controls = root.querySelector('.workspace-controls');
    let panel: Panel | null = null;
    if (controls) {
      const cs = getComputedStyle(controls);
      const box = controls.getBoundingClientRect();
      panel = {
        top: px(box.top - headerBox.bottom),
        left: px(box.left - rootBox.left),
        width: px(box.width),
        padding: [cs.paddingTop, cs.paddingRight, cs.paddingBottom, cs.paddingLeft].join(' '),
        radius: cs.borderRadius,
        border: `${cs.borderTopWidth} ${cs.borderTopStyle} ${cs.borderTopColor}`,
        background: cs.backgroundColor,
      };
    }

    return {
      insetTop: px(rootBox.top - mainBox.top),
      insetLeft: px(rootBox.left - mainBox.left),
      width: px(rootBox.width),
      crumbsToTitle: px(headerBox.top - crumbsBox.bottom),
      titleToBody: px(bodyBox.top - headerBox.bottom),
      panel,
    };
  });
}

/** Route label -> geometry, so a failure names the page that drifted. */
type Sweep = Array<[string, Geometry]>;

const show = (s: Sweep, pick: (g: Geometry) => unknown) =>
  s.map(([label, g]) => `${label}: ${JSON.stringify(pick(g))}`).join('\n  ');

/** Every route must agree on `pick`, and the failure must say which one did not. */
function agree(s: Sweep, what: string, pick: (g: Geometry) => unknown): void {
  const values = [...new Set(s.map(([, g]) => JSON.stringify(pick(g))))];
  expect(values, `${what} differs between product pages:\n  ${show(s, pick)}`).toHaveLength(1);
}

test.describe('page scaffold geometry on desktop', () => {
  test('every product page, and both workspaces, sit on one scaffold', async ({ page }) => {
    test.setTimeout(SWEEP_TIMEOUT);
    const k = await canonicalKeys(page);
    const e = encodeURIComponent;

    // The two workspaces LAST, so that a failure message reads as "the product agrees on
    // X, and the workspace does not" rather than the other way round.
    const routes: Array<[string, string]> = [
      ['Overview', '#/fleet'],
      ['Services list', '#/fleet/services'],
      ['Service detail', `#/fleet/services/${e(k.service)}`],
      ['Revision detail', `#/fleet/revisions/${e(k.revision)}`],
      ['Owners list', '#/fleet/owners'],
      ['Data sources list', '#/fleet/sources'],
      ['Needs attention', '#/fleet/attention'],
      ['Graph discovery', '#/fleet/graph'],
      ['Graph focused', `#/fleet/graph/service/${e(k.service)}`],
      ['Change analysis', `#/fleet/changes/${e(k.service)}`],
      ['Change analysis (analysed)', `#/fleet/changes/${e(k.service)}`],
    ];

    const sweep: Sweep = [];
    for (const [label, hash] of routes) {
      await boot(page, hash);
      if (label.endsWith('(analysed)')) await runAnalysis(page);
      sweep.push([label, await measure(page)]);
    }

    // Non-vacuity: the sweep must actually have visited both workspaces, or the
    // agreement below is agreement among the pages that were never in dispute.
    expect(sweep.map(([l]) => l)).toEqual(expect.arrayContaining([
      'Graph discovery', 'Graph focused', 'Change analysis', 'Change analysis (analysed)',
    ]));

    // The claim: one distance from the app bar, one content column, one rhythm.
    agree(sweep, 'the distance from the app bar to the start of the page', (g) => g.insetTop);
    agree(sweep, 'the left edge of the content column', (g) => g.insetLeft);
    agree(sweep, 'the width of the content column', (g) => g.width);
    agree(sweep, 'the gap between breadcrumbs and the page title', (g) => g.crumbsToTitle);
    agree(sweep, 'the gap between the page title and the first block of content', (g) => g.titleToBody);
  });

  test('both workspaces frame their controls with the same panel', async ({ page }) => {
    test.setTimeout(SWEEP_TIMEOUT);
    const k = await canonicalKeys(page);
    const e = encodeURIComponent;

    // A workspace is a page with controls over a result. There are exactly two, and the
    // panel is the thing a reader recognises as "this is where I drive this screen".
    const workspaces: Array<[string, string]> = [
      ['Graph focused', `#/fleet/graph/service/${e(k.service)}`],
      ['Change analysis', `#/fleet/changes/${e(k.service)}`],
    ];

    const sweep: Sweep = [];
    for (const [label, hash] of workspaces) {
      await boot(page, hash);
      const g = await measure(page);
      expect(g.panel, `${label} has no control panel; a workspace without controls is not a workspace`).not.toBeNull();
      sweep.push([label, g]);
    }

    agree(sweep, 'control panel padding', (g) => g.panel!.padding);
    agree(sweep, 'control panel corner radius', (g) => g.panel!.radius);
    agree(sweep, 'control panel border', (g) => g.panel!.border);
    agree(sweep, 'control panel background', (g) => g.panel!.background);
    agree(sweep, 'control panel width', (g) => g.panel!.width);
    agree(sweep, 'control panel offset inside the content column', (g) => g.panel!.left);
    agree(sweep, 'the gap between the page title and the control panel', (g) => g.panel!.top);

    // The panel is a real frame, not an unstyled div that happens to match another
    // unstyled div. Without this, deleting the shared rule would make both workspaces
    // agree perfectly on nothing.
    const [, first] = sweep[0];
    expect(first.panel!.border, 'the shared control panel must actually be framed').not.toMatch(/^0px/);
    expect(first.panel!.padding, 'the shared control panel must actually be padded').not.toMatch(/^0px 0px 0px 0px$/);
  });
});
