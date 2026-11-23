import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { gateMode, toggleSidebar } from './layoutGate';

// The fleet map on the narrative demo (509-S12): the Overview leads with it, the
// Operational graph is the same map full screen, and a service focus is its left-to-right
// diagram. The map is a canvas, so the spec reads what it drew through the test hooks the
// map publishes on window.__fleetMap: the camera (view), the labels last drawn (labels)
// and the end of motion (idle). The words a reader gets are the HUD, the rail, the focus
// bar and the live region, and those are read as the page shows them.

type Label = { text: string; x: number; y: number; w: number; h: number; size: number; font: string };
type MapView = { k: number; tx: number; ty: number; level: number };
type Hooks = { labels: () => Label[]; idle: () => Promise<void>; view: () => MapView };
const T = 30_000;

const hooks = (page: Page) => page.waitForFunction(() => !!(window as unknown as { __fleetMap?: Hooks }).__fleetMap, null, { timeout: T });
/** idle waits until the map stops moving and loading. */
async function idle(page: Page) {
  await hooks(page);
  await page.evaluate(() => (window as unknown as { __fleetMap: Hooks }).__fleetMap.idle());
}
const view = (page: Page) => page.evaluate(() => (window as unknown as { __fleetMap: Hooks }).__fleetMap.view());

/** openMap opens a hash and waits for the engine, the map's answer and the end of motion. */
async function openMap(page: Page, hash = '#/fleet') {
  await page.goto(`/${hash}`);
  await page.evaluate(() => (window as unknown as { __pactoReady?: Promise<void> }).__pactoReady);
  await expect(page.getByTestId('fleet-map-summary')).toBeVisible({ timeout: T });
  await idle(page);
}

const canvas = (page: Page) => page.getByTestId('fleet-map').locator('canvas[role="img"]');
const live = (page: Page) => page.getByTestId('fleet-map-live');

test.describe('fleet map', () => {
  test('the Overview leads with the map, its two figures and the attention rail', async ({ page }) => {
    await openMap(page);
    await expect(page.getByRole('heading', { level: 1 }).filter({ hasText: 'Is anything wrong?' })).toBeVisible();
    await expect(page.getByTestId('figure-match')).toBeVisible();
    await expect(page.getByTestId('figure-completeness')).toBeVisible();
    await expect(canvas(page)).toBeVisible();
    await expect(page.getByTestId('fleet-map-rail')).toBeVisible();
    await expect(page.getByTestId('fleet-map-rail-item').first()).toBeVisible({ timeout: T });
    await expect(live(page)).toHaveText(/^Level \d, /);
  });

  test('a lens or a grouping is written to the hash and never moves the camera', async ({ page }) => {
    await openMap(page, '#/fleet/graph');
    const before = await view(page);
    const show = page.getByRole('group', { name: 'Show' });
    await show.getByRole('button', { name: 'Expected' }).click();
    await expect(show.getByRole('button', { name: 'Expected' })).toHaveAttribute('aria-pressed', 'true');
    await expect(page).toHaveURL(/lens=expected/);
    await idle(page);
    expect(await view(page), 'the Expected lens keeps the camera').toEqual(before);
    await show.getByRole('button', { name: 'Observed' }).click();
    await expect(page).toHaveURL(/lens=observed/);
    await idle(page);
    expect(await view(page), 'the Observed lens keeps the camera').toEqual(before);

    const group = page.getByRole('group', { name: 'Group by' });
    await group.getByRole('button', { name: 'Owner team' }).click();
    await expect(page).toHaveURL(/groupBy=team/);
    await expect(page.getByTestId('fleet-map-summary')).toContainText('team');
    await group.getByRole('button', { name: 'Environment' }).click();
    await expect(page).toHaveURL(/groupBy=environment/);
    await expect(page.getByTestId('fleet-map-summary')).toContainText('environment');
    // Back restores the previous view's state from the hash.
    await page.goBack();
    await expect(page).toHaveURL(/groupBy=team/);
    await expect(group.getByRole('button', { name: 'Owner team' })).toHaveAttribute('aria-pressed', 'true');
  });

  test('Find focuses a service and Escape returns to the whole fleet', async ({ page }) => {
    await openMap(page, '#/fleet/graph');
    await page.locator('[data-testid="fleet-map-search"] input[type=search]').fill('orders');
    await page.locator('[data-testid="fleet-map-find-option"]').filter({ hasText: 'orders-service' }).first().click();
    await expect(page.getByTestId('fleet-map-focus')).toContainText('orders-service', { timeout: T });
    await expect(page).toHaveURL(/focus=orders-service|graph\/service\/orders-service/);
    await expect(live(page)).toContainText('Focus on orders-service');
    await idle(page);
    // One more hop folds nothing on the full page (its rail starts closed) and says so.
    await page.getByTestId('fleet-map-focus').getByRole('button', { name: 'One more hop' }).click();
    await expect(page.getByTestId('fleet-map-focus')).toContainText('one more hop');
    await canvas(page).focus();
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('fleet-map-focus')).toHaveCount(0);
    await expect(live(page)).toHaveText(/^Level \d, /);
  });

  test('the rail hides and opens, focuses an item and lands filtered from an attention link', async ({ page }) => {
    await openMap(page);
    await page.getByTestId('fleet-map-rail').getByRole('button', { name: 'Hide what needs attention' }).click();
    await expect(page.getByTestId('fleet-map-rail')).toHaveCount(0);
    await expect(page).toHaveURL(/rail=closed/);
    await page.getByTestId('fleet-map-rail-open').click();
    await expect(page.getByTestId('fleet-map-rail')).toBeVisible();
    await page.getByTestId('fleet-map-rail-item').first().click();
    await expect(page.getByTestId('fleet-map-focus')).toBeVisible({ timeout: T });

    // Every Needs attention link lands on the Overview with the rail filtered.
    await openMap(page, '#/fleet/attention?category=health');
    const filtered = page.getByTestId('fleet-map-rail-filtered');
    await expect(filtered).toBeVisible({ timeout: T });
    await filtered.getByRole('button', { name: 'Clear' }).click();
    await expect(filtered).toHaveCount(0);
    await expect(page.getByTestId('fleet-map-rail')).toBeVisible();
  });

  test('the keyboard zooms, pans, fits and finds', async ({ page }) => {
    await openMap(page, '#/fleet/graph');
    const fit = await view(page);
    await canvas(page).focus();
    await page.keyboard.press('+');
    await idle(page);
    const zoomed = await view(page);
    expect(zoomed.k, 'plus zooms in').toBeGreaterThan(fit.k);
    await page.keyboard.press('ArrowLeft');
    await idle(page);
    expect((await view(page)).tx, 'the left arrow pans').toBeGreaterThan(zoomed.tx);
    await page.keyboard.press('-');
    await page.keyboard.press('0');
    await idle(page);
    expect((await view(page)).k, '0 fits everything').toBeCloseTo(fit.k, 5);
    await page.keyboard.press('/');
    await expect(page.locator('[data-testid="fleet-map-search"] input[type=search]')).toBeFocused();
  });

  test.describe('with reduced motion', () => {
    test.use({ reducedMotion: 'reduce' });
    test('a level change jumps without animation', async ({ page }) => {
      await openMap(page, '#/fleet/graph');
      await page.getByRole('group', { name: 'Level of detail' }).getByRole('button', { name: 'Names' }).click();
      // No frame needs to pass: the camera is already there.
      expect((await view(page)).level).toBe(2);
      await expect(live(page)).toHaveText(/^Level 2, Names/);
    });
  });

  for (const theme of ['dark', 'light'] as const) {
    test(`axe finds nothing on the map, full screen and in focus (${theme})`, async ({ page }) => {
      if (theme === 'light') await page.addInitScript(() => { try { localStorage.setItem('pacto-theme', 'light'); } catch { /* private mode */ } });
      const problems: string[] = [];
      for (const hash of ['#/fleet', '#/fleet/graph', '#/fleet/graph/service/orders-service?hops=2']) {
        await openMap(page, hash);
        const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
        for (const v of axe.violations) problems.push(`${hash}: axe ${v.id} on ${v.nodes.slice(0, 3).map((n) => n.target.join(' ')).join(', ')}`);
      }
      expect(problems).toEqual([]);
    });
  }

  test('the Overview keeps its scroll as the reader uses the map', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 720 });
    await openMap(page);
    // The map's toolbar near the top of the window, clear of the sticky header: every control
    // the test clicks is in view, so any scroll is the page's doing.
    await page.getByRole('group', { name: 'Show' }).evaluate((el) => window.scrollTo(0, el.getBoundingClientRect().top + window.scrollY - 160));
    const y = await page.evaluate(() => window.scrollY);
    expect(y, 'the map is below the page header').toBeGreaterThan(0);
    await page.getByRole('group', { name: 'Show' }).getByRole('button', { name: 'Expected' }).click();
    await expect(page).toHaveURL(/lens=expected/);
    expect(await page.evaluate(() => window.scrollY), 'a lens keeps the scroll').toBe(y);
    await page.getByTestId('fleet-map-rail-item').first().click();
    await expect(page.getByTestId('fleet-map-focus')).toBeVisible({ timeout: T });
    expect(Math.abs(await page.evaluate(() => window.scrollY) - y), 'a rail item keeps the scroll').toBeLessThanOrEqual(120);
    // The wheel over the map scrolls the page until the reader clicks into it.
    await page.getByTestId('fleet-map-focus').getByRole('button', { name: 'Back to whole fleet' }).click();
    const before = await view(page);
    const box = (await canvas(page).boundingBox())!;
    await page.mouse.move(box.x + box.width / 3, box.y + box.height / 2);
    const y2 = await page.evaluate(() => window.scrollY);
    await page.mouse.wheel(0, 200);
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(y2);
    expect((await view(page)).k, 'the wheel did not zoom the map').toBe(before.k);
  });

  for (const theme of ['dark', 'light'] as const) {
    test(`the modes, the picker, a selection and a group tooltip, from their controls (${theme})`, async ({ page }) => {
      test.setTimeout(180_000);
      if (theme === 'light') await page.addInitScript(() => { try { localStorage.setItem('pacto-theme', 'light'); } catch { /* private mode */ } });
      const problems: string[] = [];
      const axe = async (at: string) => {
        const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
        for (const v of r.violations) problems.push(`${at}: axe ${v.id} on ${v.nodes.slice(0, 3).map((n) => n.target.join(' ')).join(', ')}`);
      };
      await openMap(page);
      // The match tile shows the gaps on the map, with a chip per kind of gap.
      await page.getByTestId('figure-match').getByRole('button').first().click();
      await expect(page).toHaveURL(/gaps=all/);
      const chips = page.getByRole('group', { name: 'Kinds of gap' }).getByRole('button');
      await expect(chips).toHaveCount(3);
      await axe('gaps');
      await chips.first().click();
      await expect(chips.first()).toHaveAttribute('aria-pressed', 'false');
      await page.getByTestId('fleet-map-mode').getByRole('button', { name: 'Show everything' }).click();
      await expect(page.getByTestId('fleet-map-mode')).toHaveCount(0);
      // Only problems, from the toolbar.
      await page.getByRole('button', { name: 'Only problems' }).click();
      await expect(page.getByTestId('fleet-map-mode')).toContainText('Only problems.');
      await axe('only problems');
      await page.getByRole('button', { name: 'Only problems' }).click();
      // A Most missing row opens the map, or a list, filtered on that section.
      const row = page.getByTestId('completeness-most-missing').getByRole('link').first();
      await row.click();
      await expect(page).toHaveURL(/missing=|gap=/);
      await page.goto('/#/fleet?missing=configuration');
      await expect(page.getByTestId('fleet-map-mode')).toContainText('Missing contract sections.', { timeout: T });
      await axe('missing');
      // The environment picker.
      await openMap(page, '#/fleet/graph');
      const picker = page.locator('.fm-env select');
      await picker.focus();
      await expect(picker.locator('option')).not.toHaveCount(1, { timeout: T });
      await picker.selectOption({ index: 1 });
      await expect(page).toHaveURL(/env=/);
      await axe('environment picked');
      // A selected service, opened from its panel.
      await page.getByRole('group', { name: 'Level of detail' }).getByRole('button', { name: 'Names' }).click();
      await idle(page);
      expect(await page.evaluate(() => (window as unknown as { __fleetMap: { select: () => boolean } }).__fleetMap.select())).toBe(true);
      await expect(page.getByTestId('fleet-map-selection')).toBeVisible();
      await axe('selection');
      await page.getByTestId('fleet-map-selection').getByRole('button', { name: 'Focus' }).click();
      await expect(page.getByTestId('fleet-map-focus')).toBeVisible({ timeout: T });
      await page.getByTestId('fleet-map-focus').getByRole('button', { name: 'Back to whole fleet' }).click();
      // A group's tooltip and its action.
      await page.getByRole('group', { name: 'Level of detail' }).getByRole('button', { name: 'Groups' }).click();
      await idle(page);
      expect(await page.evaluate(() => (window as unknown as { __fleetMap: { pin: () => boolean } }).__fleetMap.pin())).toBe(true);
      const tip = page.getByTestId('fleet-map-group-tip');
      await expect(tip).toBeVisible();
      await axe('group tooltip');
      const action = tip.getByRole('button', { name: 'What this group is missing' });
      if (await action.count()) {
        await action.click();
        await expect(page.getByTestId('fleet-map-mode')).toContainText('Missing contract sections.');
      }
      expect(problems).toEqual([]);
    });
  }

  test.describe('on a phone', () => {
    test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });
    test('the map fits the width, its controls take a finger and the rail opens', async ({ page }) => {
      await openMap(page);
      const scroll = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(scroll, 'no horizontal scroll').toBeLessThanOrEqual(0);
      // On a map this narrow the rail starts closed: the zoom buttons are there on load.
      await expect(page.getByTestId('fleet-map-rail')).toHaveCount(0);
      const zoomIn = page.getByRole('button', { name: 'Zoom in' });
      await expect(zoomIn).toBeVisible();
      for (const name of ['Zoom in', 'Zoom out', 'Fit everything']) {
        const box = (await page.getByRole('button', { name }).boundingBox())!;
        expect(Math.min(box.width, box.height), `${name} is at least 44px`).toBeGreaterThanOrEqual(44);
      }
      const before = await view(page);
      await zoomIn.tap();
      await idle(page);
      expect((await view(page)).k, 'a tap on Zoom in zooms').toBeGreaterThan(before.k);
      await page.getByTestId('fleet-map-rail-open').tap();
      await expect(page.getByTestId('fleet-map-rail')).toBeVisible();
    });

    test('a selected service never covers the breadcrumbs or the level ladder', async ({ page }) => {
      await openMap(page, '#/fleet/graph');
      await page.getByRole('group', { name: 'Level of detail' }).getByRole('button', { name: 'Names' }).tap();
      await idle(page);
      expect(await page.evaluate(() => (window as unknown as { __fleetMap: { select: () => boolean } }).__fleetMap.select())).toBe(true);
      const panel = (await page.getByTestId('fleet-map-selection').boundingBox())!;
      const hud = (await page.locator('.fm-hud').boundingBox())!;
      expect(panel.y, 'the panel starts under the HUD').toBeGreaterThanOrEqual(hud.y + hud.height);
      await page.getByRole('group', { name: 'Level of detail' }).getByRole('button', { name: 'Groups' }).tap();
      await idle(page);
      expect((await view(page)).level).toBe(0);
    });
  });
});

// The drawn text (SPEC 24.6): G1 skips canvas, so the map's own labels are checked here
// at every viewport from a 320px phone to a 3440px ultrawide, with the sidebar in both
// states where it is a column of its own, Verdana forced: no two labels intersect, none
// is under 12.19px and none crosses the canvas edge.
const VIEWPORTS: [number, number][] = [[320, 568], [390, 844], [844, 390], [768, 1024], [1024, 768], [1280, 720], [1440, 900], [1920, 1080], [2560, 1440], [3440, 1440]];

async function labelProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const ls = (window as unknown as { __fleetMap: Hooks }).__fleetMap.labels();
    const c = document.querySelector('[data-testid="fleet-map"] canvas[role="img"]')!.getBoundingClientRect();
    const out: string[] = [];
    const E = 0.5;
    for (const l of ls) {
      if (l.size < 12.19) out.push(`"${l.text}" is ${l.size}px, under 12.19px`);
      if (!l.font.includes('Verdana')) out.push(`"${l.text}" is drawn in ${l.font}, not the forced wide font`);
      if (l.x < -E || l.y < -E || l.x + l.w > c.width + E || l.y + l.h > c.height + E) out.push(`"${l.text}" crosses the canvas edge`);
    }
    for (let i = 0; i < ls.length; i++) {
      for (let j = i + 1; j < ls.length; j++) {
        const a = ls[i], b = ls[j];
        if (a.x < b.x + b.w - E && b.x < a.x + a.w - E && a.y < b.y + b.h - E && b.y < a.y + a.h - E) out.push(`"${a.text}" intersects "${b.text}"`);
      }
    }
    return out;
  });
}

/** toLevel brings the map to a level through its ladder. */
const toLevel = (name: string) => async (page: Page) => {
  await page.getByRole('group', { name: 'Level of detail' }).getByRole('button', { name }).click();
  await idle(page);
};

const STATES: [string, string, ((page: Page) => Promise<void>) | null][] = [
  ['Groups', '#/fleet/graph', toLevel('Groups')],
  ['Names', '#/fleet/graph', toLevel('Names')],
  ['focus', '#/fleet/graph/service/orders-service?hops=2', null],
];

for (const [state, hash, prepare] of STATES) {
  test(`the map's drawn labels hold at every viewport (${state})`, async ({ page }) => {
    test.setTimeout(240_000);
    await page.setViewportSize({ width: 1280, height: 900 });
    await openMap(page, hash);
    await gateMode(page);
    const problems: string[] = [];
    const check = async (at: string) => {
      if (prepare) await prepare(page); else await idle(page);
      const ls = await page.evaluate(() => (window as unknown as { __fleetMap: Hooks }).__fleetMap.labels().length);
      if (!ls) problems.push(`${at}: no label drawn`);
      for (const p of await labelProblems(page)) problems.push(`${at}: ${p}`);
    };
    for (const [w, h] of VIEWPORTS) {
      await page.setViewportSize({ width: w, height: h });
      // The resize observer redraws on a later frame: read the labels after it.
      await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
      // A resize refits the map; the level a state needs is then asked again.
      if (state === 'focus') await expect(page.getByTestId('fleet-map-focus')).toBeVisible();
      await check(`${w}x${h}`);
      if (w >= 1200) {
        const side = await toggleSidebar(page);
        await check(`${w}x${h}, sidebar ${side}`);
        await toggleSidebar(page);
      }
    }
    expect(problems).toEqual([]);
  });
}
