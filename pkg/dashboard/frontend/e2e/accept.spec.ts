import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';
import { acceptPlanRoute, installScaleFixture } from './layoutGate';

// "Accept all proposed ranges" from the service page's Inferred block, and the break
// marks and Ranges line of Change analysis (508-S7). The published demo bundles propose
// nothing, so the scale fixture puts the proposals, the plan route's answers and the
// break states on the page. The dashboard shows the plan and the command; it never
// signs or writes.

const T = 30_000;

function watchConsole(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', (e) => errors.push(e.message));
  return errors;
}

test.describe('accept proposed ranges and announced breaks', () => {
  test.use({ viewport: { width: 1280, height: 900 } });
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(installScaleFixture);
    await appReady(page);
  });

  test('the plan shows its ranges, the routed line and a copyable command', async ({ page }) => {
    const errors = watchConsole(page);
    await acceptPlanRoute('plan')(page);
    const plan = page.getByTestId('accept-plan');
    await expect(plan.locator('.apr-list li')).toHaveCount(3);
    await expect(plan).toContainText('--proposed --intent sha256:');
    await expect(plan.getByRole('button', { name: 'Copy command' })).toBeVisible();
    const waiting = plan.getByTestId('accept-waiting');
    await expect(waiting).toHaveCount(1);
    await expect(waiting).toContainText('Waiting for the owner');
    await waiting.getByRole('button', { name: 'About routed acceptance' }).click();
    await expect(plan.getByRole('note')).toContainText('waiting for the owner');
    expect(errors).toEqual([]);
  });

  test('a refusal shows its message and the command a person runs', async ({ page }) => {
    await acceptPlanRoute('refused')(page);
    const block = page.getByTestId('service-inferred');
    await expect(block.getByTestId('accept-refusal')).toContainText('proposes no dependency range to accept');
    await expect(block.getByTestId('accept-plan')).toContainText('pacto ci --publish');
    await expect(block.getByRole('button', { name: 'Copy command' })).toBeVisible();
  });

  test('Change analysis prints the Ranges line and marks each announced consumer', async ({ page }) => {
    await page.evaluate(() => { (window as unknown as { __pactoAccept?: string }).__pactoAccept = 'breaks'; });
    await page.goto('/#/fleet/changes/payments-service');
    for (const [sel, v] of [['#impact-old-rev', '1.0.0'], ['#impact-new-rev', '2.0.1']]) {
      const value = () => page.locator(`${sel} option`).evaluateAll((os, ver) => (os as HTMLOptionElement[]).find((o) => (o.textContent ?? '').startsWith(`${ver} `))?.value ?? '', v);
      await expect.poll(value, { timeout: 20_000 }).not.toBe('');
      await page.locator(sel).selectOption(await value());
    }
    await page.getByRole('button', { name: /Compare revisions/ }).click();
    const line = page.getByTestId('changes-ranges');
    await expect(line).toContainText('Ranges: 41 declared and 6 accepted of 58 dependency edges (81%)', { timeout: T });
    await expect(line.getByTestId('changes-note')).toContainText('POLICY_WARNING');
    await page.locator('#sec-affected-consumers > summary').click();
    const marks = page.getByTestId('consumer-break');
    await expect(marks.first()).toBeVisible({ timeout: T });
    await expect(marks.first().getByRole('button')).toHaveAccessibleName(/^About accepted /);
  });
});
