import { test, expect, type Page } from '@playwright/test';
import { appReady, settle } from './ready';
import { gateMode, installScaleFixture, layoutProblems } from './layoutGate';

// G1, the transversal layout gate (SPEC section 12): every route, at 390, 760, 1024 and
// 1280px, in both themes, with Verdana forced (CI renders DejaVu, wider than Inter),
// the final frame forced, on the scale fixture. It fails on intersecting text, a box
// spilling with visible overflow, text under 12.19px, ellipsized text with no title,
// text cut with no ellipsis, an identity that is not an elision of its title, a lone
// surrogate, horizontal page scroll, and the figure rules (marks, linked numbers, one
// ring) once figures exist.
//
// PENDING names subtrees a later phase rewrites. Each entry is deleted by that phase;
// nothing else is excluded.
const PENDING: string[] = [
  // P1 rewrites the isometric impact diagram as ImpactFigure on the figure primitives.
  '[data-testid="iso-impact"]',
];

const WIDTHS = [390, 760, 1024, 1280];

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

type Route = { name: string; hash?: string; open?: (page: Page) => Promise<void> };
const ROUTES: Route[] = [
  { name: 'overview', hash: '#/fleet' },
  { name: 'services', hash: '#/fleet/services' },
  { name: 'owners', hash: '#/fleet/owners' },
  { name: 'domains', hash: '#/fleet/domains' },
  { name: 'offerings', hash: '#/fleet/offerings' },
  { name: 'sources', hash: '#/fleet/sources' },
  { name: 'attention', hash: '#/fleet/attention' },
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
  { name: 'revision list', hash: '#/fleet/revisions?service=payments-service' },
  { name: 'revision entity', open: firstLink('#/fleet/revisions?service=payments-service', '/fleet/revisions/') },
  { name: 'target list', hash: '#/fleet/targets' },
  { name: 'target entity', open: firstLink('#/fleet/targets', '/fleet/targets/') },
  { name: 'owner entity', hash: '#/fleet/owners/team:catalog-team' },
  { name: 'domain entity', hash: '#/fleet/domains/commerce' },
  { name: 'offering entity', hash: '#/fleet/offerings/messaging' },
  { name: 'source entity', hash: '#/fleet/sources/edge-cluster' },
  { name: 'delivery inbox', hash: '#/delivery/inbox' },
  { name: 'delivery environments', hash: '#/delivery/environments' },
  { name: 'delivery history', hash: '#/delivery/history' },
  { name: 'delivery stage history', hash: '#/delivery/history?namespace=prod&stage=payments' },
  { name: 'delivery autonomy', hash: '#/delivery/autonomy' },
  { name: 'delivery publish', hash: '#/delivery/publish' },
  { name: 'delivery change', hash: '#/delivery/changes/prod/payments-promote-2' },
  { name: 'delivery change held', hash: '#/delivery/changes/test/pay-agent-promote-17' },
];

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
        }
        expect(problems, `${r.name} (${theme})`).toEqual([]);
      });
    }
  });
}

// R1: a URL naming something the snapshot does not hold renders not found, never data
// computed for it.
test('an entity the snapshot does not hold renders not found', async ({ page }) => {
  await appReady(page);
  await page.goto('/#/fleet/services/no-such-service-in-this-snapshot');
  await expect(page.getByText(/not found|does not exist|no service/i).first()).toBeVisible({ timeout: 20_000 });
});
