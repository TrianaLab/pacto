import { test, expect, type Page } from '@playwright/test';

// An attested owner, against the built WASM demo: newsletter-service's owner was read
// from its catalog-info.yaml at the commit a CI identity signed (the demo stands in for
// that publish), so the owner carries the one provenance mark and its tip names where
// it was read. A real source can be long (a path, a line, a commit and a workflow
// identity), so the tip is checked with a 200-character source at a 320px touch phone
// and a 1920px desktop: inside the viewport, no horizontal scroll, the whole source
// readable and, under a coarse pointer, a 44px target.

const T = 20_000;
const SERVICE = 'newsletter-service';
const LONG_SOURCE = `catalog-info.yaml:7 at 4f1c2a9, signed by github.com/trianalab/${'a'.repeat(60)}/${'b'.repeat(40)}/.github/workflows/ci.yml@refs/heads/main`.padEnd(200, 'x');

/** Gives newsletter-service's detail a long ownership.source; every other answer is the engine's own. */
function installLongSource(source: string): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null) => Promise<ServeResult>;
  let real: Serve | null = null;
  const wrapped: Serve = async (method, path, body) => {
    const res = await (real as Serve)(method, path, body);
    const [route, search] = path.split('?');
    if (route !== '/api/fleet/entities/service' || new URLSearchParams(search || '').get('key') !== 'newsletter-service' || res.status !== 200) return res;
    const doc = JSON.parse(res.body);
    if (doc.service?.ownership) doc.service.ownership.source = source;
    return { status: res.status, body: JSON.stringify(doc), contentType: res.contentType };
  };
  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}

async function openService(page: Page) {
  await page.goto(`/#/fleet/services/${SERVICE}`);
  await expect(page.getByRole('heading', { level: 1, name: /^Service: / })).toBeVisible({ timeout: T });
  const mark = page.getByTestId('service-owner').getByTestId('owner-provenance');
  await expect(mark).toBeVisible({ timeout: T });
  await mark.evaluate((el) => el.scrollIntoView({ block: 'center' }));
  return mark;
}

/** The open tip lies inside the viewport, the page does not scroll sideways and the tip carries the whole source. */
async function expectTipFits(page: Page, mark: ReturnType<Page['getByTestId']>) {
  const tip = mark.locator('.help-body');
  await expect(tip).toBeVisible();
  await expect(tip).toContainText(LONG_SOURCE);
  const vp = page.viewportSize()!;
  const box = (await tip.boundingBox())!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(vp.width);
  expect(box.y + box.height).toBeLessThanOrEqual(vp.height);
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
}

test.describe('WASM demo — attested owner', () => {
  test('newsletter-service reads its owner as attested, with the file it came from', async ({ page }) => {
    await openService(page);
    const ownership = await page.evaluate(async (key) =>
      (await (await fetch(`/api/fleet/entities/service?key=${key}`)).json()).service?.ownership, SERVICE);
    expect(ownership?.provenance).toBe('attested');
    expect(ownership?.source).toContain('catalog-info.yaml');
    // The revision declares no owner, so the page never says the owner was declared there.
    await expect(page.getByTestId('service-owner').getByTestId('owner-declared-in')).toHaveCount(0);
    await expect(page.getByTestId('service-owner')).not.toContainText('Declared in');
  });

  test.describe('phone, touch', () => {
    test.use({ viewport: { width: 320, height: 568 }, hasTouch: true, isMobile: true });
    test.beforeEach(async ({ page }) => { await page.addInitScript(installLongSource, LONG_SOURCE); });

    test('the tip opens by keyboard focus and by tap, fits and keeps a 44px target', async ({ page }) => {
      const mark = await openService(page);
      expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches)).toBe(true);
      const btn = mark.getByRole('button', { name: 'About owner provenance' });
      const hit = (await btn.boundingBox())!;
      expect(hit.width).toBeGreaterThanOrEqual(44);
      expect(hit.height).toBeGreaterThanOrEqual(44);
      await btn.focus();
      await expectTipFits(page, mark);
      await btn.blur();
      await expect(mark.locator('.help-body')).toBeHidden();
      await btn.tap();
      await expectTipFits(page, mark);
    });
  });

  test.describe('desktop', () => {
    test.use({ viewport: { width: 1920, height: 1080 } });
    test.beforeEach(async ({ page }) => { await page.addInitScript(installLongSource, LONG_SOURCE); });

    test('the tip opens by keyboard focus and by click and fits', async ({ page }) => {
      const mark = await openService(page);
      const btn = mark.getByRole('button', { name: 'About owner provenance' });
      await btn.focus();
      await expectTipFits(page, mark);
      await btn.blur();
      await expect(mark.locator('.help-body')).toBeHidden();
      await btn.click();
      await expectTipFits(page, mark);
    });
  });
});
