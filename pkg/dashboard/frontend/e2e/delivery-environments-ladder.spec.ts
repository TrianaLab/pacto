import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// The installation ladder in the browser demo (examples/demo/source_delivery.go), with
// no mock: the chart declares test then prod, a person confirmed the preview leaf, Pacto
// proposes perf from three namespaces, payments' Stages are the ones Pacto created and
// checkout's preview environment could not be placed.

const T = 30_000;

async function switchTo(page: Page, who: 'pay-agent' | 'alice' | 'carol') {
  await page.getByTestId('identity-trigger').click();
  await page.getByRole('button', { name: 'Switch person' }).click();
  await page.getByTestId(`demo-persona-${who}`).click();
  await expect(page.getByTestId('demo-signin')).toHaveCount(0);
}

async function inbox(page: Page) {
  await page.goto('/#/delivery/inbox');
  await expect(page.getByTestId('demo-delivery-start')).toBeVisible({ timeout: T });
  const ladder = page.getByTestId('inbox-ladder');
  await expect(ladder).toBeVisible({ timeout: T });
  return ladder;
}

async function audit(page: Page, what: string) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

test('the inbox offers the installation ladder with its sources and the exact command', async ({ page }) => {
  await inbox(page);
  await switchTo(page, 'alice');
  const ladder = page.getByTestId('inbox-ladder');
  await expect(ladder.locator('summary')).toContainText('Installation ladder: test → prod (from the chart) + preview + perf proposed', { timeout: T });
  await ladder.locator('summary').click();
  await expect(ladder).toContainText('preview: confirmed by a person, namespaces *-preview, 2 found');
  await expect(ladder.getByTestId('ladder-rung')).toHaveText('perf: inferred from namespaces *-perf, 3 found');
  await ladder.getByRole('button', { name: 'Confirm', exact: true }).click();
  await expect(ladder.getByTestId('ladder-confirm')).toContainText(/pacto delivery ladder confirm --proposal sha256:[0-9a-f]{64}/);
  await audit(page, 'inbox, installation ladder');
});

test('an environment row says where it runs and who confirmed it in its tooltip', async ({ page }) => {
  await page.goto('/#/delivery/environments');
  const prod = page.locator('[data-testid="env-cell"][data-stage="prod/payments"]');
  await prod.getByRole('button', { name: 'Where payments runs in prod' }).click({ timeout: T });
  await expect(prod.locator('.help-body')).toHaveText(
    'From the installation ladder the chart declares. In namespace prod, placement default (the namespace named after the environment), confirmed by alice@example.com.');
  const inTest = page.locator('[data-testid="env-cell"][data-stage="test/payments"]');
  await inTest.getByRole('button', { name: 'Where payments runs in test' }).click();
  await expect(inTest.locator('.help-body')).toContainText('placement observed (Argo CD Application test/payments already runs it)');
  // A Stage a person wrote is marked as one.
  const orders = page.locator('[data-testid="env-cell"][data-stage="prod/orders"]');
  await orders.getByRole('button', { name: 'Where orders runs in prod' }).click();
  await expect(orders.locator('.help-body')).toContainText('From a Stage a person wrote.');
});

test('an unplaced environment says why in the collapsed details', async ({ page }) => {
  await page.goto('/#/delivery/environments');
  const unplaced = page.getByTestId('environments-unplaced');
  await expect(unplaced).not.toHaveAttribute('open', /.*/, { timeout: T });
  await unplaced.locator('summary').click({ timeout: T });
  await expect(unplaced.locator('summary')).toHaveText('1 environment not placed');
  await expect(unplaced.getByTestId('unplaced-row')).toHaveText('checkout in preview (namespace orders): namespace orders belongs to orders (team-orders)');
  await audit(page, 'environments, unplaced open');
});

test('a person without confirm rights sees no Confirm', async ({ page }) => {
  // The default persona is the pay-agent: an agent proposes, people decide.
  const ladder = await inbox(page);
  await ladder.locator('summary').click();
  await expect(ladder.getByTestId('ladder-rung')).toBeVisible();
  await expect(ladder.getByRole('button', { name: 'Confirm', exact: true })).toHaveCount(0);
});
