import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';
import { touchProblems } from './layoutGate';

// The delivery lifecycle journeys of #466, over the browser demo's simulated
// delivery (examples/demo/source_delivery.go): the service page's Stage rows, Why
// card and offers, the Environments section beside them (#467), the history filters and record panel, the
// revision page's lifecycle list and the target page's link back to its Stage.
// The demo signs in as pay-agent, an agent, so its offers carry the agent's
// refusals. Every test opens its own page, so the file passes alone in any shard.

const T = 30_000;
const PAYMENTS = '#/fleet/services/registry.example%252Fstory%2Fpayments';
// payments 2.3.0, the version prod runs: a revision both payments Stages reached.
const PAYMENTS_230 = '#/fleet/revisions/registry.example%252Fstory%2Fpayments@sha256:8c4f595146e5154efc1fe8d58960e0632e2d3668eb390fa75da0db6390a6494a';

async function audit(page: Page, what: string) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

/** Opens a service page and returns its Stage row in env, once the rows are drawn. */
async function stageRow(page: Page, hash: string, env: string) {
  await page.goto(`/${hash}`);
  const rows = page.getByTestId('service-delivery-row');
  await expect(rows.first()).toBeVisible({ timeout: T });
  return rows.filter({ has: page.locator('.sd-env', { hasText: new RegExp(`^${env}$`) }) });
}

test.describe('delivery lifecycle in the demo', () => {
  test('the service page lists each Stage with its state and offers, never a false empty', async ({ page }) => {
    const prod = await stageRow(page, PAYMENTS, 'prod');
    const delivery = page.getByTestId('service-delivery');
    await expect(page.getByTestId('service-delivery-row')).toHaveCount(2);
    // Rows are on screen, so the empty copy never is; a partial listing says so beside them.
    await expect(delivery.getByText('No Stages for this service')).toHaveCount(0);
    await expect(delivery.getByText('No Stages of this service that you can see')).toHaveCount(0);
    await expect(prod.locator('[data-testid="env-state-mark"]')).toHaveAttribute('data-state', 'verified');
    await expect(prod.getByText(/^Re-checked .*, Pass$/)).toBeVisible();
    await expect(prod.getByText('Watched: the newest re-check passed')).toBeVisible();
    // An offer the agent may not take is disabled, with its reason as visible text.
    const freeze = prod.getByRole('button', { name: 'Freeze payments in prod' });
    await expect(freeze).toBeDisabled();
    await expect(prod.getByText('Freeze: an agent may not raise a freeze')).toBeVisible();
    // The version links to its revision page.
    await expect(prod.locator('a[href*="/fleet/revisions/"]').first()).toBeVisible();
    // The source revision pacto publish stamped, labelled as the unsigned annotation it is.
    await expect(prod.getByText(/^Source revision [0-9a-f]{40}, an annotation, not signed$/)).toBeVisible();
    await audit(page, 'service page with Stages');
  });

  test('a Stage that cannot promote shows the Ready fix, and Why answers as the caller', async ({ page }) => {
    const prod = await stageRow(page, '#/fleet/services/audit-log', 'prod');
    await expect(prod.getByText(/^Cannot promote: .*sets no spec\.verify/)).toBeVisible();
    await expect(prod.getByText(/^Fix: Set spec\.verify on the candidate OCIRepository/)).toBeVisible();
    await expect(prod.locator('[data-testid="env-state-mark"]')).toHaveAttribute('data-state', 'empty');
    await expect(prod.locator('.env-word')).toHaveText('running nothing yet');

    const pay = await stageRow(page, PAYMENTS, 'prod');
    await pay.locator('details.sd-why summary').click();
    const why = pay.getByTestId('service-delivery-why');
    await expect(why).toBeVisible({ timeout: T });
    await expect(why.getByText('Needs a person', { exact: false }).first()).toBeVisible();
    await expect(why.getByText(/^As of$/)).toBeVisible();
    // The agent may propose, so the fix is the propose command; it gets no approve command.
    await expect(why.getByTestId('why-command').first()).toContainText('pacto delivery propose -n prod --stage payments');
    await expect(why.getByText('pacto delivery approve')).toHaveCount(0);
    await expect(why.getByText('What happens next')).toBeVisible();
    await audit(page, 'service page, Why card open');
  });

  test('an available undo opens the undo panel and proposes nothing on its own', async ({ page }) => {
    const test_ = await stageRow(page, PAYMENTS, 'test');
    const undo = test_.getByRole('button', { name: 'Undo payments in test' });
    await expect(undo).toBeEnabled();
    let wrote = false;
    page.on('request', (r) => { if (r.method() !== 'GET' && r.url().includes('/api/delivery/operations')) wrote = true; });
    await undo.click();
    await expect(test_.getByTestId('undo-panel')).toBeVisible({ timeout: T });
    expect(wrote, 'opening the undo panel must not propose').toBe(false);
  });

  test.describe('on a touch screen', () => {
    test.use({ hasTouch: true, viewport: { width: 390, height: 844 } });
    test('a Stage row\'s version, History and held links are touch targets', async ({ page }) => {
      const prod = await stageRow(page, PAYMENTS, 'prod');
      expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches)).toBe(true);
      for (const link of await prod.locator('p.sd-line.go a').all()) {
        const box = await link.boundingBox();
        expect(box && box.width >= 43.5 && box.height >= 43.5, `${await link.textContent()} is ${box?.width}x${box?.height}`).toBe(true);
      }
      expect(await touchProblems(page)).toEqual([]);
    });
  });

  test('the published versions of a service name each source revision as an unsigned annotation', async ({ page }) => {
    await page.goto('/#/delivery/publish?service=payments');
    await expect(page.getByText(/^Source revision [0-9a-f]{40}, an annotation, not signed$/).first()).toBeVisible({ timeout: T });
    await audit(page, 'publish, source revisions');
  });

  test('a service with no Stage shows its environments, observed from targets', async ({ page }) => {
    await page.goto('/#/fleet/services/storefront-web');
    const section = page.locator('#sec-environments');
    const row = section.locator('[data-testid="service-environment"][data-env="production"]');
    await expect(row).toBeVisible({ timeout: T });
    await expect(section.getByTestId('environments-footer')).toContainText(/^As of /);
    // Its target is linked to 1.1.0 by a mutable tag only: inferred, so neither
    // declared nor a revision it is known to run.
    await expect(row.locator('[data-cell="observed"]')).toContainText('1 unknown');
    await expect(row.locator('[data-cell="observed"] a[href*="/fleet/revisions/"]')).toHaveCount(0);
    await expect(page.getByTestId('service-delivery-row')).toHaveCount(0);
    await audit(page, 'service page, environments without Stages');
  });

  test('targets in an environment no Stage covers show beside the Stages', async ({ page }) => {
    await stageRow(page, '#/fleet/services/notification-worker', 'prod');
    const staging = page.locator('#sec-environments [data-testid="service-environment"][data-env="staging"]');
    await expect(staging).toBeVisible({ timeout: T });
    await expect(staging.locator('[data-cell="observed"]')).toContainText('1 unknown');
    await expect(staging.locator('[data-cell="stage"]')).toContainText('No Stage you can see');
    // prod is a Stage only: notification-worker declares no prod and no target you can see reports it.
    const prod = page.locator('#sec-environments [data-testid="service-environment"][data-env="prod"]');
    await expect(prod.locator('[data-cell="declared"]')).toContainText('Stage only: not declared by');
    await expect(prod.locator('[data-cell="stage"] a[href*="stage="]')).toBeVisible();
    await audit(page, 'service page, environments beside Stages');
  });

  test('a Stage joined to an observed environment says an admin declared it', async ({ page }) => {
    await stageRow(page, '#/fleet/services/audit-log', 'prod');
    const stage = page.locator('#sec-environments [data-testid="service-environment"][data-env="prod"] [data-cell="stage"]');
    await expect(stage).toContainText('state observed, environment declared by a Stage (admin)', { timeout: T });
    await expect(stage.locator('[data-testid="env-state-mark"]')).toBeVisible();
    await expect(stage.locator('a[href*="/delivery/history"]')).toHaveAttribute('href', /namespace=prod&stage=audit-log/);
    await audit(page, 'service page, environment joined to its Stage');
  });

  test('history filters by correlation id and cause, and a record reads in its panel', async ({ page }) => {
    // The every-Stage view keeps each Stage: prod/payments shows the change B1's
    // ticket names, and every other Stage says none of its changes match.
    await page.goto('/#/delivery/history');
    const filters = page.getByTestId('history-filters');
    const summary = page.getByTestId('history-summary');
    await expect(summary).toBeVisible({ timeout: T });
    await filters.getByLabel('Correlation id').fill('CHG-2041');
    await filters.getByRole('button', { name: 'Filter' }).click();
    await expect(page).toHaveURL(/correlationID=CHG-2041/);
    await expect(summary.locator('[data-stage="prod/payments"] a[href*="payments-promote-2"]')).toBeVisible({ timeout: T });
    await expect(summary.locator('[data-stage="test/payments"]')).toContainText('None matches the filter');
    await expect(summary.getByText('Rejected')).toHaveCount(0);
    await filters.getByRole('link', { name: 'Clear cause and correlation' }).click();
    await expect(page).not.toHaveURL(/correlationID=/);
    await expect(summary.getByText('Rejected').first()).toBeVisible({ timeout: T });

    // One Stage, by cause: the failed health check's rollback and step-down.
    await page.goto('/#/delivery/history?namespace=prod&stage=payments');
    const rows = page.locator('section.hv-stage tbody tr');
    await expect(rows.first()).toBeVisible({ timeout: T });
    await filters.getByLabel('Cause').fill('error ratio');
    await filters.getByRole('button', { name: 'Filter' }).click();
    await expect(page).toHaveURL(/cause=error/);
    await expect(rows).toHaveCount(2, { timeout: T });

    // An undo the caller may not take says why, in the row.
    await page.goto('/#/delivery/history?namespace=prod&stage=payments');
    await expect(page.getByTestId('history-undo-reason').first()).toHaveText(/^No undo: /, { timeout: T });

    // One Stage: a record id opens the record panel with its verdicts.
    await page.goto('/#/delivery/history?namespace=prod&stage=payments');
    await page.locator('button[aria-label^="Read record"]').first().click();
    const panel = page.getByTestId('record-panel');
    await expect(panel).toBeVisible({ timeout: T });
    await expect(panel.getByTestId('record-verdicts')).toBeVisible({ timeout: T });
    await audit(page, 'history, record panel open');
  });

  test('a revision lists its way through each Stage, with records', async ({ page }) => {
    await page.goto(`/${PAYMENTS_230}`);
    const life = page.getByTestId('revision-delivery');
    await expect(life.locator('.lc-stage').first()).toBeVisible({ timeout: T });
    await expect(life.getByText('Signature checked')).toBeVisible();
    await expect(life.locator('.lc-stage')).toHaveCount(2);
    await expect(life.getByText(/^record [0-9a-f]{12}$/).first()).toBeVisible();
    // The history of test/payments is partial: what is missing is stated, never "not reached".
    await expect(life.getByText(/partial or was not read/)).toBeVisible();
    await expect(life.getByText('not reached')).toHaveCount(0);
    await expect(life.getByText('Version 2.3.0')).toBeVisible();
    await expect(life.getByTestId('lifecycle-source')).toHaveText(/^Source revision [0-9a-f]{40}, an annotation, not signed$/);
    await audit(page, 'revision page, Delivery lifecycle list');
  });

  test('a revision of a service no Stage delivers says so, from a complete search', async ({ page }) => {
    await page.goto('/#/fleet/revisions?service=storefront-web');
    const first = page.locator('main a[href*="/fleet/revisions/"]').first();
    await expect(first).toBeVisible({ timeout: T });
    await page.goto(`/${await first.getAttribute('href')}`);
    const unmatched = page.getByTestId('lifecycle-unmatched');
    await expect(unmatched).toContainText('No Stage of this service you can see names this revision.', { timeout: T });
    await expect(unmatched).toContainText('Complete');
    await audit(page, 'revision page, no Stage names it');
  });

  test('a target in an environment no Stage delivers says it is observed', async ({ page }) => {
    await page.goto('/#/fleet/targets/staging%2FDeployment%2Fnotification-worker');
    await expect(page.getByTestId('target-delivery')).toContainText('No Stage you can see delivers this service here', { timeout: T });
    await audit(page, 'target page, no Stage delivers it here');
  });

  test('the audit-log prod target links to its Stage', async ({ page }) => {
    await page.goto('/#/fleet/targets/prod%2FCronJob%2Faudit-log');
    const link = page.getByTestId('target-delivery').getByRole('link', { name: 'Delivery in prod' });
    await expect(link).toBeVisible({ timeout: T });
    await expect(link).toHaveAttribute('href', /namespace=prod&stage=audit-log/);
    await link.click();
    await expect(page.getByRole('heading', { name: 'History of prod/audit-log' })).toBeVisible({ timeout: T });
    await audit(page, 'Delivery in prod, from the target');
  });
});
