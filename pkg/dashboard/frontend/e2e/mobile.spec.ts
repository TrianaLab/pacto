import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import {
  boot, canonicalKeys, runAnalysis, sampleRoles, normalBody,
  assertPageHierarchy, assertRoleCoherence, type RoleSample,
} from './typographyChecks';

// Mobile-layout E2E (runs only on the mobile project, see playwright.config.ts):
// at a narrow viewport the desktop nav is hidden and the primary navigation
// collapses into an accessible hamburger drawer. This asserts the section A rules:
// hidden navigation is ABSENT from the accessibility tree until opened, the toggle
// exposes aria-expanded, and Escape closes the overlay.
test('the navbar collapses into an accessible hamburger drawer on mobile', async ({ page }) => {
  await page.goto('/');
  // Wait for the wasm engine (a service from the embedded fleet appears).
  await expect(page.getByText('payments-service').first()).toBeVisible({ timeout: 20_000 });

  // Collapsed: the drawer's navigation is not in the DOM/accessibility tree, and
  // the toggle reports collapsed. The desktop nav is display:none (also absent),
  // so the Operational Graph link resolves to exactly zero accessible matches.
  const hamburger = page.getByRole('button', { name: 'Menu' });
  await expect(hamburger).toBeVisible();
  await expect(hamburger).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('#mobile-drawer')).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toHaveCount(0);

  // Open: the drawer appears, the toggle reports expanded, and the primary nav
  // (a single landmark) is now reachable.
  await hamburger.click();
  const drawer = page.locator('#mobile-drawer');
  await expect(drawer).toBeVisible();
  await expect(hamburger).toHaveAttribute('aria-expanded', 'true');
  await expect(drawer.getByRole('link', { name: 'Operational Graph' })).toBeVisible();

  // Escape closes the overlay and the navigation leaves the accessibility tree.
  await page.keyboard.press('Escape');
  await expect(page.locator('#mobile-drawer')).toHaveCount(0);
  await expect(hamburger).toHaveAttribute('aria-expanded', 'false');
});

// WCAG A/AA axe gate over the mobile navigation open state (requirement 8.9); contrast
// is measured separately (requirement 8.8), so it is the only disabled rule.
test('mobile navigation open passes the WCAG A/AA axe gate', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('payments-service').first()).toBeVisible({ timeout: 20_000 });
  await page.getByRole('button', { name: 'Menu' }).click();
  await expect(page.locator('#mobile-drawer')).toBeVisible();
  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
    .disableRules(['color-contrast'])
    .analyze();
  expect(results.violations, JSON.stringify(results.violations.map((v) => v.id))).toEqual([]);
});

/**
 * Mobile typography acceptance, from COMPUTED styles (requirement 20).
 *
 * Not a copy of the desktop sweep for symmetry's sake: at 393px the page title is the
 * element most likely to be shrunk by a responsive override, and the section titles are
 * the ones most likely to be left alone -- which is exactly how a hierarchy collapses to
 * flat on the screen where the reader has the least context. Fewer routes than desktop
 * (each is a full WASM boot on a throttled mobile profile), but the same three claims.
 */
test.describe('typography hierarchy on mobile', () => {
  test('the ramp survives a 393px viewport', async ({ page }) => {
    test.setTimeout(240_000);
    const k = await canonicalKeys(page);
    const e = encodeURIComponent;

    const routes: Array<[string, string]> = [
      ['Overview', '#/fleet'],
      ['Service detail', `#/fleet/services/${e(k.service)}`],
      ['Revision detail', `#/fleet/revisions/${e(k.revision)}`],
      ['Target detail', `#/fleet/targets/${e(k.target)}`],
      ['Change analysis', `#/fleet/changes/${e(k.service)}`],
    ];

    const all: RoleSample[] = [];
    for (const [label, hash] of routes) {
      await boot(page, hash);
      if (label === 'Change analysis') await runAnalysis(page);
      const s = await sampleRoles(page, label);
      assertPageHierarchy(s, `mobile ${label}`, await normalBody(page));
      all.push(...s);
    }
    assertRoleCoherence(all, 'mobile');

    // Body text still has to be readable, not merely smaller than the headings: a ramp
    // can be perfectly ordered and entirely unreadable. 12px is the floor below which
    // this stops being a design choice.
    const body = all.filter((x) => x.role === 't-body' || x.role === 't-body-2');
    expect(body.length).toBeGreaterThan(0);
    for (const b of body) {
      expect(b.size, `${b.route}: body text at ${b.size}px "${b.text}"`).toBeGreaterThanOrEqual(12);
    }
  });
});

/**
 * The "On this page" navigator on a phone.
 *
 * page-toc.spec.ts measures the desktop rail and the same control at 900px; neither is a
 * phone. Three claims only exist here: the summary is a touch target on a 393px screen, a
 * chosen section lands BELOW the sticky header rather than underneath it, and the marker
 * saying where the reader is answers a TOUCH scroll. The second is the mobile-only
 * failure -- `scrollIntoView({block: 'start'})` puts the section at viewport top, which on
 * a page with a sticky navbar is behind it, and the reader is told they arrived at a
 * heading they cannot see. The third is mobile-only because a finger, not a wheel, is what
 * hands the answer back from a chosen entry to the page's own geometry.
 */
test('the contents navigator is the same control on a phone, and lands clear of the header', async ({ page }) => {
  test.setTimeout(240_000);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const k = await canonicalKeys(page);
  await boot(page, `#/fleet/revisions/${encodeURIComponent(k.revision)}`);
  const toc = page.locator('[data-testid="page-toc"]');
  await expect(toc).toBeVisible({ timeout: 30_000 });

  const closed = await page.evaluate(() => {
    const nav = document.querySelector('[data-testid="page-toc"]')!;
    const s = nav.querySelector('summary')!.getBoundingClientRect();
    return {
      open: (nav.querySelector('details') as HTMLDetailsElement).open,
      height: Math.round(s.height),
      right: Math.round(s.right),
      width: window.innerWidth,
      sticky: getComputedStyle(nav).position,
      // Measured against the product's OWN declared minimum, not a number invented here.
      // A tap target that meets a standard this product does not use, while the button
      // beside it meets a different one, is not an accessible product -- it is two.
      touchMin: parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--touch-min')),
    };
  });
  expect(closed.open, 'the contents list opened by default where it costs a screenful').toBe(false);
  // WCAG 2.5.8 (AA) asks for 24x24 CSS px; the product declares more than that and this
  // control has to honour what the product declared.
  expect(closed.touchMin).toBeGreaterThanOrEqual(24);
  expect(closed.height, `the contents summary is ${closed.height}px, under the product's own ${closed.touchMin}px minimum`)
    .toBeGreaterThanOrEqual(closed.touchMin);
  expect(closed.right).toBeLessThanOrEqual(closed.width);
  expect(closed.sticky, 'a sticky rail on a phone would eat the column it sits in').not.toBe('sticky');

  await toc.locator('summary').click();
  const labels = (await toc.locator('.toc-link').allTextContents()).map((s) => s.trim());
  expect(labels.length).toBeGreaterThanOrEqual(3);
  // The LAST entry, so the jump has somewhere to go on a page this long.
  const label = labels[labels.length - 1];
  await page.getByRole('button', { name: label, exact: true }).click();

  const after = await page.evaluate((wanted: string) => {
    const el = [...document.querySelectorAll('[data-toc][id]')].find((e) => e.getAttribute('data-toc') === wanted)!;
    const header = document.querySelector('header.navbar')!.getBoundingClientRect();
    const b = el.getBoundingClientRect();
    return {
      collapsed: el.tagName === 'DETAILS' && !(el as HTMLDetailsElement).open,
      top: Math.round(b.top),
      headerBottom: Math.round(header.bottom),
      viewport: window.innerHeight,
    };
  }, label);

  expect(after.collapsed, `"${label}" was still closed after being chosen from the contents`).toBe(false);
  expect(after.top, `"${label}" landed under the sticky header (${after.top} < ${after.headerBottom})`)
    .toBeGreaterThanOrEqual(after.headerBottom);
  expect(after.top).toBeLessThan(after.viewport);

  // The same one control also says where the reader is, on the same one implementation:
  // the section they chose, marked exactly once.
  const current = () => toc.locator('.toc-link[aria-current="true"]').allTextContents();
  expect((await current()).map((s) => s.trim())).toEqual([label]);

  // And a finger releases it. `touchstart` is the phone's "I am driving now" signal --
  // without it the marker would stay on the chosen entry for the rest of the session,
  // which on a phone is the only way most readers ever scroll.
  const spot = await page.evaluate(() => {
    // An inert pixel, found rather than guessed: a tap that happens to land on a link
    // would navigate, and this test would then be measuring a different page.
    for (let y = 120; y < window.innerHeight - 20; y += 20) {
      const el = document.elementFromPoint(8, y);
      if (el && !el.closest('a,button,summary,input,select,[role="button"]')) return { x: 8, y };
    }
    return null;
  });
  expect(spot, 'no inert pixel on this screen to tap').not.toBeNull();
  await page.touchscreen.tap(spot!.x, spot!.y);
  for (let i = 0; i < 20 && await page.evaluate(() => window.scrollY) > 0; i++) {
    await page.mouse.wheel(0, -4000);
  }
  expect(await page.evaluate(() => Math.round(window.scrollY))).toBe(0);
  await expect.poll(async () => (await current()).map((s) => s.trim()), { timeout: 15_000 }).toEqual([labels[0]]);
});

/**
 * A ranked bar row is a label, its number, and a bar under both. At a narrow viewport
 * the bar drops to a line of its own, and grid auto-flow used to push the NUMBER down
 * with it onto a third line -- so every row was half again as tall and the value sat
 * separated from the label it belonged to by the bar between them.
 */
test('a ranked bar keeps its number beside its label at 393px', async ({ page }) => {
  test.setTimeout(240_000);
  await boot(page, '#/fleet/services');
  // The rankings sit behind the per-owner disclosure, which is where a figure that costs
  // ten touch-sized rows belongs on a phone.
  await page.locator('.sv-inv-more summary').click();
  await expect(page.locator('.hb-row .hb-inner').first()).toBeVisible();

  const m = await page.evaluate(() => {
    const row = document.querySelector('.hb-row .hb-inner')!;
    const box = (sel: string) => {
      const b = row.querySelector(sel)!.getBoundingClientRect();
      return { top: Math.round(b.top), bottom: Math.round(b.bottom), left: Math.round(b.left), width: Math.round(b.width) };
    };
    return { row: Math.round(row.getBoundingClientRect().width), label: box('.hb-label'), value: box('.hb-value'), track: box('.hb-track') };
  });

  // Same line, number to the right of the name.
  expect(m.value.top, `label at y=${m.label.top}, value at y=${m.value.top}`).toBe(m.label.top);
  expect(m.value.left).toBeGreaterThan(m.label.left);
  // The bar is under both, and spans the row -- proving this ran at the narrow layout
  // and not at the desktop one, where the track sits between label and value.
  expect(m.track.top).toBeGreaterThanOrEqual(m.label.bottom);
  expect(m.track.width).toBe(m.row);
});
