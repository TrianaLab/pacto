import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// Managed cloud resources (517-S7) against the built WASM demo. The demo cluster's
// operator read two Crossplane RDS instances and one queue from a listed Terraform state
// (examples/demo/source_managed.go): orders-db is outside the contract and two shop
// workloads read its connection Secret; ticketing-db went through the postgresql offering,
// named by the platform adapter's signed report on its PostgreSQLInstance claim; the
// queue order-events is outside the contract. Cloud inventory is off: nobody named the
// cloud in controller.cloudInventory.

const T = 30_000;
const ORDERS_DB = 'aws/rds/123456789012/eu-west-1/orders-db';
const TICKETING_DB = 'aws/rds/123456789012/eu-west-1/ticketing-db';
const ORDER_EVENTS = 'aws/sqs/123456789012/eu-west-1/order-events';

async function audit(page: Page, what: string) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

test.describe('WASM demo — managed resources', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('orders-db lists its resource outside the contract, and both claim paths open the panel on Offering', async ({ page }) => {
    await page.goto(`/#/fleet/externals/${ORDERS_DB}`);
    const card = page.getByTestId('external-resource');
    await expect(card).toHaveCount(1, { timeout: T });
    await expect(page.getByTestId('external-resources-caption')).toHaveText('1 managed resource read for it; 1 outside the contract.');
    await expect(page.getByTestId('external-resource-group').getByRole('heading', { level: 3 })).toHaveText('Crossplane');
    await expect(card).toContainText('orders-db');
    await expect(card).toContainText(/Instance\s*·\s*production/);
    await expect(card.getByTestId('external-resource-engine')).toHaveText('postgres 16');
    await expect(card.getByTestId('external-resource-contract')).toHaveText('Outside contract');
    // The consumers are the two shop workloads that read its connection Secret.
    await expect(page.getByTestId('external-consumers').locator('tbody tr')).toHaveCount(2);
    // The observed engine moved, and no source checked either inferred revision.
    const block = page.getByTestId('external-inferred-contract');
    await block.locator('summary').first().click();
    await expect(block.getByTestId('external-provider-change')).toContainText('unchecked');
    await audit(page, 'orders-db');

    await card.getByTestId('resource-claim-join').click();
    const offering = page.getByTestId('claim-offering');
    await expect(offering).toBeFocused({ timeout: T });
    await expect(offering).toHaveAttribute('placeholder', 'An offering of the fleet');
    await card.getByTestId('resource-claim-propose').click();
    await expect(offering).toBeFocused();
    await expect(offering).toHaveAttribute('placeholder', 'A new offering name');
  });

  test('ticketing-db reads through the postgresql offering and offers no claim', async ({ page }) => {
    await page.goto(`/#/fleet/externals/${TICKETING_DB}`);
    const card = page.getByTestId('external-resource');
    await expect(card).toHaveCount(1, { timeout: T });
    await expect(card.getByTestId('external-resource-contract')).toHaveText('Through offering postgresql');
    await expect(card.getByRole('link', { name: 'postgresql' })).toHaveAttribute('href', /#\/fleet\/offerings\/postgresql$/);
    await expect(card.getByTestId('resource-claim-join')).toHaveCount(0);
  });

  test('the Outside contract chip keeps orders-db and order-events', async ({ page }) => {
    await page.goto('/#/fleet/externals');
    const list = page.getByTestId('external-list');
    await expect(list).toBeVisible({ timeout: T });
    await page.getByTestId('external-outside-contract').click();
    await expect(page).toHaveURL(/outsideContract=1/);
    await expect(list.locator(`a.entity-link[href*="/fleet/externals/${TICKETING_DB}"]`)).toHaveCount(0, { timeout: T });
    await expect(list.locator(`a.entity-link[href*="/fleet/externals/${ORDERS_DB}"]`)).toHaveCount(1);
    await expect(list.locator(`a.entity-link[href*="/fleet/externals/${ORDER_EVENTS}"]`)).toHaveCount(1);
    await expect(list.locator('tbody tr')).toHaveCount(2);
  });

  test('the Overview reports the share through offerings and the map counts orders-db outside the contract', async ({ page }) => {
    await page.goto('/#/fleet');
    await expect(page.getByTestId('figure-match-resources')).toHaveText('Resources through offerings: 1 of 3', { timeout: T });
    await page.goto('/#/fleet/graph');
    await expect(page.getByTestId('fleet-map-summary')).toBeVisible({ timeout: T });
    const ladder = page.getByRole('group', { name: 'Level of detail' });
    if (await ladder.isVisible()) await ladder.getByRole('button', { name: 'Names' }).click();
    await page.evaluate(() => (window as unknown as { __fleetMap: { idle: () => Promise<void> } }).__fleetMap.idle());
    await expect.poll(() => page.evaluate((k) => (window as unknown as { __fleetMap: { select: (k: string) => boolean } }).__fleetMap.select(k), ORDERS_DB), { timeout: T }).toBe(true);
    await expect(page.getByTestId('fleet-map-selection')).toContainText('1 resource outside contract');
    await expect(page.getByTestId('fleet-map-live')).toContainText('1 resource outside contract');
    await expect(page.getByTestId('fleet-map')).toContainText('Resources outside contract');
  });

  test('Data sources lists the three managed readers, cloud inventory off with its opt-in', async ({ page }) => {
    await page.goto('/#/fleet/sources');
    const block = page.getByTestId('detected-providers');
    await expect(block).toBeVisible({ timeout: T });
    await block.getByRole('button', { name: /^Show all \d+ providers$/ }).click();
    const row = (name: string) => block.getByTestId('provider-row').filter({ has: page.locator(`.dp-name bdi[title="${name}"]`) });
    await expect(row('Managed resources')).toContainText('2 managed resources');
    await expect(row('Terraform state')).toContainText('s3://acme-tf/prod.tfstate?region=eu-west-1');
    const inventory = row('Cloud inventory');
    await expect(inventory).toHaveCount(1);
    await expect(inventory).toContainText('off');
    await expect(inventory).toHaveClass(/dp-inferred/);
  });
});
