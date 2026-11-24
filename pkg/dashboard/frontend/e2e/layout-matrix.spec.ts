import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { appReady, settle } from './ready';
import { ROUTES, densityProblems, figureLinkTotals, gateMode, installScaleFixture, layoutProblems, loaded, shellProblems, sweepOverflow, toggleSidebar, touchProblems } from './layoutGate';

// The resolution matrix (SPEC 23, 24.6): every route on the scale fixture, both themes,
// Verdana forced, at viewports from a 320px phone to a 3440px ultrawide, short heights
// included. At each viewport it runs the G1 checks (overlaps, squeezed and clipped text,
// overflow), the shell checks (the sidebar runs the full window height, the phone drawer
// too) and, where the sidebar can change state, both states. Phones and tablets run with
// a coarse pointer and the 44x44px touch rule. axe runs once per route and pointer, at
// the first viewport of the set.
//
// On a PR it runs the representative set of SPEC 24.6. PACTO_LAYOUT_FULL=1 (make
// test-browser-layout-full) runs the whole SPEC 23 matrix instead, plus the 40px width
// sweep from 320 to 1920 at 900px high, phones and tablets at DPR 2 and 3, and the
// default font as well as Verdana. CI runs the full matrix nightly and on the changesets
// Version PR (.github/workflows/layout-full.yml), never on an ordinary PR.
//
// The desktop group's Verdana test also runs G1, the transversal layout gate (SPEC
// sections 12 and 22), on the page it already booted: at 390, 760, 1024, 1280, 1600, 1920
// and 2560px, all 900px high, with a fine pointer at DPR 1, the final frame forced. It
// fails on intersecting text, two text boxes closer than 8px on one line, a box spilling
// with visible overflow, text under 12.19px, ellipsized text with no title, text cut with
// no ellipsis, an identity that is not an elision of its title (or an elided one with no
// title), a lone surrogate, horizontal page scroll, more than one element carrying an err
// box-shadow ring, and the figure rules once figures exist: at most 20 marks, no mark over
// text, every number a filter link or data-nofilter, every "List all N" landing on a list
// of N and every "N more" on N plus the names shown, and on list views L3: the list within
// 600px of the top or a Compact toggle on the figure, and (SPEC 22 r2) no word squeezed to
// one or two characters per line and no table column under 6ch. Then it sweeps every 20px
// from 760 to 1440 and every 40px on to 1920 for horizontal overflow (SPEC 19, 22), since
// a layout clean at the fixed widths spilled between them. Both sidebar states are gated
// (SPEC 20): from 1200px each fixed width is checked with the sidebar as it starts and
// again with it toggled, and the sweep runs once as the sidebar starts and once with it
// expanded from 1200px. A G1 width the matrix already gates at the same viewport (the full
// run's 900px-high sweep) is not checked twice. G1 used to boot every route again in a
// spec of its own (layout.spec.ts).
//
// On a PR the geometry runs in the dark theme only: light changes colour tokens,
// color-scheme and one shadow, never a size, spacing or font (styles/tokens.css). The
// light tests still boot every route per pointer and run the same axe scan at the same
// first viewport, so contrast and a11y stay gated in both themes on every PR. Light
// geometry runs under PACTO_LAYOUT_FULL.
const FULL = !!process.env.PACTO_LAYOUT_FULL;
type VP = [number, number];

const TOUCH: VP[] = FULL
  ? [[320, 568], [360, 740], [375, 667], [390, 844], [414, 896], [430, 932], [667, 375], [844, 390], [932, 430],
      [768, 1024], [820, 1180], [1024, 768], [1180, 820], [1366, 1024]]
  : [[320, 568], [390, 844], [844, 390], [768, 1024], [1024, 768]];
const DESK: VP[] = FULL
  ? [[1280, 720], [1280, 800], [1366, 768], [1440, 900], [1536, 864], [1680, 1050], [1920, 1080], [1920, 1200],
      [2560, 1080], [2560, 1440], [3440, 1440], [3840, 2160],
      ...Array.from({ length: 41 }, (_, i): VP => [320 + i * 40, 900]).filter(([w]) => w >= 1280)]
  : [[1280, 720], [1440, 900], [1920, 1080], [2560, 1440], [3440, 1440]];
// The sweep's narrow half runs on the touch side, where those widths live.
const SWEEP_TOUCH: VP[] = FULL ? Array.from({ length: 24 }, (_, i): VP => [320 + i * 40, 900]) : [];

const GROUPS = [
  { name: 'touch', touch: true, sizes: [...TOUCH, ...SWEEP_TOUCH], dprs: FULL ? [2, 3] : [1] },
  { name: 'desktop', touch: false, sizes: DESK, dprs: [1] },
];
const FONTS = FULL ? ['verdana', 'default'] as const : ['verdana'] as const;
// Routes whose owner value carries the owner-provenance mark under the scale fixture.
const OWNER_MARK_ROUTES = new Set(['service entity', 'service page, resources', 'offering entity']);
// Routes whose 511-S5 block must render, by the block's testid.
const BLOCK_ROUTES = new Map([['delivery inbox, ladder proposal', 'inbox-ladder'], ['delivery environments, unplaced open', 'environments-unplaced']]);
// G1's fixed widths, at 900px high.
const G1_WIDTHS = [390, 760, 1024, 1280, 1600, 1920, 2560];

async function atViewport(page: Page, [w, h]: VP) {
  await page.setViewportSize({ width: w, height: h });
  await page.evaluate(() => new Promise((res) => requestAnimationFrame(() => requestAnimationFrame(res))));
  await settle(page);
}

// Route outermost: Playwright shards a contiguous run of tests, so every kind of test
// (the dark desktop one carries G1 and costs several times the rest) has to be spread
// along the file, or one shard gets the heavy group whole. Titles are unchanged: each
// (theme, group) describe repeats once per route.
// A full route (the map over 3000 and 10000 generated services) runs only in the full
// matrix, in the desktop group.
for (const r of ROUTES.filter((x) => FULL || !x.full)) {
for (const theme of ['dark', 'light'] as const) {
  for (const g of GROUPS.filter((x) => !r.full || !x.touch)) {
    for (const dpr of g.dprs) {
      for (const font of FONTS) {
        test.describe(`layout matrix (${theme}, ${g.name}${FULL ? `, DPR ${dpr}, ${font}` : ''})`, () => {
          test.use({ colorScheme: theme, hasTouch: g.touch, deviceScaleFactor: dpr, viewport: { width: 1280, height: 900 } }); // the app opens wide, then each size is set
          test.beforeEach(async ({ page }) => {
            // A generated fleet is already at scale: the fixture would amend it twice.
            if (!r.fleet) await page.addInitScript(installScaleFixture);
            if (theme === 'light') {
              await page.addInitScript(() => { try { localStorage.setItem('pacto-theme', 'light'); } catch { /* private mode */ } });
            }
          });

          // ponytail: light on a PR is axe only; geometry there waits for the full run.
          const geometry = FULL || theme === 'dark';
          // G1 runs where its own spec ran: fine pointer, DPR 1, Verdana, the same themes.
          const g1 = geometry && !g.touch && dpr === 1 && font === 'verdana';
            test(r.name, async ({ page }) => {
              test.setTimeout((FULL ? 900_000 : 240_000) + (g1 ? 150_000 : 0));
              await appReady(page);
              if (r.open) await r.open(page);
              else await page.goto(`/${r.hash}`);
              await loaded(page);
              // The scale fixture gives these routes' owners a non-declared provenance (516-S1).
              if (OWNER_MARK_ROUTES.has(r.name)) await expect(page.getByTestId('owner-provenance').first()).toBeVisible();
              if (BLOCK_ROUTES.has(r.name)) await expect(page.getByTestId(BLOCK_ROUTES.get(r.name)!)).toBeVisible();
              if (font === 'verdana') await gateMode(page);
              else await page.evaluate(() => { document.documentElement.dataset.narrate = 'final'; });
              expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches), 'the pointer is coarse exactly on touch devices').toBe(g.touch);
              const problems: string[] = [];
              for (const [i, vp] of (geometry ? g.sizes : g.sizes.slice(0, 1)).entries()) {
                await atViewport(page, vp);
                const at = `${vp[0]}x${vp[1]}`;
                if (geometry) {
                  for (const p of await layoutProblems(page)) problems.push(`${at}: ${p}`);
                  for (const p of await shellProblems(page)) problems.push(`${at}: ${p}`);
                  for (const p of await touchProblems(page)) problems.push(`${at}: ${p}`);
                }
                if (i === 0) {
                  const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
                  for (const v of axe.violations) problems.push(`${at}: axe ${v.id} on ${v.nodes.slice(0, 3).map((n) => n.target.join(' ')).join(', ')}`);
                }
                // The other sidebar state, where the sidebar is a column of its own.
                if (geometry && vp[0] >= 1200) {
                  const state = await toggleSidebar(page);
                  for (const p of await layoutProblems(page)) problems.push(`${at}, sidebar ${state}: ${p}`);
                  for (const p of await shellProblems(page)) problems.push(`${at}, sidebar ${state}: ${p}`);
                  await toggleSidebar(page);
                }
              }
              if (g1) {
                for (const w of G1_WIDTHS) {
                  // ponytail: same viewport, same checks (a superset) already ran above.
                  if (g.sizes.some(([sw, sh]) => sw === w && sh === 900)) continue;
                  await atViewport(page, [w, 900]);
                  for (const p of await layoutProblems(page)) problems.push(`${w}px: ${p}`);
                  if (w >= 1200) {
                    const state = await toggleSidebar(page);
                    for (const p of await layoutProblems(page)) problems.push(`${w}px, sidebar ${state}: ${p}`);
                    await toggleSidebar(page);
                  }
                }
                problems.push(...await sweepOverflow(page));
                await page.setViewportSize({ width: 1280, height: 900 });
                if (await toggleSidebar(page) !== 'open') await toggleSidebar(page);
                for (const p of await sweepOverflow(page)) problems.push(`sidebar expanded: ${p}`);
                // Last, because it navigates: every "List all N" / "N more" lands on N (T3).
                await page.setViewportSize({ width: 1280, height: 900 });
                await settle(page);
                if (r.list) problems.push(...await densityProblems(page));
                problems.push(...await figureLinkTotals(page));
              }
              expect(problems, `${r.name} (${theme}, ${g.name})`).toEqual([]);
            });
        });
      }
    }
  }
}
}

// The case the reader reported (SPEC 22): at about 1900px with the sidebar expanded, the
// History table squeezed a column until its header read one letter per line.
test('the History table at 1900px beside the expanded sidebar keeps every column readable', async ({ page }) => {
  await page.addInitScript(installScaleFixture);
  await page.addInitScript(() => { try { localStorage.setItem('pacto-nav', 'expanded'); } catch { /* private mode */ } });
  await page.setViewportSize({ width: 1900, height: 1000 });
  await appReady(page);
  await page.goto('/#/delivery/history');
  await loaded(page);
  await gateMode(page);
  await expect(page.locator('[data-testid="sidebar"]')).toHaveAttribute('data-rail', 'open');
  await expect(page.locator('main table').first()).toBeVisible();
  const narrow = await page.locator('main thead th').evaluateAll((ths) => ths
    .filter((th) => (th.textContent ?? '').trim().length >= 4)
    .filter((th) => th.getBoundingClientRect().height > 2.5 * (parseFloat(getComputedStyle(th).lineHeight) || 20))
    .map((th) => (th.textContent ?? '').trim()));
  expect(narrow, 'column headers wrapped past two lines').toEqual([]);
  expect([...await layoutProblems(page), ...await shellProblems(page)]).toEqual([]);
});

// A service with 200 environments (the scale fixture's storefront-web): the rows cap
// themselves behind one Show all, and the differences, history and the asOf and
// completeness line #452 asks for stay visible instead of hiding behind a fade.
test('the Environments section at 200 environments keeps its differences, changes link and footer visible', async ({ page }) => {
  await page.addInitScript(installScaleFixture);
  await page.setViewportSize({ width: 1440, height: 900 });
  await appReady(page);
  await page.goto('/#/fleet/services/storefront-web');
  await loaded(page);
  const section = page.locator('#sec-environments');
  await expect(section.getByTestId('service-environment').first()).toBeVisible({ timeout: 30_000 });
  await expect(section.locator('.ps-clipped')).toHaveCount(0);
  await expect(section.getByTestId('preview-show-all')).toHaveCount(0);
  await expect(section.getByRole('button', { name: /^Show all [\d,]+ environments$/ })).toHaveCount(1);
  for (const id of ['environment-differences', 'environment-changes-link', 'environments-footer']) {
    const el = section.getByTestId(id);
    await el.scrollIntoViewIfNeeded();
    await expect(el).toBeInViewport();
  }
  await expect(section.getByTestId('environments-footer')).toContainText(/^As of .*, (complete|partial)/);
});

// The Metrics fixture answers on the plain demo in place of the demo's own run records: the
// metrics routes above draw the fixture's answers unless they ask for `engine`.
test('the metrics fixture answers GET /api/stats on the plain demo', async ({ page }) => {
  await page.addInitScript(installScaleFixture);
  await appReady(page);
  const serve = (mode: string) => page.evaluate(async (m) => {
    sessionStorage.setItem('fixture.stats', m);
    const w = window as unknown as { __pactoServe: (method: string, path: string, body: string | null) => Promise<{ status: number; body: string }> };
    const [stats, caps] = await Promise.all([w.__pactoServe('GET', '/api/stats', null), w.__pactoServe('GET', '/api/capabilities', null)]);
    return { status: stats.status, stats: caps.status === 200 ? JSON.parse(caps.body).stats : null };
  }, mode);
  expect(await serve('')).toEqual({ status: 200, stats: true });
  expect(await serve('none')).toEqual({ status: 200, stats: true });
  expect(await serve('off')).toEqual({ status: 404, stats: false });
  // `engine` leaves both to the demo, which serves its own run records.
  expect(await serve('engine')).toEqual({ status: 200, stats: true });
});

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

// The sign-in screen, signed out with token sign-in on. It has no sidebar and no shell,
// so it stays out of ROUTES (every route there waits for the Operational Graph link): the
// same viewports, both themes, the G1, touch and axe checks, without the shell checks.
function signedOutTokenSession(): void {
  type Serve = (method: string, path: string, body: string | null, headers?: Record<string, string>) => Promise<{ status: number; body: string; contentType: string }>;
  let real: Serve | null = null;
  const json = (status: number, v: unknown) => ({ status, body: JSON.stringify(v), contentType: 'application/json' });
  const wrapped: Serve = async (method, path, body) => {
    const route = path.split('?')[0];
    if (route === '/api/session') return json(200, { mode: 'shared', login: false, tokenLogin: true, signedIn: false });
    if (route.startsWith('/api/')) return json(401, { status: 401, code: 'LoginRequired', detail: 'sign in to use this dashboard' });
    return (real as Serve)(method, path, body);
  };
  // The demo signs people in itself; the real sign-in screen needs the hook away.
  Object.defineProperty(window, '__pactoSignIn', { configurable: true, get() { return undefined; }, set() {} });
  Object.defineProperty(window, '__pactoServe', { configurable: true, get() { return real ? wrapped : undefined; }, set(v: Serve) { real = v; } });
}
const EXPIRED_LINK = `/#token=h.${Buffer.from(JSON.stringify({ exp: 1700000000 })).toString('base64url')}.s`;

for (const [name, path, shown] of [['sign-in screen, token form', '/', 'token-sign-in'], ['sign-in, link expired', EXPIRED_LINK, 'sign-in-link-expired']] as const) {
  for (const theme of ['dark', 'light'] as const) {
    for (const g of GROUPS) {
      test.describe(`sign-in layout (${theme}, ${g.name})`, () => {
        test.use({ colorScheme: theme, hasTouch: g.touch, viewport: { width: 1280, height: 900 } });
        test(name, async ({ page }) => {
          test.setTimeout(FULL ? 600_000 : 120_000);
          await page.addInitScript(signedOutTokenSession);
          if (theme === 'light') await page.addInitScript(() => { try { localStorage.setItem('pacto-theme', 'light'); } catch { /* private mode */ } });
          await page.goto(path);
          await expect(page.getByTestId(shown)).toBeVisible({ timeout: 30_000 });
          await gateMode(page);
          const problems: string[] = [];
          for (const [i, vp] of g.sizes.entries()) {
            await atViewport(page, vp);
            const at = `${vp[0]}x${vp[1]}`;
            for (const p of await layoutProblems(page)) problems.push(`${at}: ${p}`);
            for (const p of await touchProblems(page)) problems.push(`${at}: ${p}`);
            if (i === 0) {
              const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
              for (const v of axe.violations) problems.push(`${at}: axe ${v.id} on ${v.nodes.slice(0, 3).map((n) => n.target.join(' ')).join(', ')}`);
            }
          }
          expect(problems, `${name} (${theme}, ${g.name})`).toEqual([]);
        });
      });
    }
  }
}
