import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';
import { installScaleFixture } from './layoutGate';

// The demo's newsletter-service declares nothing; its pacto.inferred.yaml holds what
// Pacto read from deploy/deployment.yaml. Against the built WASM demo: both pages list
// those values apart from the contract, each with one mark whose note names the source,
// and Change analysis lists the service as a consumer the record names, never with a
// known verdict.

const T = 30_000;
const SOURCE = /Source: deploy\/deployment\.yaml:\d+/;

function watchConsole(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', (e) => errors.push(e.message));
  return errors;
}

async function openRevision(page: Page) {
  await page.goto('/#/fleet/revisions?service=newsletter-service');
  const link = page.locator('main a[href*="/fleet/revisions/"]').first();
  await expect(link).toBeVisible({ timeout: T });
  await page.goto(`/${await link.getAttribute('href')}`);
}

test.describe('WASM demo — a service Pacto completes into an inferred record', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('the service page holds the inferred values in one collapsed block', async ({ page }) => {
    const errors = watchConsole(page);
    await appReady(page);
    await page.goto('/#/fleet/services/newsletter-service');
    const block = page.getByTestId('service-inferred');
    await expect(block).toBeVisible({ timeout: T });
    const fold = block.locator('details').first();
    await expect(fold).not.toHaveAttribute('open', /.*/);
    await expect(block).toContainText('Inferred by Pacto');
    await fold.locator('summary').first().click();
    // Five rows show and the rest sit behind one "N more" disclosure.
    await block.locator('.ifl-more > summary').click();
    const dep = block.getByTestId('inferred-field').filter({ hasText: 'dependencies[notification-worker]' });
    await expect(dep).toHaveCount(1);
    const secret = block.getByTestId('inferred-field').filter({ hasText: 'configuration.SMTP_PASSWORD' });
    await expect(secret).toContainText('string, writeOnly');
    await dep.getByRole('button', { name: 'About inferred dependencies[notification-worker]' }).click();
    await expect(block.getByRole('note')).toContainText(SOURCE);
    await expect(block.getByRole('note')).toContainText('Confidence: likely');
    // Every row carries exactly one mark, named after its field.
    const rows = block.getByTestId('inferred-field');
    const n = await rows.count();
    for (let i = 0; i < n; i++) {
      const field = (await rows.nth(i).locator('code').textContent())?.trim() ?? '';
      await expect(rows.nth(i).getByRole('button')).toHaveAccessibleName(`About inferred ${field}`);
    }
    expect(errors).toEqual([]);
  });

  test('the revision page lists the same fields, five shown and the rest behind more', async ({ page }) => {
    const errors = watchConsole(page);
    await appReady(page);
    await openRevision(page);
    const inferred = page.getByTestId('revision-inferred');
    await expect(inferred).toBeVisible({ timeout: T });
    const visible = inferred.locator('.ifl > .ifl-list > [data-testid="inferred-field"]');
    await expect(visible).toHaveCount(5);
    const more = inferred.locator('.ifl-more');
    await expect(more.locator('> summary')).toContainText(/\d+ more/);
    await more.locator('> summary').click();
    await expect(inferred.getByTestId('inferred-field').filter({ hasText: 'dependencies[notification-worker]' })).toHaveCount(1);
    await expect(inferred.getByTestId('inferred-field').filter({ hasText: 'configuration.SMTP_PASSWORD' })).toContainText('string, writeOnly');
    await inferred.getByRole('button', { name: 'About inferred configuration.SMTP_PASSWORD' }).click();
    await expect(inferred.getByRole('note')).toContainText(SOURCE);
    expect(errors).toEqual([]);
  });

  test('neighbouring marks never share a touch target', async ({ page }) => {
    await appReady(page);
    await openRevision(page);
    const marks = page.getByTestId('revision-inferred').locator('.ifl > .ifl-list .help-btn');
    await expect(marks).toHaveCount(5, { timeout: T });
    const boxes = await marks.evaluateAll((els) => els.map((e) => { const r = e.getBoundingClientRect(); return [r.top, r.bottom]; }));
    for (let i = 1; i < boxes.length; i++) expect(boxes[i][0], `mark ${i} overlaps mark ${i - 1}`).toBeGreaterThanOrEqual(boxes[i - 1][1] - 0.5);
  });

  test('a record Pacto could not read is named on both pages', async ({ page }) => {
    const errors = watchConsole(page);
    await page.addInitScript(installScaleFixture);
    await appReady(page);
    await page.evaluate(() => { (window as unknown as { __pactoInferredUnreadable?: boolean }).__pactoInferredUnreadable = true; });
    await page.goto('/#/fleet/services/newsletter-service');
    const block = page.getByTestId('service-inferred');
    // Folded, the head already says the values are unknown, never absent.
    await expect(block.locator('summary').first()).toContainText('Could not be read', { timeout: T });
    await block.locator('summary').first().click();
    await expect(block).toContainText('Pacto could not read the inferred record');
    await expect(block.getByTestId('inferred-field')).toHaveCount(0);
    // The revision keeps the values a local read infers under the same line.
    await openRevision(page);
    const inferred = page.getByTestId('revision-inferred');
    await expect(inferred).toContainText('Pacto could not read the inferred record', { timeout: T });
    await expect(inferred.getByTestId('inferred-field').filter({ hasText: 'dependencies[' })).toHaveCount(0);
    expect(errors).toEqual([]);
  });

  test('removing notification-worker lists newsletter-service as an inferred consumer', async ({ page }) => {
    const errors = watchConsole(page);
    await appReady(page);
    await page.goto('/#/fleet/changes/notification-worker?event=removed');
    await expect(page.getByTestId('changes-answer')).toBeVisible({ timeout: T });
    const fold = page.locator('#sec-affected-consumers');
    if ((await fold.getAttribute('open')) === null) await fold.locator('> summary').click();
    // The assertion is on the row, never on the gate: orders-service declares the dependency.
    const row = page.getByTestId('consumer-row').filter({ hasText: 'newsletter-service' });
    await expect(row).toHaveCount(1, { timeout: T });
    const why = row.locator('td[data-label="Why"]');
    // One discreet mark: the pill, whose title names the file the record read it from.
    await expect(why.locator('.cell-badges .badge')).toHaveCount(0);
    await expect(why.locator('.pill')).toHaveText('inferred');
    await expect(why.locator('.pill')).toHaveAttribute('title', /named by its inferred record, read from deploy\/deployment\.yaml:\d+/);
    expect(errors).toEqual([]);
  });
});

test.describe('WASM demo — the note of an inferred value on a phone', () => {
  test.use({ viewport: { width: 1280, height: 900 }, hasTouch: true }); // the app opens wide, then the phone size is set

  test('the note of a mark fits the viewport', async ({ page }) => {
    await appReady(page);
    await page.setViewportSize({ width: 320, height: 568 });
    await page.goto('/#/fleet/services/newsletter-service');
    const block = page.getByTestId('service-inferred');
    await expect(block).toBeVisible({ timeout: T });
    await block.locator('summary').first().click();
    const rows = block.locator('.ifl > .ifl-list [data-testid="inferred-field"]');
    await expect(rows.first()).toBeVisible();
    for (let i = 0; i < await rows.count(); i++) {
      await rows.nth(i).getByRole('button').tap();
      const note = rows.nth(i).getByRole('note');
      await expect(note).toBeVisible();
      const box = (await note.boundingBox())!;
      expect(box.x, `note ${i} starts before the viewport`).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width, `note ${i} runs past the viewport`).toBeLessThanOrEqual(320);
    }
  });
});
