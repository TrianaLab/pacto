import { expect, type Page } from '@playwright/test';

/*
 * Readiness waits the specs share. `ready.ts`, not `*.spec.ts`, so Playwright does not
 * collect it as a test file.
 *
 * Every wait here is on something the app itself reports, never on a guess at how long
 * it takes. A guess is wrong in both directions: too short on a loaded machine, wasted
 * time on an idle one.
 */

/**
 * appReady opens `path` and returns once the dashboard can answer: the wasm engine has
 * resolved `window.__pactoReady` (boot.js sets it, and every API call queues behind it)
 * and the capability-gated Operational Graph link is on screen, which proves the app
 * mounted, fetched its capabilities and found the fleet provider.
 */
export async function appReady(page: Page, path = '/'): Promise<void> {
  await page.goto(path);
  await page.evaluate(() => (window as unknown as { __pactoReady?: Promise<void> }).__pactoReady);
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible();
}

/**
 * settle waits for the page to stop moving, so an audit samples it at rest: a fade's
 * mid-flight opacity transiently dips muted text below the AA ratio and reports a
 * contrast failure against a colour the reader never sees.
 *
 * Two consecutive quiet samples rather than one: a test gates on a heading, the list
 * under it arrives later, and an entrance that begins between the samples resets the
 * count. Every product animation is finite by design (the alarm in FleetAttentionView
 * is three pulses, not an infinite loop, precisely so this can be a real wait), so the
 * deadline is a backstop against a bug, not the normal path.
 */
export async function settle(page: Page): Promise<void> {
  const deadline = Date.now() + 6_000;
  const moving = () => page.evaluate(() => (document.getAnimations?.() ?? []).some((a) => a.playState === 'running'));
  let quiet = 0;
  while (quiet < 2 && Date.now() < deadline) {
    quiet = (await moving()) ? 0 : quiet + 1;
    await page.waitForTimeout(200); // the sampling interval, not a guess at an end
  }
}

// The demo's payments-service v2.1.1 overview carries a flowchart fence.
//
// The row is picked by canonical key, never by label: the demo publishes same-named
// services in two domains, so matching on the visible name alone can silently open
// whichever of them happens to sort first.
export async function openMermaidDoc(page: Page) {
  await page.goto('/#/fleet/services');
  await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible({ timeout: 20_000 });
  await page.locator('.sv-item a.entity-link[href$="/fleet/services/payments-service"]').first().click();
  await expect(page).toHaveURL(/#\/fleet\/services\/payments-service$/);
  // 2.1.1 is published but not deployed, so it is not in the in-use list the page
  // opens with -- it is in the exhaustive "All revisions" disclosure, which is
  // collapsed. A revision nobody runs still has documentation worth reading.
  await page
    .locator('details.ps-collapsible')
    .filter({ has: page.getByRole('heading', { name: 'All revisions' }) })
    .locator('summary')
    .first()
    .click();
  await page.locator('a.entity-link[href*="/fleet/revisions/"]', { hasText: '2.1.1' }).first().click();
  await expect(page).toHaveURL(/#\/fleet\/revisions\//);

  const doc = page.locator('details.rd-doc', { hasText: 'overview' }).first();
  await expect(doc).toBeVisible({ timeout: 20_000 });
  await doc.locator('summary').click();
  return doc;
}
