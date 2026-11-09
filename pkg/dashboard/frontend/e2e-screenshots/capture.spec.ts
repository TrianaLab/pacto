import { test, expect, type Page } from '@playwright/test';
import { appReady, settle } from '../e2e/ready';
import { chooseRevision } from '../e2e/layoutGate';

// One screenshot per documented view and theme, at 1600x1000 with the sidebar in its
// wide-screen default (expanded). The demo's notice strip is dismissed, and every
// figure is shown at its final frame, so two runs over the same build match.
const OUT = '../../../docs/assets/screenshots';

const SHOTS: { name: string; open: (page: Page) => Promise<void> }[] = [
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
        await appReady(page);
        const dismiss = page.getByRole('button', { name: 'Dismiss this notice' });
        if (await dismiss.isVisible()) await dismiss.click();
        await s.open(page);
        await page.evaluate(() => { document.documentElement.dataset.narrate = 'final'; });
        await page.evaluate(() => document.getAnimations().forEach((a) => a.finish()));
        await settle(page);
        await page.evaluate(() => window.scrollTo(0, 0));
        await page.screenshot({ path: `${OUT}/dashboard-${s.name}-${theme}.png`, animations: 'disabled', caret: 'hide' });
      });
    }
  });
}
