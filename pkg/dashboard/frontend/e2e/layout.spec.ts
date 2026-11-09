import { test, expect, type Page } from '@playwright/test';
import { appReady, settle } from './ready';
import { ROUTES, densityProblems, figureLinkTotals, gateMode, installScaleFixture, layoutProblems, loaded, sweepOverflow, toggleSidebar } from './layoutGate';

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
