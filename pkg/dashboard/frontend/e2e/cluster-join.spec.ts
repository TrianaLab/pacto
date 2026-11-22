import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';
import { installScaleFixture } from './layoutGate';

// 514-S3: on a hub, Data sources' Clusters block lists the pending members with their
// name and its source, the environments they seem to host, their key and an [Approve]
// that hands the person the exact command. The demo is a hub with two members and one
// pending cluster; the wide and restricted lists come from the scale fixture behind
// __pactoClusterJoin.

const KEY = '91ab'.repeat(16);

async function joins(page: Page, mode: true | 'viewer' | 'off' = true): Promise<void> {
  await page.addInitScript(installScaleFixture);
  await page.addInitScript((m) => { (window as unknown as { __pactoClusterJoin?: unknown }).__pactoClusterJoin = m; }, mode);
  await appReady(page);
  await page.goto('/#/fleet/sources');
}

test('a pending member shows its name, hosts, key and reason', async ({ page }) => {
  await joins(page);
  const rows = page.getByTestId('cluster-join-row');
  await expect(rows).toHaveCount(2, { timeout: 30_000 });
  const first = rows.first();
  await expect(first.locator('.sc-name')).toHaveAttribute('title', /^production-eu-north-1-x+$/);
  await expect(first.locator('.sc-key bdi')).toHaveText('sha256:91ab…b91ab');
  await expect(first.locator('.sc-key bdi')).toHaveAttribute('title', `sha256:${KEY}`);
  await expect(first.locator('.sc-hosts')).toContainText('hosts: production, staging, qa');
  await expect(first).toContainText('nobody approved this key yet');
  await expect(rows.nth(1).getByTestId('cluster-join-replace')).toHaveText('may replace eu-prod');
  await expect(page.getByTestId('cluster-join-counts')).toHaveText('1 record from another hub not used.');
});

test('Approve opens the command, the name field rebuilds it and Copy writes it', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await joins(page);
  const row = page.getByTestId('cluster-join-row').first();
  await row.getByRole('button', { name: /^Approve/ }).click();
  const panel = page.getByTestId('cluster-approve-panel');
  const cmd = panel.locator('.copyable-value');
  await expect(cmd).toHaveText(`pacto cluster approve 3c7e1b9d-7f1a-4c55-9d1e-2b8a4e6f0a21 --key sha256:${KEY} --registry ghcr.io/acme/pacto-targets`);
  await panel.getByLabel('Display name (optional)').fill('eu-north');
  await expect(cmd).toHaveText(`pacto cluster approve 3c7e1b9d-7f1a-4c55-9d1e-2b8a4e6f0a21 --key sha256:${KEY} --name eu-north --registry ghcr.io/acme/pacto-targets`);
  await panel.getByRole('button', { name: 'Copy command' }).click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toContain(`--key sha256:${KEY} --name eu-north --registry ghcr.io/acme/pacto-targets`);
});

test('a reader who may not approve sees no Approve and the hidden count', async ({ page }) => {
  await joins(page, 'viewer');
  await expect(page.getByTestId('cluster-join-counts')).toHaveText('3 clusters you may not approve; 1 record from another hub not used.', { timeout: 30_000 });
  await expect(page.getByTestId('cluster-join-row')).toHaveCount(0);
  await expect(page.getByTestId('cluster-approve')).toHaveCount(0);
});

test('removed members stay collapsed until opened', async ({ page }) => {
  await joins(page);
  const removed = page.getByTestId('cluster-join-removed');
  await expect(removed.locator('summary')).toHaveText('1 removed', { timeout: 30_000 });
  const name = removed.locator('.sc-name');
  await expect(name).toBeHidden();
  await removed.locator('summary').click();
  await expect(name).toBeVisible();
  await expect(removed).toContainText('Removed by oidc:');
});

test('a host with no join reader keeps the Clusters block without join rows or an error', async ({ page }) => {
  await joins(page, 'off');
  await expect(page.getByTestId('source-clusters')).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId('cluster-join-row')).toHaveCount(0);
  await expect(page.getByTestId('cluster-join-error')).toHaveCount(0);
  await expect(page.locator('[role="alert"]')).toHaveCount(0);
});

test('the demo without the flag renders the block with ap-dev pending and two members', async ({ page }) => {
  await appReady(page);
  await page.goto('/#/fleet/sources');
  const block = page.getByTestId('source-clusters');
  await expect(block).toBeVisible({ timeout: 30_000 });
  await expect(block).toContainText('eu-prod');
  await expect(block).toContainText('us-west');
  const rows = page.getByTestId('cluster-join-row');
  await expect(rows).toHaveCount(1);
  await expect(rows.first().locator('.sc-name')).toHaveText('ap-dev');
  await expect(rows.first().locator('.sc-hosts')).toContainText('hosts: dev');
  await expect(rows.first()).toContainText('no approval yet');
  await expect(rows.first().getByRole('button', { name: /^Approve/ })).toBeVisible();
});
