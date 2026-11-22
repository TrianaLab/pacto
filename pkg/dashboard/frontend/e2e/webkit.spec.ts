import { test, expect } from '@playwright/test';

// The WASM demo must load in WebKit (Safari, every iOS browser), not only in
// Chromium: runs on the webkit project only, see playwright.config.ts.
//
// A WebKit worker has a much smaller call stack than Chromium. In 4.6.0 a
// regexp with a {1,253} repeat, compiled during package init, overflowed it:
// the engine threw "Maximum call stack size exceeded" before main ran and the
// page sat at "Loading the Pacto engine" in Safari while every Chromium test
// stayed green. pkg/contract's TestPatternsStayShallow pins that cause; this
// pins the symptom, whatever the next deep call turns out to be.
test('the demo engine boots and the Overview renders in WebKit', async ({ page }) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on('console', (m) => {
    if (m.type() === 'error' || m.text().includes('RangeError')) errors.push(m.text());
  });
  page.on('pageerror', (e) => errors.push(String(e)));
  await page.goto('/');
  await expect(page.getByTestId('overview-answer')).toBeVisible({ timeout: 45_000 });
  expect(errors.filter((e) => /RangeError|engine failed/.test(e))).toEqual([]);
});
