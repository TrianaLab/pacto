import { test, expect, type Page } from '@playwright/test';
import { appReady, settle } from '../e2e/ready';
import { chooseRevision, loaded } from '../e2e/layoutGate';

// One screenshot per documented view and theme, at 1600x1000 in the bundled fonts (Inter,
// Source Serif 4, JetBrains Mono) with the sidebar in its wide-screen default (expanded,
// nothing stored). The demo's notice strip is dismissed, and every figure is shown at its
// final frame, so two runs over the same build match. `clip` crops a shot to one region.
const OUT = '../../../docs/assets/screenshots';

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
        await page.clock.setFixedTime(new Date('2026-11-09T12:00:00Z'));
        await appReady(page);
        const dismiss = page.getByRole('button', { name: 'Dismiss this notice' });
        if (await dismiss.isVisible()) await dismiss.click();
        await s.open(page);
        await page.evaluate(() => { document.documentElement.dataset.narrate = 'final'; });
        await loaded(page);
        // A spinner loops forever and has no final frame to finish at.
        await page.evaluate(() => document.getAnimations().forEach((a) => { if (a.effect?.getComputedTiming().endTime !== Infinity) a.finish(); }));
        await settle(page);
        await page.evaluate(() => window.scrollTo(0, 0));
        await page.screenshot({ path: `${OUT}/dashboard-${s.name}-${theme}.png`, animations: 'disabled', caret: 'hide', clip: s.clip });
      });
    }
  });
}
