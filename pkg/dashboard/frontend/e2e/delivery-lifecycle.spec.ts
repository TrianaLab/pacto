import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// The delivery lifecycle journeys of #466, over the browser demo's simulated
// delivery (examples/demo/source_delivery.go): the service page's Stage rows, Why
// card, offers and observed strip, the history filters and record panel, the
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

  test('a service with no Stage shows where it runs, observed from targets', async ({ page }) => {
    await page.goto('/#/fleet/services/storefront-web');
    const strip = page.getByTestId('service-placement');
    await expect(strip).toBeVisible({ timeout: T });
    await expect(strip.getByRole('heading', { name: /^Where it runs \(observed/ })).toBeVisible();
    await expect(strip.getByText('Each row is observed from targets, not a promotion step.', { exact: false })).toBeVisible();
    await expect(strip.getByText(/^As of /)).toBeVisible();
    const row = strip.getByTestId('service-placement-row').filter({ hasText: 'production' });
    await expect(row).toContainText('1 declared');
    await expect(row.locator('a[href*="/fleet/revisions/"]')).toHaveText('1.1.0');
    await expect(page.getByTestId('service-delivery-row')).toHaveCount(0);
    await audit(page, 'service page, observed strip');
  });

  test('targets in an environment no Stage covers show beside the Stages', async ({ page }) => {
    await stageRow(page, '#/fleet/services/notification-worker', 'prod');
    const strip = page.getByTestId('service-placement');
    await expect(strip).toBeVisible({ timeout: T });
    await expect(strip.getByTestId('service-placement-row').filter({ hasText: 'staging' })).toContainText('1 unknown');
    await audit(page, 'service page, observed strip beside Stages');
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
    await audit(page, 'revision page, Delivery lifecycle list');
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
