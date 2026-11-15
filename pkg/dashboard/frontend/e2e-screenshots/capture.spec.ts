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

const SHOTS: { name: string; open: (page: Page) => Promise<void>; clip?: { x: number; y: number; width: number; height: number }; clipTo?: string; element?: string }[] = [
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
  // What if redis becomes unavailable: the event link answers on load, and redis has
  // required and optional dependents, so both the Breaks and the May degrade lanes show.
  {
    name: 'change-analysis-event',
    open: async (p) => {
      await go(p, '/#/fleet/changes/redis?event=unavailable', 'Change analysis');
      await expect(p.getByTestId('changes-answer')).toBeVisible();
      await expect(p.locator('[data-fig] svg[role="img"]').first()).toBeAttached();
    },
  },
  { name: 'delivery-stages', open: (p) => go(p, '/#/delivery/environments', 'Stages') },
  { name: 'autonomy', open: (p) => go(p, '/#/delivery/autonomy', 'Autonomy') },
  { name: 'golden-paths', open: (p) => go(p, '/#/fleet/golden-paths', 'Golden paths') },
  // storefront-web's Environments section alone: declared, expired, observed-only and Stage-less.
  {
    name: 'service-environments',
    open: async (p) => { await go(p, '/#/fleet/services/storefront-web', 'storefront-web'); await expect(p.locator('#sec-environments')).toBeVisible(); },
    element: '#sec-environments',
  },
  // The overview's Operational health and Knowledge bands, each alone: the ownership and
  // readiness band sits between them, so one shot would not hold both.
  { name: 'overview-health', open: (p) => go(p, '/#/fleet', 'Is anything wrong?'), element: '#sec-health' },
  { name: 'overview-knowledge', open: (p) => go(p, '/#/fleet', 'Is anything wrong?'), element: '#sec-knowledge' },
  // The Offerings list: where requests for platform offerings stand, then who maintains
  // each offering, its lifecycle, availability and failed requests.
  { name: 'offerings-list', open: (p) => go(p, '/#/fleet/offerings', 'Platform offerings') },
  // The postgresql offering: availability per environment from its two providers, then
  // each provider's versions, the successor 16.1.0 names and its consumers.
  { name: 'offering-postgresql', open: async (p) => { await go(p, '/#/fleet/offerings/postgresql', 'postgresql'); await expect(p.getByTestId('availability').first()).toBeVisible(); } },
  // The delivery section of payments' target page in prod, opened under ticket CHG-2041:
  // the Plan offers, and the promote plan the first one opens with the ticket filled in.
  {
    name: 'act-from-investigation',
    open: async (p) => {
      await go(p, '/#/fleet/targets/prod%2FDeployment%2Fpayments?correlationID=CHG-2041', 'payments');
      await p.getByRole('button', { name: /^Plan promote/ }).click();
      await expect(p.getByTestId('pp-correlation')).toHaveValue('CHG-2041');
      await expect(p.getByTestId('plan-prediction')).toBeVisible();
    },
    element: '[data-testid="target-delivery"]',
  },
  // The expanded sidebar alone, below the top bar, on the overview.
  // Clipped to the expanded nav's own box, so a new item never pushes Data sources out of frame.
  { name: 'sidebar', open: (p) => go(p, '/#/fleet', 'Is anything wrong?'), clipTo: '[data-testid="sidebar"] nav' },
  // catalog-service's Resources section alone: the declared request, its phase and usage.
  {
    name: 'service-resources',
    open: async (p) => { await go(p, '/#/fleet/services/catalog-service', 'catalog-service'); await expect(p.locator('#sec-resources')).toBeVisible(); },
    element: '#sec-resources',
  },
  // The story payments service's Changes section alone, with the times its revisions
  // reached a Stage read from signed delivery records.
  {
    name: 'service-changes',
    open: async (p) => {
      await go(p, '/#/fleet/services/registry.example%252Fstory%2Fpayments?section=changes', 'payments');
      await expect(p.locator('#sec-changes [data-testid="service-change-reached"] a').first()).toBeVisible();
    },
    element: '#sec-changes',
  },
  // The story payments service's Delivery history as of a minute ago: what each of its
  // Stages ran then, known where a complete signed history holds and partial where not.
  {
    name: 'delivery-history-as-of',
    open: async (p) => {
      await go(p, '/#/delivery/history?service=payments&domain=registry.example%2Fstory&at=1m', 'History');
      await expect(p.getByTestId('history-at-row').first()).toBeVisible();
    },
  },
  // Metrics: the explainer on the plain demo, which records no run statistics.
  { name: 'metrics', open: (p) => go(p, '/#/metrics', 'Metrics') },
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
        const path = `${OUT}/dashboard-${s.name}-${theme}.png`;
        if (s.element) {
          // A section taller than the viewport would be stitched from scrolled frames with
          // the sticky top bar drawn over it, so grow the viewport to hold the whole section.
          const box = await page.locator(s.element).boundingBox();
          if (box) await page.setViewportSize({ width: 1600, height: Math.max(1000, Math.ceil(box.y + box.height) + 16) });
          await finish(page);
          await page.locator(s.element).screenshot({ path, animations: 'disabled', caret: 'hide' });
        }
        else {
          // clipTo: the full sidebar column below the 56px top bar, down to the selector's foot.
          const box = s.clipTo ? await page.locator(s.clipTo).boundingBox() : null;
          const clip = box ? { x: 0, y: 56, width: 232, height: Math.ceil(box.y + box.height) + 16 - 56 } : s.clip;
          await page.screenshot({ path, animations: 'disabled', caret: 'hide', clip });
        }
      });
    }
  });
}
