import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';
import { createServiceRoute, installScaleFixture } from './layoutGate';

// Create a service from a golden path (515-S6). The demo's own creator plans
// http-service into demo.pacto.local/<name>, signs nothing ("the demo has no sign-in")
// and keeps saved drafts in the tab, so the plain flow runs against it; the states the
// demo never reaches (a signed publish, a review denial, a taken name, a tiered field
// refused and a configured portal) come from the scale fixture.

const T = 30_000;

// A fixture's 403 or 409 answer is logged by the browser as a failed resource load;
// that line is the answer under test, never an error of the page.
function watchConsole(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error' && !m.text().startsWith('Failed to load resource')) errors.push(m.text()); });
  page.on('pageerror', (e) => errors.push(e.message));
  return errors;
}

test.describe('create a service from a golden path', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('the demo plans http-service, nobody signs and Save draft lists draft-1', async ({ page }) => {
    const errors = watchConsole(page);
    await appReady(page);
    await page.goto('/#/fleet/services/http-service');
    const panel = page.getByTestId('create-service');
    await panel.getByTestId('create-name').fill('invoices', { timeout: T });
    await panel.getByTestId('create-owner').fill('billing');
    await panel.getByTestId('create-namespace').fill('team-billing');
    await panel.getByTestId('create-review').focus();
    await page.keyboard.press('Enter');
    await expect(panel.getByTestId('create-plan-heading')).toHaveText('Plan: invoices 0.1.0', { timeout: T });
    await expect(panel.getByTestId('create-plan-heading')).toBeFocused();
    await expect(panel.getByTestId('create-plan')).toContainText('demo.pacto.local/invoices');
    await expect(panel.getByTestId('create-signature')).toHaveText('signature Unknown');
    await expect(panel.getByTestId('create-signer')).toContainText('the demo has no sign-in');
    await expect(panel.locator('[data-testid="create-offering"][data-offering="postgresql"]')).toContainText('17.0.0');
    await expect(panel.getByTestId('create-sign')).toHaveCount(0);

    await panel.getByTestId('create-save-draft').click();
    await expect(panel.getByTestId('create-result')).toContainText('Saved draft-1', { timeout: T });
    await panel.getByTestId('create-review').click();
    const drafts = panel.getByTestId('create-drafts');
    await drafts.locator('> summary').click({ timeout: T });
    await expect(drafts.getByTestId('create-draft')).toHaveCount(1);
    await expect(drafts.getByTestId('create-draft').first()).toContainText('draft-1');
    expect(errors).toEqual([]);
  });

  test('the demo refuses a value outside the offering at its field', async ({ page }) => {
    const errors = watchConsole(page);
    await appReady(page);
    await page.goto('/#/fleet/services/http-service');
    const panel = page.getByTestId('create-service');
    await panel.getByTestId('create-name').fill('invoices', { timeout: T });
    await panel.getByTestId('create-namespace').fill('team-billing');
    await panel.getByTestId('create-review').click();
    await panel.locator('[data-testid="create-param"][data-field="storageGi"] input').first().fill('2000', { timeout: T });
    await panel.getByTestId('create-review').click();
    const at = panel.locator('[data-testid="create-param"][data-field="storageGi"]').first().getByTestId('create-refusal');
    await expect(at).toHaveAttribute('data-code', 'ParameterOutOfRange', { timeout: T });
    await expect(at.getByTestId('create-closest')).toContainText('closest 1000');
    await expect(panel.getByTestId('create-save-draft')).toHaveCount(0);
    expect(errors).toEqual([]);
  });

  test.describe('fixture states', () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript(installScaleFixture);
      await appReady(page);
    });

    test('a signed publish shows the reference, the signer, the commands and the zip', async ({ page }) => {
      const errors = watchConsole(page);
      await createServiceRoute('published', 'invoices', 'team-billing')(page);
      const result = page.getByTestId('create-service').getByTestId('create-result');
      await expect(result).toBeFocused();
      await expect(result).toContainText(/Published ghcr\.io\/.+\/invoices@sha256:[0-9a-f]{64} by alice@acme\.io/);
      await expect(result).toContainText('It shows in the fleet within a minute, declared at 0.1.0 with nothing delivered');
      await expect(result).toContainText(/Start the folder.*pacto init invoices --from oci:\/\//s);
      await expect(result).toContainText(/Optional, with your own Git token.*gh repo create <org>\/invoices --private --clone/s);
      const download = page.waitForEvent('download');
      await result.getByTestId('create-zip').click();
      expect((await download).suggestedFilename()).toBe('invoices.zip');
      expect(errors).toEqual([]);
    });

    test('a review denial is a 403 with no Sign or Save', async ({ page }) => {
      const errors = watchConsole(page);
      await createServiceRoute('forbidden', 'invoices', 'team-billing')(page);
      const panel = page.getByTestId('create-service');
      await expect(panel.getByTestId('refusal')).toContainText('alice may not create services in team-billing');
      await expect(panel.getByTestId('create-sign')).toHaveCount(0);
      await expect(panel.getByTestId('create-save-draft')).toHaveCount(0);
      expect(errors).toEqual([]);
    });

    test('a taken name shows ServiceExists verbatim with its command', async ({ page }) => {
      const errors = watchConsole(page);
      await createServiceRoute('refused', 'invoices', 'team-billing')(page);
      const panel = page.getByTestId('create-service');
      await expect(panel.getByTestId('refusal')).toHaveAttribute('data-code', 'ServiceExists');
      await expect(panel.getByTestId('refusal')).toContainText('invoices is already taken in this registry: pick another name, or adopt your own service with pacto declare');
      await expect(panel.getByTestId('create-refusal-command')).toContainText('pacto init invoices --from oci://');
      await expect(panel.getByTestId('create-sign')).toHaveCount(0);
      expect(errors).toEqual([]);
    });

    test('a value over a tiered field is refused at it with the closest value and both commands', async ({ page }) => {
      const errors = watchConsole(page);
      await createServiceRoute('plan', 'invoices', 'team-billing')(page);
      const panel = page.getByTestId('create-service');
      await expect(panel.getByTestId('create-sign')).toBeVisible();
      await panel.locator('[data-testid="create-param"][data-field="storageGi"] input').first().fill('900');
      await panel.getByTestId('create-review').click();
      const at = panel.locator('[data-testid="create-param"][data-field="storageGi"]').first().getByTestId('create-refusal');
      await expect(at).toHaveAttribute('data-code', 'ParameterOutOfRange', { timeout: T });
      await expect(at.getByTestId('create-closest')).toContainText('closest 500');
      await expect(at).toContainText(/CLI.*pacto init invoices --from oci:\/\/\S+@sha256:[0-9a-f]{64} .*--set 'configurations\[0\]\.values\.storageGi=500'/s);
      await expect(at).toContainText('pacto_create_service {"offerings":[{"offering":"postgresql","values":{"storageGi":500}}]}');
      await expect(panel.getByTestId('create-sign')).toHaveCount(0);
      await expect(panel.getByTestId('create-save-draft')).toHaveCount(0);
      expect(errors).toEqual([]);
    });

    test('a configured portal leads with Open in Backstage', async ({ page }) => {
      const errors = watchConsole(page);
      await createServiceRoute('portal')(page);
      const actions = page.getByTestId('create-service').getByTestId('create-portal');
      await expect(actions.locator('a, button').first()).toHaveText('Open in Backstage');
      await expect(actions.getByTestId('create-here')).toBeVisible();
      await expect(page.getByTestId('create-form')).toHaveCount(0);
      expect(errors).toEqual([]);
    });
  });
});
