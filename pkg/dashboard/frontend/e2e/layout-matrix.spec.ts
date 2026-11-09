import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { appReady, settle } from './ready';
import { ROUTES, gateMode, installScaleFixture, layoutProblems, loaded, shellProblems, toggleSidebar, touchProblems } from './layoutGate';

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
// default font as well as Verdana. G1's link totals and the 20px overflow sweep stay in
// layout.spec.ts.
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

async function atViewport(page: Page, [w, h]: VP) {
  await page.setViewportSize({ width: w, height: h });
  await page.evaluate(() => new Promise((res) => requestAnimationFrame(() => requestAnimationFrame(res))));
  await settle(page);
}

for (const theme of ['dark', 'light'] as const) {
  for (const g of GROUPS) {
    for (const dpr of g.dprs) {
      for (const font of FONTS) {
        test.describe(`layout matrix (${theme}, ${g.name}${FULL ? `, DPR ${dpr}, ${font}` : ''})`, () => {
          test.use({ colorScheme: theme, hasTouch: g.touch, deviceScaleFactor: dpr, viewport: { width: 1280, height: 900 } }); // the app opens wide, then each size is set
          test.beforeEach(async ({ page }) => {
            await page.addInitScript(installScaleFixture);
            if (theme === 'light') {
              await page.addInitScript(() => { try { localStorage.setItem('pacto-theme', 'light'); } catch { /* private mode */ } });
            }
          });

          for (const r of ROUTES) {
            test(r.name, async ({ page }) => {
              test.setTimeout(FULL ? 900_000 : 240_000);
              await appReady(page);
              if (r.open) await r.open(page);
              else await page.goto(`/${r.hash}`);
              await loaded(page);
              if (font === 'verdana') await gateMode(page);
              else await page.evaluate(() => { document.documentElement.dataset.narrate = 'final'; });
              expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches), 'the pointer is coarse exactly on touch devices').toBe(g.touch);
              const problems: string[] = [];
              for (const [i, vp] of g.sizes.entries()) {
                await atViewport(page, vp);
                const at = `${vp[0]}x${vp[1]}`;
                for (const p of await layoutProblems(page)) problems.push(`${at}: ${p}`);
                for (const p of await shellProblems(page)) problems.push(`${at}: ${p}`);
                for (const p of await touchProblems(page)) problems.push(`${at}: ${p}`);
                if (i === 0) {
                  const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
                  for (const v of axe.violations) problems.push(`${at}: axe ${v.id} on ${v.nodes.slice(0, 3).map((n) => n.target.join(' ')).join(', ')}`);
                }
                // The other sidebar state, where the sidebar is a column of its own.
                if (vp[0] >= 1200) {
                  const state = await toggleSidebar(page);
                  for (const p of await layoutProblems(page)) problems.push(`${at}, sidebar ${state}: ${p}`);
                  for (const p of await shellProblems(page)) problems.push(`${at}, sidebar ${state}: ${p}`);
                  await toggleSidebar(page);
                }
              }
              expect(problems, `${r.name} (${theme}, ${g.name})`).toEqual([]);
            });
          }
        });
      }
    }
  }
}
