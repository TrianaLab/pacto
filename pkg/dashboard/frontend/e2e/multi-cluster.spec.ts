import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';
import { chooseRevision, installScaleFixture } from './layoutGate';

// 514-S2: with two or more clusters the Services and Targets lists filter by cluster and
// caption the clusters the answer cannot show, and Data sources lists each cluster's
// status. The seven-cluster answer comes from the scale fixture behind __pactoMultiCluster;
// the demo itself is a hub with two members (514-S3).

const DEMO = '5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11';
const HIDDEN = '9f1c4e2a-7f1a-4c55-9d1e-2b8a4e6f0a12';

async function multiCluster(page: Page): Promise<void> {
  await page.addInitScript(installScaleFixture);
  await page.addInitScript(() => { (window as unknown as { __pactoMultiCluster?: boolean }).__pactoMultiCluster = true; });
  await appReady(page);
}

test('the Cluster filter narrows the Services list and captions the hidden clusters', async ({ page }) => {
  await multiCluster(page);
  await page.goto('/#/fleet/services');
  const caption = page.getByTestId('cluster-caption');
  await expect(caption).toContainText('Showing 3 of the 7 listed clusters; more are not listed (production-us-east-2', { timeout: 30_000 });
  await expect(caption).toContainText(': no access, production-ap-south-1');
  await expect(caption).toContainText(': review failed, production-me-central-1');
  await expect(caption).toContainText(': not reviewable, context-staging-unidentified');
  await expect(caption).toContainText(': unreachable)');
  const select = page.getByLabel('Filter by cluster');
  // The context with no id cannot be filtered by: Any cluster plus six.
  await expect(select.locator('option')).toHaveCount(7);
  await select.selectOption(DEMO);
  await expect(page).toHaveURL(new RegExp(`cluster=${DEMO}`));
  await expect(page.getByTestId('service-list').locator('li').first()).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('.chip', { hasText: 'Cluster' })).toContainText('production-eu-west-1');
  await page.getByLabel('Filter by cluster').selectOption(HIDDEN);
  await expect(page).toHaveURL(new RegExp(`cluster=${HIDDEN}`));
  await expect(page.getByTestId('service-list')).toHaveCount(0, { timeout: 30_000 });
});

test('the Targets list keeps the cluster across paging and chip removal', async ({ page }) => {
  await multiCluster(page);
  await page.goto(`/#/fleet/targets?cluster=${DEMO}&limit=5`);
  await expect(page.getByTestId('cluster-caption')).toBeVisible({ timeout: 30_000 });
  const next = page.locator('main a[href*="offset=5"]').first();
  await expect(next).toHaveAttribute('href', new RegExp(`cluster=${DEMO}`));
  await next.click();
  await expect(page).toHaveURL(new RegExp(`cluster=${DEMO}.*offset=5|offset=5.*cluster=${DEMO}`));
  await page.goto(`/#/fleet/targets?text=pay&cluster=${DEMO}`);
  await page.locator('.chip', { hasText: 'Search' }).getByRole('button').click();
  await expect(page).toHaveURL((u) => u.hash === `#/fleet/targets?cluster=${DEMO}`);
});

test('Data sources lists every cluster with its status word and reason', async ({ page }) => {
  await multiCluster(page);
  await page.goto('/#/fleet/sources');
  const chips = page.getByTestId('source-cluster');
  await expect(chips).toHaveCount(7, { timeout: 30_000 });
  await expect(chips.locator('.sc-word')).toHaveText(['complete', 'no access', 'review failed', 'partial', 'not reviewable', 'unreachable', 'stale']);
  await expect(chips.nth(5)).toHaveClass(/tone-err/);
  await expect(chips.nth(6)).toHaveClass(/tone-warn/);
  await chips.nth(2).getByRole('button').click();
  await expect(chips.nth(2).getByRole('note')).toContainText('took longer than 5s');
  await expect(page.getByTestId('source-clusters')).toContainText('Showing the 7 least healthy clusters.');
});

test('the demo is a hub and shows the filter and the Clusters block with its members', async ({ page }) => {
  await appReady(page);
  await page.goto('/#/fleet/services');
  await expect(page.getByTestId('service-list')).toBeVisible({ timeout: 30_000 });
  await expect(page.getByLabel('Filter by cluster')).toBeVisible();
  await page.goto('/#/fleet/targets');
  await expect(page.getByTestId('entity-list')).toBeVisible({ timeout: 30_000 });
  await expect(page.getByLabel('Filter by cluster')).toBeVisible();
  await page.goto('/#/fleet/sources');
  const block = page.getByTestId('source-clusters');
  await expect(block).toBeVisible({ timeout: 30_000 });
  await expect(block).toContainText('eu-prod');
  await expect(block).toContainText('us-west');
  await expect(page.getByTestId('cluster-join-row')).toContainText('ap-dev');
});

// 514-S7: api-gateway also runs in the member eu-prod, and its range refuses
// payments-service 2.0.1. Change analysis names that target by its cluster's display
// name, never by uid, blocks the change, and says the pending ap-dev is not read.
test('a member cluster consumer blocks the breaking change, named by its cluster', async ({ page }) => {
  await appReady(page);
  await page.goto('/#/fleet/changes/payments-service');
  await chooseRevision(page, '#impact-old-rev', '1.0.0');
  await chooseRevision(page, '#impact-new-rev', '2.0.1');
  await page.getByRole('button', { name: /Compare revisions/ }).click();
  await expect(page.getByTestId('impact-gate')).toContainText('Blocked', { timeout: 30_000 });
  const fold = page.locator('#sec-affected-consumers');
  if ((await fold.getAttribute('open')) === null) await fold.locator('> summary').click();
  const row = page.getByTestId('consumer-row')
    .filter({ has: page.locator('td[data-label="Consumer"]').getByRole('link', { name: 'Service api-gateway', exact: true }) });
  await expect(row.locator('td[data-label="Runs on"]')).toContainText('eu-prod / ', { timeout: 30_000 });
  await expect(row.locator('td[data-label="Runs on"]')).toContainText('production / ');
  const lims = page.getByTestId('changes-limitations');
  if ((await lims.getAttribute('open')) === null) await lims.locator('> summary').click();
  await expect(lims).toContainText('1 cluster publishes to demo.pacto.local/pacto-targets and is not a member of this hub');
  // Every label reads a display name: the uid lives only in titles and hrefs.
  expect(await page.locator('main').innerText()).not.toMatch(/[0-9a-f]{8}-[0-9a-f]{4}-/);
});
