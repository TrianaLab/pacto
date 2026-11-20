import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';
import { installScaleFixture } from './layoutGate';

// 514-S2: with two or more clusters the Services and Targets lists filter by cluster and
// caption the clusters the answer cannot show, and Data sources lists each cluster's
// status. The demo reads one cluster, so the three-cluster answer comes from the scale
// fixture behind __pactoMultiCluster; without the flag neither the filter nor the block shows.

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
  await expect(caption).toContainText('Showing 1 of 3 clusters (production-us-east-2', { timeout: 30_000 });
  await expect(caption).toContainText(': no access, production-ap-south-1');
  await expect(caption).toContainText(': review failed)');
  const select = page.getByLabel('Filter by cluster');
  await expect(select.locator('option')).toHaveCount(4);
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
  await expect(chips).toHaveCount(3, { timeout: 30_000 });
  await expect(chips.locator('.sc-word')).toHaveText(['complete', 'no access', 'review failed']);
  await chips.nth(2).getByRole('button').click();
  await expect(chips.nth(2).getByRole('note')).toContainText('took longer than 5s');
});

test('the demo reads one cluster and shows neither the filter nor the Clusters block', async ({ page }) => {
  await appReady(page);
  await page.goto('/#/fleet/services');
  await expect(page.getByTestId('service-list')).toBeVisible({ timeout: 30_000 });
  await expect(page.getByLabel('Filter by cluster')).toHaveCount(0);
  await page.goto('/#/fleet/targets');
  await expect(page.getByTestId('entity-list')).toBeVisible({ timeout: 30_000 });
  await expect(page.getByLabel('Filter by cluster')).toHaveCount(0);
  await page.goto('/#/fleet/sources');
  await expect(page.getByTestId('source-list')).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId('source-clusters')).toHaveCount(0);
});
