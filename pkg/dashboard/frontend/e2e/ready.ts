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

/** settleScroll waits for a smooth scroll to come to rest: scrollY unchanged across frames. */
export async function settleScroll(page: Page): Promise<void> {
  await page.waitForFunction(() => new Promise<boolean>((resolve) => {
    const y = window.scrollY;
    requestAnimationFrame(() => requestAnimationFrame(() => resolve(window.scrollY === y)));
  }));
}
