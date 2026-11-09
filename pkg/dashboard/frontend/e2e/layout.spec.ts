import { test, expect, type Page } from '@playwright/test';
import { appReady, settle } from './ready';
import { densityProblems, figureLinkTotals, gateMode, installScaleFixture, layoutProblems, sweepOverflow } from './layoutGate';

// G1, the transversal layout gate (SPEC sections 12 and 22): every route, at 390, 760,
// 1024, 1280, 1600, 1920 and 2560px, in both themes, with Verdana forced (CI renders DejaVu, wider than Inter),
// the final frame forced, on the scale fixture. It fails on intersecting text, two text
// boxes closer than 8px on one line, a box spilling with visible overflow, text under
// 12.19px, ellipsized text with no title, text cut with no ellipsis, an identity that
// is not an elision of its title (or an elided one with no title), a lone surrogate,
// horizontal page scroll, more than one element carrying an err box-shadow ring, and
// the figure rules once figures exist: at most 20 marks, no mark over text, every
// number a filter link or data-nofilter, every "List all N" landing on a list of N and
// every "N more" on N plus the names shown, and on list views L3: the list within 600px
// of the top or a Compact toggle on the figure, and (SPEC 22 r2) no word squeezed to one
// or two characters per line and no table column under 6ch. Then it sweeps every 20px
// from 760 to 1440 and every 40px on to 1920 for horizontal overflow (SPEC 19, 22),
// since a layout clean at the fixed widths spilled between them. Both sidebar states
// are gated (SPEC 20): from 1200px each fixed width is checked with the sidebar as it
// starts and again with it toggled, and the sweep runs once with the default states
// (collapsed under 1440px) and once with the sidebar expanded from 1200px.
//
// PENDING names subtrees a later phase rewrites. Each entry is deleted by that phase;
// nothing else is excluded.
const PENDING: string[] = [];

const WIDTHS = [390, 760, 1024, 1280, 1600, 1920, 2560];

async function chooseRevision(page: Page, sel: string, version: string) {
  const read = () => page.locator(`${sel} option`).evaluateAll(
    (os, v) => (os as HTMLOptionElement[]).find((o) => (o.textContent ?? '').startsWith(`${v} `))?.value ?? '', version);
  await expect.poll(read, { message: `no ${version} revision in ${sel}`, timeout: 20_000 }).not.toBe('');
  await page.locator(sel).selectOption(await read());
}

/** Opens the first entity link of a kind on a list route: keys come from the engine. */
const firstLink = (list: string, href: string) => async (page: Page) => {
  await page.goto(`/${list}`);
  const a = page.locator(`main a[href*="${href}"]`).first();
  await expect(a).toBeVisible({ timeout: 20_000 });
  await page.goto(`/${await a.getAttribute('href')}`);
};

/** Opens a service created from a golden path, through the path's own created count. */
const createdService = async (page: Page) => {
  await page.goto('/#/fleet/golden-paths');
  const created = page.locator('[data-testid="path-created"]').first();
  await expect(created).toBeVisible({ timeout: 20_000 });
  await page.goto(`/${await created.getAttribute('href')}`);
  const a = page.locator('[data-testid="service-list"] a[href*="/fleet/services/"]').first();
  await expect(a).toBeVisible({ timeout: 20_000 });
  await page.goto(`/${await a.getAttribute('href')}`);
};

// `list` marks list views, where L3 holds the list within 600px or the figure folds.
type Route = { name: string; hash?: string; list?: boolean; open?: (page: Page) => Promise<void> };
const ROUTES: Route[] = [
  { name: 'overview', hash: '#/fleet' },
  { name: 'services', list: true, hash: '#/fleet/services' },
  { name: 'owners', list: true, hash: '#/fleet/owners' },
  { name: 'domains', list: true, hash: '#/fleet/domains' },
  { name: 'offerings', list: true, hash: '#/fleet/offerings' },
  { name: 'sources', list: true, hash: '#/fleet/sources' },
  { name: 'attention', list: true, hash: '#/fleet/attention' },
  { name: 'graph discovery', hash: '#/fleet/graph' },
  { name: 'graph focused', hash: '#/fleet/graph/service/payments-service' },
  { name: 'change analysis idle', hash: '#/fleet/changes/payments-service' },
  {
    name: 'change analysis answered',
    open: async (page) => {
      await page.goto('/#/fleet/changes/payments-service');
      await chooseRevision(page, '#impact-old-rev', '1.0.0');
      await chooseRevision(page, '#impact-new-rev', '2.0.1');
      await page.getByRole('button', { name: /Compare revisions/ }).click();
      await expect(page.locator('#sec-affected-consumers')).toBeVisible({ timeout: 30_000 });
    },
  },
  { name: 'service entity', hash: '#/fleet/services/payments-service' },
  { name: 'revision list', list: true, hash: '#/fleet/revisions?service=payments-service' },
  { name: 'revision entity', open: firstLink('#/fleet/revisions?service=payments-service', '/fleet/revisions/') },
  { name: 'target list', list: true, hash: '#/fleet/targets' },
  { name: 'target entity', open: firstLink('#/fleet/targets', '/fleet/targets/') },
  { name: 'owner entity', hash: '#/fleet/owners/team:catalog-team' },
  { name: 'domain entity', hash: '#/fleet/domains/commerce' },
  { name: 'offering entity', hash: '#/fleet/offerings/messaging' },
  { name: 'source entity', hash: '#/fleet/sources/edge-cluster' },
  { name: 'golden paths', list: true, hash: '#/fleet/golden-paths' },
  { name: 'golden path entity', open: firstLink('#/fleet/golden-paths?sort=name', '/fleet/services/') },
  { name: 'service created from a golden path', open: createdService },
  { name: 'delivery inbox', list: true, hash: '#/delivery/inbox' },
  { name: 'delivery environments', list: true, hash: '#/delivery/environments' },
  { name: 'delivery environments of one service', hash: '#/delivery/environments?service=payments' },
  { name: 'delivery history', list: true, hash: '#/delivery/history' },
  { name: 'delivery stage history', hash: '#/delivery/history?namespace=prod&stage=payments' },
  { name: 'delivery autonomy', list: true, hash: '#/delivery/autonomy' },
  { name: 'delivery publish', list: true, hash: '#/delivery/publish' },
  { name: 'delivery change', hash: '#/delivery/changes/prod/payments-promote-2' },
  { name: 'delivery change held', hash: '#/delivery/changes/test/pay-agent-promote-17' },
];

/** Flips the sidebar with its "[" shortcut and returns the state it lands in. */
async function toggleSidebar(page: Page): Promise<string> {
  const side = page.locator('[data-testid="sidebar"]');
  const before = await side.getAttribute('data-rail');
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur?.()); // "[" is ignored in a field
  await page.keyboard.press('[');
  await expect(side).not.toHaveAttribute('data-rail', before ?? '');
  await settle(page);
  return (await side.getAttribute('data-rail')) ?? 'none';
}

/** The route has drawn its answer: a heading, and nothing still loading. */
async function loaded(page: Page) {
  await expect(page.locator('h1').first()).toBeVisible({ timeout: 30_000 });
  await expect.poll(() => page.evaluate(() =>
    !document.querySelector('[aria-busy="true"]') &&
    ![...document.querySelectorAll('main *')].some((el) => /^Loading\b/.test((el.textContent ?? '').trim()) && (el as HTMLElement).offsetParent !== null && el.children.length === 0)),
  { timeout: 30_000 }).toBe(true);
}

for (const theme of ['dark', 'light'] as const) {
  test.describe(`G1 layout gate (${theme})`, () => {
    test.use({ colorScheme: theme, viewport: { width: 1280, height: 900 } });
    test.beforeEach(async ({ page }) => {
      await page.addInitScript(installScaleFixture);
      if (theme === 'light') {
        await page.addInitScript(() => { try { localStorage.setItem('pacto-theme', 'light'); } catch { /* private mode */ } });
      }
    });

    for (const r of ROUTES) {
      test(r.name, async ({ page }) => {
        test.setTimeout(150_000);
        await appReady(page);
        if (r.open) await r.open(page);
        else await page.goto(`/${r.hash}`);
        await loaded(page);
        await gateMode(page);
        const problems: string[] = [];
        for (const w of WIDTHS) {
          await page.setViewportSize({ width: w, height: 900 });
          await page.evaluate(() => new Promise((res) => requestAnimationFrame(() => requestAnimationFrame(res))));
          await settle(page);
          for (const p of await layoutProblems(page, PENDING)) problems.push(`${w}px: ${p}`);
          if (w >= 1200) {
            const state = await toggleSidebar(page);
            for (const p of await layoutProblems(page, PENDING)) problems.push(`${w}px, sidebar ${state}: ${p}`);
            await toggleSidebar(page);
          }
        }
        problems.push(...await sweepOverflow(page, PENDING));
        await page.setViewportSize({ width: 1280, height: 900 });
        if (await toggleSidebar(page) !== 'open') await toggleSidebar(page);
        for (const p of await sweepOverflow(page, PENDING)) problems.push(`sidebar expanded: ${p}`);
        // Last, because it navigates: every "List all N" / "N more" lands on N (T3).
        await page.setViewportSize({ width: 1280, height: 900 });
        await settle(page);
        if (r.list) problems.push(...await densityProblems(page));
        problems.push(...await figureLinkTotals(page));
        expect(problems, `${r.name} (${theme})`).toEqual([]);
      });
    }
  });
}

// R1: a URL naming something the snapshot does not hold renders the not-found state,
// never data computed for it: no neighbours, no previous or next, no counts.
async function rendersNotFound(page: Page) {
  await expect(page.getByRole('heading', { name: 'Not found', exact: true })).toBeVisible({ timeout: 20_000 });
  const main = page.locator('main');
  await expect(main.getByRole('link', { name: /^(Previous|Next)\b/ })).toHaveCount(0);
  await expect(main.locator('.dist, [data-fig], table')).toHaveCount(0);
}

test('a service the snapshot does not hold renders not found', async ({ page }) => {
  await appReady(page);
  await page.goto('/#/fleet/services/no-such-service-in-this-snapshot');
  await rendersNotFound(page);
});

// The spec's case: a revision of a real service that the service never had. The key is
// a real revision's, with its content digest swapped for one no bundle has.
test('a revision the service never had renders not found', async ({ page }) => {
  await appReady(page);
  await page.goto('/#/fleet/revisions?service=payments-service');
  const a = page.locator('main a[href*="/fleet/revisions/"]').first();
  await expect(a).toBeVisible({ timeout: 20_000 });
  const key = decodeURIComponent((await a.getAttribute('href'))!.replace(/^.*\/fleet\/revisions\//, ''));
  expect(key, 'a revision key is service@content').toContain('@');
  const ghost = `${key.slice(0, key.lastIndexOf('@'))}@sha256:${'0'.repeat(64)}`;
  await page.goto(`/#/fleet/revisions/${encodeURIComponent(ghost)}`);
  await rendersNotFound(page);
});
