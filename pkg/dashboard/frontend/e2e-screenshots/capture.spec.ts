import { test, expect, type Page } from '@playwright/test';
import { appReady, settle } from '../e2e/ready';
import { chooseRevision, loaded } from '../e2e/layoutGate';

// One screenshot per documented view and theme, at 1600x1000 in the bundled fonts (Inter,
// Source Serif 4, JetBrains Mono) with the sidebar in its wide-screen default (expanded,
// nothing stored). The demo's notice strip is dismissed, and every figure is shown at its
// final frame, so two runs over the same build match pixel for pixel. `clip` crops a shot
// to one region.
const OUT = '../../../docs/assets/screenshots';

// The fixed instant every shot is taken at. page.clock fixes the page's clock only; the
// engine runs in a Web Worker (examples/demo/engine.js), which the clock does not reach,
// so its "evaluated at" times would read the real clock. The route below runs the same
// fixed Date in the worker before the Go runtime reads it.
const NOW = '2026-11-09T12:00:00Z';
const WORKER_CLOCK = `{const T=Date.parse('${NOW}'),D=Date;self.Date=class extends D{constructor(...a){super(...(a.length?a:[T]))}static now(){return T}}}\n`;

const SHOTS: { name: string; open: (page: Page) => Promise<void>; clip?: { x: number; y: number; width: number; height: number } }[] = [
  { name: 'overview', open: (p) => go(p, '/#/fleet', 'Is anything wrong?') },
  { name: 'service', open: (p) => go(p, '/#/fleet/services/payments-service', 'payments-service') },
  { name: 'graph', open: async (p) => { await go(p, '/#/fleet/graph/service/payments-service', 'Operational graph'); await p.waitForTimeout(1500); } },
  {
    name: 'change-analysis',
    open: async (p) => {
      await go(p, '/#/fleet/changes/payments-service', 'Change analysis');
      await chooseRevision(p, '#impact-old-rev', '1.2.1');
      await chooseRevision(p, '#impact-new-rev', '2.0.1');
      await p.getByRole('button', { name: /Compare revisions/ }).click();
      await expect(p.locator('[data-fig] svg[role="img"]').first()).toBeAttached();
    },
  },
  { name: 'delivery-stages', open: (p) => go(p, '/#/delivery/environments', 'Stages') },
  { name: 'autonomy', open: (p) => go(p, '/#/delivery/autonomy', 'Autonomy') },
  { name: 'golden-paths', open: (p) => go(p, '/#/fleet/golden-paths', 'Golden paths') },
  // The expanded sidebar alone, below the top bar, on the overview.
  { name: 'sidebar', open: (p) => go(p, '/#/fleet', 'Is anything wrong?'), clip: { x: 0, y: 56, width: 232, height: 574 } },
];

async function finish(page: Page) {
  // A spinner loops forever and has no final frame to finish at.
  await page.evaluate(() => document.getAnimations().forEach((a) => { if (a.effect?.getComputedTiming().endTime !== Infinity) a.finish(); }));
  await settle(page);
}

async function go(page: Page, hash: string, heading: string) {
  await page.goto(hash);
  await expect(page.getByRole('heading', { level: 1 }).filter({ hasText: heading }).first()).toBeVisible();
}

for (const theme of ['light', 'dark'] as const) {
  test.describe(theme, () => {
    test.use({ colorScheme: theme });
    for (const s of SHOTS) {
      test(s.name, async ({ page }) => {
        // A fixed clock, so "as of" times and relative ages read the same on every run.
        await page.clock.setFixedTime(new Date(NOW));
        await page.context().route(/\/engine\.js(\?|$)/, async (route) => {
          const res = await route.fetch();
          await route.fulfill({ response: res, body: WORKER_CLOCK + (await res.text()) });
        });
        await appReady(page);
        const dismiss = page.getByRole('button', { name: 'Dismiss this notice' });
        if (await dismiss.isVisible()) await dismiss.click();
        await s.open(page);
        // Chromium on macOS draws the page scrollbar, and so its stable gutter, only while the
        // system shows scrollbars (a mouse attached, or the setting), so the page width would
        // follow the machine. With no scrollbar the shot uses the full 1600px everywhere.
        await page.addStyleTag({ content: 'html { scrollbar-gutter: auto; scrollbar-width: none; } ::-webkit-scrollbar { display: none; }' });
        await page.evaluate(() => { document.documentElement.dataset.narrate = 'final'; });
        await loaded(page);
        await finish(page);
        // Lay the settled page out again from nothing. The compositor keeps what it drew
        // while the page loaded and moved, and a sticky bar or table header at a fractional
        // offset then snaps a pixel up or down depending on that history. Hiding the body
        // for a frame drops those layers, so the shot depends only on the final page.
        // Showing it again restarts its CSS animations, hence the second finish.
        await page.evaluate(async () => {
          const frame = () => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
          document.body.style.display = 'none';
          await frame();
          document.body.style.display = '';
          await frame();
        });
        await finish(page);
        await page.evaluate(() => window.scrollTo(0, 0));
        await page.screenshot({ path: `${OUT}/dashboard-${s.name}-${theme}.png`, animations: 'disabled', caret: 'hide', clip: s.clip });
      });
    }
  });
}
