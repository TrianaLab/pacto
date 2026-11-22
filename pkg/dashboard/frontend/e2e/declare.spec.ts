import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';
import { acceptPlanRoute, declareRoute, installScaleFixture } from './layoutGate';

// Declare an inferred service from the dashboard (513-S6). The demo's own declarer
// plans legacy-billing, signs nothing ("the demo has no sign-in") and keeps saved drafts
// in the tab, so the plain flow runs against it; the states the demo never reaches (a
// signing login and its return, a refusal, a draft's own plan with what nothing backs,
// and 508-S7's accept execution) come from the scale fixture.

const T = 30_000;

function watchConsole(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', (e) => errors.push(e.message));
  return errors;
}

test.describe('declare an inferred service', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('the attention row links to the panel, a checked range changes the command and Save draft lists draft-1', async ({ page }) => {
    const errors = watchConsole(page);
    await appReady(page);
    await page.goto('/#/fleet/attention?category=undeclared');
    const link = page.locator('a[data-testid="next-action-link"][href*="legacy-billing"]').first();
    await expect(link).toBeVisible({ timeout: T });
    await expect(link).toHaveAttribute('title', /^pacto declare legacy-billing --namespace shop/);
    await link.click();
    await expect(page).toHaveURL((url) => url.hash.includes('legacy-billing') && url.hash.includes('declare=1'));

    const panel = page.getByTestId('declare');
    const ranges = panel.getByTestId('declare-ranges');
    await expect(ranges).toContainText('payments-service range', { timeout: T });
    await expect(ranges).toContainText('keep unknown (proposed ^2.1.0)');
    await expect(panel.getByTestId('declare-signer')).toContainText('the demo has no sign-in');
    const command = panel.getByTestId('declare-command');
    await expect(command).toContainText('pacto declare legacy-billing --namespace shop --registry demo.pacto.local --intent sha256:');
    await expect(command).not.toContainText('--accept');

    await panel.getByRole('checkbox', { name: 'Accept payments-service range' }).check();
    await expect(command).toContainText('--accept payments-service --intent sha256:', { timeout: T });
    await expect(ranges).toContainText('^2.1.0');

    await panel.getByTestId('declare-save-draft').click();
    await expect(panel.getByTestId('declare-result')).toContainText('Saved draft-1', { timeout: T });
    await expect(panel.getByTestId('declare-accept-command')).toContainText('pacto declare legacy-billing --registry demo.pacto.local --from-draft draft-1');
    const drafts = panel.getByTestId('declare-drafts');
    await drafts.locator('> summary').click();
    await expect(drafts.getByTestId('declare-draft')).toHaveCount(1);
    await expect(drafts.getByTestId('declare-draft').first()).toContainText('draft-1 by an agent for');
    await expect(drafts.getByTestId('declare-draft').first()).toContainText('requests payments-service ^2.1.0');

    // Accepting it loads its own plan; nobody signs in the demo, so nothing publishes.
    await drafts.getByRole('button', { name: 'Accept draft-1' }).click();
    await expect(panel.getByTestId('declare-draft-plan')).toContainText('Accepting draft-1', { timeout: T });
    await expect(command).toContainText('--from-draft draft-1');
    await expect(panel.getByRole('button', { name: /^Publish/ })).toHaveCount(0);
    expect(errors).toEqual([]);
  });

  test.describe('fixture states', () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript(installScaleFixture);
      await appReady(page);
    });

    test('accepting a draft shows what nothing backs before anything is signed', async ({ page }) => {
      await declareRoute('drafts')(page);
      const panel = page.getByTestId('declare');
      await expect(panel.getByTestId('declare-draft')).toHaveCount(20);
      await expect(panel.getByTestId('declare-drafts')).toContainText('7 more');
      await expect(panel.locator('[data-testid="declare-draft"][data-state="unreadable"]')).toContainText('is unreadable');
      await expect(panel.getByTestId('declare-draft-plan')).toContainText('Accepting draft-27');
      await expect(panel.getByTestId('declare-unbacked').locator('li')).toHaveCount(5);
      await expect(panel.getByTestId('declare-unbacked')).toContainText('the baseline could not be read');
      await expect(panel.getByTestId('declare-save-draft')).toHaveCount(0);
    });

    test('Sign starts a fresh login with the ticket, and the return shows the plan with Publish', async ({ page }) => {
      await page.addInitScript(() => { (window as unknown as { __pactoDeclare?: string }).__pactoDeclare = 'signed'; });
      await page.goto('/#/fleet/services?declaration=inferred');
      const a = page.locator('main a[href*="/fleet/services/"][href*="legacy-billing"]').first();
      await expect(a).toBeVisible({ timeout: T });
      await page.goto(`/${await a.getAttribute('href')}?declare=1`);
      await page.reload();
      const sign = page.getByTestId('declare-sign');
      await expect(sign).toBeVisible({ timeout: T });
      const href = await sign.getAttribute('href') ?? '';
      expect(href).toMatch(new RegExp(`^/auth/sign\\?ticket=${'t'.repeat(43)}&return=`));
      expect(decodeURIComponent(href.split('return=')[1])).toContain('declare=1');
      expect(decodeURIComponent(href.split('return=')[1])).toContain(`signed=sha256${encodeURIComponent(':')}${'b3'.repeat(32)}`);

      await declareRoute('signed')(page);
      const panel = page.getByTestId('declare');
      await expect(panel.getByTestId('declare-signed-return')).toContainText('Signed in again as');
      await expect(panel.getByTestId('declare-ranges')).toBeVisible();
      await panel.getByRole('button', { name: 'Publish 0.1.0' }).click();
      await expect(panel.getByTestId('declare-result')).toContainText('Published 0.1.0, signed by you as', { timeout: T });
      // A published version leaves nothing to sign or save.
      await expect(panel.getByTestId('declare-sign')).toHaveCount(0);
      await expect(panel.getByTestId('declare-save-draft')).toHaveCount(0);
    });

    test('a refusal shows its message, the reason nothing writes and the command', async ({ page }) => {
      await declareRoute('refused')(page);
      const panel = page.getByTestId('declare');
      await expect(panel.getByTestId('refusal')).toHaveAttribute('data-code', 'OWNER_SIGNER_NOT_ALLOWED');
      await expect(panel.getByTestId('refusal')).toContainText('may not set the owner');
      await expect(panel.getByTestId('declare-command')).toContainText('pacto declare');
      await expect(panel.getByTestId('declare-save-draft')).toHaveCount(0);
    });

    // 508-S7's "Accept all proposed ranges", carried out from the service page.
    test('accept execution: a saved draft, a signed record and a refused draft', async ({ page }) => {
      await acceptPlanRoute('execute')(page);
      const block = page.getByTestId('service-inferred');
      await expect(block.getByTestId('accept-result')).toContainText('Saved ');
      await expect(block.getByTestId('accept-result')).toContainText('pacto-accept-draft-28');

      await page.goto(`/#/fleet/services/payments-service?signed=${encodeURIComponent(`sha256:${'a7'.repeat(32)}`)}`);
      await page.reload();
      await expect(block.getByTestId('accept-signed-return')).toContainText('Signed in again as', { timeout: T });
      await block.getByRole('button', { name: 'Attach the acceptance' }).click();
      await expect(block.getByTestId('accept-result')).toContainText('signed by you: counted', { timeout: T });
      await expect(block.getByTestId('accept-sign')).toHaveCount(0);
      await expect(block.getByTestId('accept-save-draft')).toHaveCount(0);

      // A draft accepted across the signing login: its own plan, then Attach, which
      // the person may not do on this workload.
      await acceptPlanRoute('drafts')(page);
      await block.getByTestId('accept-drafts').getByRole('button', { name: 'Accept', exact: true }).first().click();
      await expect(block.getByTestId('accept-draft-plan')).toContainText('Accepting pacto-accept-draft-27', { timeout: T });
      await expect(block.getByTestId('accept-save-draft')).toHaveCount(0);
      const intent = `sha256:${'a7'.repeat(32)}`;
      await page.evaluate((i) => sessionStorage.setItem(`pacto-accept-draft:${i}`, 'pacto-accept-draft-27'), intent);
      await page.goto(`/#/fleet/services/payments-service?signed=${encodeURIComponent(intent)}`);
      await page.reload();
      await expect(block.getByTestId('accept-draft-plan')).toBeVisible({ timeout: T });
      await block.getByRole('button', { name: 'Attach the acceptance' }).click();
      await expect(block.getByTestId('refusal')).toContainText('needs update on Deployment', { timeout: T });
    });
  });
});

test.describe('declare: the pull request', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  // 513-S8: the dashboard never opens the pull request; it hands the person the command
  // that does, with their own Git token, from their machine.
  test('the declare panel shows the pull request command from your machine (desktop and 390px)', async ({ page }) => {
    const errors = watchConsole(page);
    await appReady(page);
    await declareRoute('')(page);
    const line = page.getByTestId('declare').getByTestId('declare-pr-cli');
    await expect(line).toContainText('Open a pull request with your own Git token from your machine:', { timeout: T });
    await expect(line).toContainText('pacto declare legacy-billing --namespace shop --registry demo.pacto.local --pr');
    await expect(line).not.toContainText('--intent');
    await expect(line.getByRole('button', { name: 'Copy pull request command' })).toBeVisible();
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(line).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    const box = await line.boundingBox();
    expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(390);
    await expect(line.getByRole('button', { name: 'Copy pull request command' })).toBeVisible();
    expect(errors).toEqual([]);
  });
});

test.describe('declare on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

  test('the panel fits a 390px screen and its controls are 44px targets', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => (window as unknown as { __pactoReady?: Promise<void> }).__pactoReady);
    await declareRoute('')(page);
    const panel = page.getByTestId('declare');
    await panel.getByTestId('declare-contract').locator('> summary').click();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    // A range's whole label toggles its checkbox, a 44px target with the 24px box inside.
    for (const target of [panel.getByTestId('declare-save-draft'), panel.getByRole('button', { name: 'Copy command' }).first(), panel.locator('label.fpf-control').first()]) {
      const box = await target.boundingBox();
      expect(box?.height ?? 0).toBeGreaterThanOrEqual(44);
    }
  });
});
